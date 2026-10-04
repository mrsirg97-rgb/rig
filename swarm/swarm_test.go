package swarm_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/evt"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
	"github.com/mrsirg97-rgb/rig/v2/swarm"
)

var proj = todostore.Project{Key: "swarm", Label: "swarm"}

var modelsFixture = []struct {
	ID     string
	Alias  []string
	Status string
}{
	{ID: "qwen3.8-27b", Alias: []string{"qwen3.8"}},
	{ID: "qwen3.8-27b-workers", Alias: []string{"qwen3.8-workers"}},
}

func modelRows(t *testing.T) models.Table {
	t.Helper()
	tbl, err := models.New(
		models.Model{ID: "qwen3.8-27b", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker},
		models.Model{ID: "qwen3.8-workers", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker},
		models.Model{ID: "qwen3.8-review", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker},
		models.Model{ID: "dsv4", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker},
	)
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	return tbl
}

func modelsJSON(statuses map[string]string) string {
	type alias struct {
		Aliases []string `json:"aliases"`
	}
	type meta struct {
		LLamaSwap alias `json:"llamaswap"`
	}
	type status struct {
		Value string `json:"value"`
	}
	type model struct {
		ID     string `json:"id"`
		Meta   meta   `json:"meta"`
		Status status `json:"status"`
	}
	var data []model
	for _, m := range modelsFixture {
		st := "unloaded"
		if statuses != nil {
			st = statuses[m.ID]
		}
		data = append(data, model{ID: m.ID, Meta: meta{LLamaSwap: alias{Aliases: m.Alias}}, Status: status{Value: st}})
	}
	b, _ := json.Marshal(map[string]any{"data": data, "object": "list"})
	return string(b)
}

func runningJSON(models ...string) string {
	type entry struct {
		Model string `json:"model"`
	}
	var rs []entry
	for _, m := range models {
		rs = append(rs, entry{Model: m})
	}
	b, _ := json.Marshal(map[string]any{"running": rs})
	return string(b)
}

type fetchState struct {
	mu       sync.Mutex
	busy     int
	failing  string
	resident []string
	slots    [][]bool
	reads    int
}

func (f *fetchState) fetch(url string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing != "" {
		return nil, jsonError(f.failing)
	}
	switch {
	case strings.HasSuffix(url, "/v1/models"):
		return json.RawMessage(modelsJSON(nil)), nil
	case strings.HasSuffix(url, "/running"):
		if f.busy > 0 {
			f.busy--
			return json.RawMessage(runningJSON("qwen3.8-27b")), nil
		}
		return json.RawMessage(runningJSON(f.resident...)), nil
	case strings.Contains(url, "/upstream/"):
		served := []bool{false}
		if len(f.slots) > 0 {
			idx := f.reads
			if idx > len(f.slots)-1 {
				idx = len(f.slots) - 1
			}
			served = f.slots[idx]
			f.reads++
		}
		type slot struct {
			ID           int  `json:"id"`
			IsProcessing bool `json:"is_processing"`
		}
		var out []slot
		for i, pr := range served {
			out = append(out, slot{ID: i, IsProcessing: pr})
		}
		b, _ := json.Marshal(out)
		return json.RawMessage(b), nil
	}
	return nil, jsonError("unexpected url " + url)
}

type jsonErr string

func (e jsonErr) Error() string { return string(e) }

func jsonError(s string) error { return jsonErr(s) }

type fakeSpawn struct {
	mu     sync.Mutex
	calls  []fakeCall
	queue  []sched.SpawnResult
	result sched.SpawnResult
	err    error
	onCall func(ctx context.Context)
	block  chan struct{}
}

type fakeCall struct {
	Argv []string
	Cwd  string
	Ctx  context.Context
}

func (f *fakeSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{Argv: argv, Cwd: cwd, Ctx: ctx})
	result := f.result
	if len(f.queue) > 0 {
		result = f.queue[0]
		f.queue = f.queue[1:]
	}
	f.mu.Unlock()
	if f.onCall != nil {
		f.onCall(ctx)
	}
	speakVerdict(ctx, result.Stdout)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
		}
	}
	return result, f.err
}

func speakVerdict(ctx context.Context, stdout string) {
	for _, line := range strings.Split(stdout, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "verdict:")
		if !ok {
			continue
		}
		id, w, ok := sched.FleetFrom(ctx)
		if !ok {
			panic("the spawn context carries no fleet pipe")
		}
		kind, reason, _ := strings.Cut(strings.TrimSpace(rest), " ")
		v := core.Verdict{Accept: kind == "accept", Reason: reason}
		broadcast.NewPipeTransport(id, w, broadcast.NewJSONEncoder()).Send(ctx, func(error) {}, broadcast.NewMessage(id, true, v))
	}
}

func beat(ctx context.Context, n int) {
	id, w, ok := sched.FleetFrom(ctx)
	if !ok {
		panic("the spawn context carries no fleet pipe")
	}
	wire := broadcast.NewPipeTransport(id, w, broadcast.NewJSONEncoder())
	for i := 0; i < n; i++ {
		wire.Send(ctx, func(error) {}, broadcast.Heartbeat(id, true))
	}
}

func (f *fakeSpawn) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSpawn) argv(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.calls) {
		return ""
	}
	return strings.Join(f.calls[i].Argv, " ")
}

func (f *fakeSpawn) prompt(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.calls) {
		return ""
	}
	p, _ := sched.PromptFrom(f.calls[i].Ctx)
	return p
}

type harness struct {
	todoDB  store.DB
	schedDB store.DB
	home    string
	cwd     string
	rigHome string
	spawn   *fakeSpawn
	fetch   *fetchState
	ctl     *swarm.Controller
	fe      *recordFrontend
	engine  evt.Engine
	room    broadcast.Room
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{}
	h.engine = evt.NewEngine()
	go h.engine.Start(context.Background())
	t.Cleanup(h.engine.Stop)
	h.room = broadcast.NewRoom("fleet", func(origin int64) broadcast.Transport {
		return broadcast.NewLoopTransport(origin, h.engine, rig.PriorityFleet)
	})
	todoDB, _, _, err := store.Open(filepath.Join(t.TempDir(), "todo.sqlite"), todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatalf("todo store: %v", err)
	}
	h.todoDB = todoDB
	h.home = t.TempDir()
	schedDB, _, _, err := store.Open(filepath.Join(h.home, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatalf("sched store: %v", err)
	}
	h.schedDB = schedDB
	h.cwd = t.TempDir()
	h.rigHome = t.TempDir()
	h.spawn = &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	h.fetch = &fetchState{}
	h.fe = &recordFrontend{}
	h.listen(h.room)
	h.ctl = swarm.New(swarm.Opts{
		TodoDB:       todoDB,
		SchedDB:      schedDB,
		Home:         h.home,
		Project:      func(ctx context.Context, session string) (todostore.Project, error) { return proj, nil },
		Cwd:          h.cwd,
		WorkerCmd:    []string{"/x/rig"},
		Fetch:        h.fetch.fetch,
		Spawn:        h.spawn.spawn,
		SwapURL:      "http://127.0.0.1:8090",
		Sandbox:      "off",
		RigHome:      h.rigHome,
		StateDir:     t.TempDir(),
		DefaultModel: "qwen3.8-workers",
		Models:       func() models.Table { return modelRows(t) },
		Engine:       h.engine,
		Room:         h.room,
	})
	t.Cleanup(func() { h.ctl.Stop() })
	return h
}

func (h *harness) listen(room broadcast.Room) {
	room.Add(-1).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if err == nil && m.Event() != nil {
				h.fe.Notify(m.Event())
			}
		}
	})
}

func (h *harness) newRoom() broadcast.Room {
	room := broadcast.NewRoom("fleet", func(origin int64) broadcast.Transport {
		return broadcast.NewLoopTransport(origin, h.engine, rig.PriorityFleet)
	})
	h.listen(room)
	return room
}

func (h *harness) create(t *testing.T, texts ...string) {
	t.Helper()
	items := make([]todostore.CreateItem, len(texts))
	for i, text := range texts {
		items[i] = todostore.CreateItem{Text: text}
	}
	if _, err := todostore.Create(context.Background(), h.todoDB, proj, items, "sess-architect"); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func (h *harness) status(t *testing.T, id string) string {
	t.Helper()
	rows, err := h.todoDB.DB.Query(`SELECT status FROM tasks WHERE scope = 'swarm' AND id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ""
	}
	var status string
	if err := rows.Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func (h *harness) start(t *testing.T, in swarm.StartOpts) string {
	t.Helper()
	ctx := core.WithSession(context.Background(), core.NewSession())
	reply, err := h.ctl.Start(ctx, in)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return reply
}

func (h *harness) waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSwarmHandsOutOneTaskPerIdleWorker(t *testing.T) {
	h := newHarness(t)
	h.create(t, "write the parser", "write the tests")
	h.spawn.block = make(chan struct{})
	h.start(t, swarm.StartOpts{Count: 3, Role: "worker"})
	h.waitFor(t, "both tasks handed out", func() bool { return h.spawn.count() == 2 })
	idle := 0
	busy := 0
	for _, r := range h.ctl.List() {
		if r.Task == "" {
			idle++
		} else {
			busy++
		}
	}
	if idle != 1 || busy != 2 {
		t.Fatalf("idle = %d busy = %d, want one of three holding nothing:\n%+v", idle, busy, h.ctl.List())
	}
	time.Sleep(50 * time.Millisecond)
	if got := h.spawn.count(); got != 2 {
		t.Fatalf("spawn calls = %d, want two (the queue holds nothing more for the idle worker)", got)
	}
}

func TestSwarmWorkerFinishingIsTheNextHandout(t *testing.T) {
	h := newHarness(t)
	h.create(t, "first", "second")
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "both tasks ran on the one worker", func() bool { return h.spawn.count() == 2 })
	h.waitFor(t, "the board drained", func() bool {
		return h.status(t, "t1") == "review" && h.status(t, "t2") == "review"
	})
	rows := h.ctl.List()
	if len(rows) != 1 || rows[0].Done != 2 {
		t.Fatalf("worker rows = %+v, want one worker with done 2", rows)
	}
	if rows[0].Task != "" {
		t.Errorf("the finished worker must be idle again, holds %q", rows[0].Task)
	}
}

func TestSwarmQueuedWorkerLivesPastTheOldStallBound(t *testing.T) {
	h := newHarness(t)
	h.create(t, "queued behind the session")
	h.spawn.onCall = func(context.Context) {
		time.Sleep(150 * time.Millisecond)
	}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the silent worker's task in review", func() bool {
		return h.status(t, "t1") == "review"
	})
}

func TestSwarmSpawnCarriesNoStallNoTimeoutAndWaitsOnTheServer(t *testing.T) {
	h := newHarness(t)
	h.create(t, "shaped work")
	captured := make(chan sched.DelegateInput, 1)
	h.ctl = swarm.New(swarm.Opts{
		TodoDB:       h.todoDB,
		SchedDB:      h.schedDB,
		Home:         h.home,
		Project:      func(ctx context.Context, session string) (todostore.Project, error) { return proj, nil },
		Cwd:          h.cwd,
		WorkerCmd:    []string{"/x/rig"},
		Fetch:        h.fetch.fetch,
		Spawn:        h.spawn.spawn,
		SwapURL:      "http://127.0.0.1:8090",
		Sandbox:      "off",
		RigHome:      h.rigHome,
		StateDir:     t.TempDir(),
		DefaultModel: "qwen3.8-workers",
		Models:       func() models.Table { return modelRows(t) },
		Engine:       h.engine,
		Room:         h.newRoom(),
		Delegate: func(in sched.DelegateInput) (sched.DelegateResult, error) {
			captured <- in
			return sched.DelegateResult{Exit: 0, Stdout: "done\n"}, nil
		},
	})
	t.Cleanup(func() { h.ctl.Stop() })
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	in := <-captured
	if in.Timeout >= 0 {
		t.Errorf("timeout = %v, want the caller's context as the only bound (negative)", in.Timeout)
	}
	if in.Member == nil {
		t.Error("the worker's member must ride the input; the pipe publishes as it")
	}
}

func TestSwarmDeadWorkerTaskReleasedAndHandedToAnother(t *testing.T) {
	h := newHarness(t)
	h.create(t, "survive a crash")
	h.spawn.queue = []sched.SpawnResult{
		{Exit: 1, Stderr: "the worker died\n"},
		{Exit: 0, Stdout: "done\n"},
	}
	h.start(t, swarm.StartOpts{Count: 2, Role: "worker"})
	h.waitFor(t, "the dead claim released", func() bool {
		rows, err := h.todoDB.DB.Query(`SELECT op FROM events WHERE scope = 'swarm' AND op = 'release'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		return rows.Next()
	})
	h.waitFor(t, "the retry spawn", func() bool { return h.spawn.count() == 2 })
	if !strings.Contains(h.spawn.prompt(1), "survive a crash") {
		t.Errorf("the retry must carry the same task brief:\n%s", h.spawn.prompt(1))
	}
	h.waitFor(t, "the task submitted for review", func() bool {
		return h.status(t, "t1") == "review"
	})
	rows := h.ctl.List()
	done := 0
	for _, r := range rows {
		done += r.Done
	}
	if done != 1 {
		t.Errorf("done total = %d, want 1 (%+v)", done, rows)
	}
}

func TestSwarmTwoSessionsOnOneQueueNeverRunATaskTwice(t *testing.T) {
	h := newHarness(t)
	h.create(t, "one task, two routers")
	first := h.ctl
	second := swarm.New(swarm.Opts{
		TodoDB:       h.todoDB,
		SchedDB:      h.schedDB,
		Home:         h.home,
		Project:      func(ctx context.Context, session string) (todostore.Project, error) { return proj, nil },
		Cwd:          h.cwd,
		WorkerCmd:    []string{"/x/rig"},
		Fetch:        h.fetch.fetch,
		Spawn:        h.spawn.spawn,
		SwapURL:      "http://127.0.0.1:8090",
		Sandbox:      "off",
		RigHome:      h.rigHome,
		StateDir:     t.TempDir(),
		DefaultModel: "qwen3.8-workers",
		Models:       func() models.Table { return modelRows(t) },
		Engine:       h.engine,
		Room:         h.newRoom(),
	})
	t.Cleanup(func() { second.Stop() })
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := first.Start(ctx, swarm.StartOpts{Count: 1, Role: "worker"}); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if _, err := second.Start(ctx, swarm.StartOpts{Count: 1, Role: "worker"}); err != nil {
		t.Fatalf("second start: %v", err)
	}
	h.waitFor(t, "the task claimed exactly once", func() bool { return h.spawn.count() == 1 })
	time.Sleep(50 * time.Millisecond)
	if got := h.spawn.count(); got != 1 {
		t.Fatalf("spawn calls across both swarms = %d, want 1 (the claim is atomic)", got)
	}
	h.waitFor(t, "the one run submitted for review", func() bool {
		return h.status(t, "t1") == "review"
	})
	time.Sleep(50 * time.Millisecond)
	if got := h.spawn.count(); got != 1 {
		t.Fatalf("spawn calls across both swarms = %d, want 1 (the claim is atomic)", got)
	}
	done := 0
	for _, rows := range [][]swarm.Worker{first.List(), second.List()} {
		for _, r := range rows {
			done += r.Done
		}
	}
	if done != 1 {
		t.Errorf("done total across both swarms = %d, want 1 (one task, one run):\n%+v %+v", done, first.List(), second.List())
	}
}

func TestSwarmCountBounds(t *testing.T) {
	h := newHarness(t)
	ctx := core.WithSession(context.Background(), core.NewSession())
	for _, in := range []swarm.StartOpts{{Count: 0, Role: "worker"}, {Count: -2, Role: "worker"}, {Count: swarm.MaxWorkers + 1, Role: "worker"}} {
		if _, err := h.ctl.Start(ctx, in); err == nil {
			t.Errorf("Start(%+v) must refuse a count outside 1..%d", in, swarm.MaxWorkers)
		}
	}
	if rows := h.ctl.List(); len(rows) != 0 {
		t.Errorf("a refused start must start nothing: %+v", rows)
	}
}

func TestSwarmDrainsAThreeTaskQueueWithTwoWorkers(t *testing.T) {
	h := newHarness(t)
	h.create(t, "write the parser", "write the tests", "write the docs")
	h.start(t, swarm.StartOpts{Count: 2, Role: "worker"})
	h.waitFor(t, "all three tasks in review", func() bool {
		return h.status(t, "t1") == "review" && h.status(t, "t2") == "review" && h.status(t, "t3") == "review"
	})
	if got := h.spawn.count(); got != 3 {
		t.Fatalf("spawn calls = %d, want one per task (3)", got)
	}
	briefs := h.spawn.prompt(0) + h.spawn.prompt(1) + h.spawn.prompt(2)
	for _, text := range []string{"write the parser", "write the tests", "write the docs"} {
		if strings.Count(briefs, text) != 1 {
			t.Errorf("one spawn must carry the task brief %q once:\n%s", text, briefs)
		}
	}
	for i := 0; i < 3; i++ {
		if !strings.Contains(h.spawn.prompt(i), "The supervisor owns this board entry") {
			t.Errorf("spawn %d must tell the worker the supervisor owns the board entry:\n%s", i, h.spawn.prompt(i))
		}
	}
	rows := h.ctl.List()
	done := 0
	for _, r := range rows {
		done += r.Done
	}
	if done != 3 {
		t.Errorf("done total = %d, want 3 (%+v)", done, rows)
	}
	for _, r := range rows {
		if r.Model != "resident" {
			t.Errorf("worker model = %q, want the resident fleet", r.Model)
		}
	}
}

func TestSwarmCompletedRequirementHandsTheDependentOut(t *testing.T) {
	h := newHarness(t)
	req := "t1"
	if _, err := todostore.Create(context.Background(), h.todoDB, proj, []todostore.CreateItem{{Text: "the blocker"}}, "sess-architect"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := todostore.Create(context.Background(), h.todoDB, proj, []todostore.CreateItem{{Text: "the dependent", Requires: &req}}, "sess-architect"); err != nil {
		t.Fatalf("create: %v", err)
	}
	h.spawn.queue = []sched.SpawnResult{
		{Exit: 0, Stdout: "done\n"},
		{Exit: 0, Stdout: "looks good\nverdict: accept\n"},
		{Exit: 0, Stdout: "done\n"},
	}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.start(t, swarm.StartOpts{Count: 1, Role: "reviewer"})
	h.waitFor(t, "only the blocker claimed while it blocked", func() bool {
		return h.spawn.count() == 1 && h.status(t, "t1") == "review"
	})
	h.waitFor(t, "the dependent handed out on the completion", func() bool {
		return h.spawn.count() == 3 && h.status(t, "t1") == "done" && h.status(t, "t2") == "review"
	})
}

func TestSwarmSpawnsWhileEverySlotIsProcessing(t *testing.T) {
	h := newHarness(t)
	h.create(t, "queued on the server")
	h.fetch.resident = []string{"qwen3.8-27b"}
	h.fetch.slots = [][]bool{{true, true}}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the spawn against a full slot set", func() bool { return h.spawn.count() == 1 })
	if got := h.spawn.argv(0); !strings.Contains(got, "-model qwen3.8-27b") {
		t.Errorf("the worker must run the resident model: %s", got)
	}
	h.waitFor(t, "the task in review", func() bool { return h.status(t, "t1") == "review" })
}

func TestSwarmReviewerRejectsAndAWorkerPicksItUp(t *testing.T) {
	h := newHarness(t)
	h.create(t, "ship the feature")
	h.spawn.queue = []sched.SpawnResult{
		{Exit: 0, Stdout: "done\n"},
		{Exit: 0, Stdout: "reviewed it\nverdict: reject tests are missing\n"},
		{Exit: 0, Stdout: "done\n"},
		{Exit: 0, Stdout: "looks good now\nverdict: accept\n"},
	}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.start(t, swarm.StartOpts{Count: 1, Role: "reviewer"})
	h.waitFor(t, "the rejection picked up and accepted", func() bool {
		return h.status(t, "t1") == "done"
	})
	notes, err := todostore.Notes(context.Background(), h.todoDB, proj, "t1", "sess-architect")
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	if !strings.Contains(notes, "tests are missing") {
		t.Errorf("the rejection reason must be a note:\n%s", notes)
	}
	rows := h.ctl.List()
	var workerDone, reviewerDone int
	for _, r := range rows {
		if r.Role == "reviewer" {
			reviewerDone = r.Done
		} else {
			workerDone = r.Done
		}
	}
	if workerDone != 2 {
		t.Errorf("worker done = %d, want 2 (work + the rejected pick-up)", workerDone)
	}
	if reviewerDone != 2 {
		t.Errorf("reviewer done = %d, want 2 (reject + accept)", reviewerDone)
	}
}

func TestSwarmSecondDeathFailsTheTask(t *testing.T) {
	h := newHarness(t)
	h.create(t, "doomed")
	h.spawn.result = sched.SpawnResult{Exit: 1, Stderr: "dead\n"}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the task failed", func() bool { return h.status(t, "t1") == "failed" })
	if got := h.spawn.count(); got != 2 {
		t.Fatalf("spawn calls = %d, want the first try plus one restart (2)", got)
	}
	rows := h.ctl.List()
	if len(rows) != 1 || rows[0].Failed != 1 {
		t.Fatalf("worker rows = %+v, want failed 1", rows)
	}
}

func TestSwarmReviewerSecondDeathRejectsWithTheReason(t *testing.T) {
	h := newHarness(t)
	h.create(t, "in review")
	if _, err := todostore.Claim(context.Background(), h.todoDB, proj, "sess-architect", ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(context.Background(), h.todoDB, proj, "t1", "sess-architect", true); err != nil {
		t.Fatalf("complete: %v", err)
	}
	h.spawn.result = sched.SpawnResult{Exit: 1, Stderr: "reviewer died\n"}
	h.start(t, swarm.StartOpts{Count: 1, Role: "reviewer"})
	h.waitFor(t, "the review rejected after the restart", func() bool {
		return h.status(t, "t1") == "pending"
	})
	notes, err := todostore.Notes(context.Background(), h.todoDB, proj, "t1", "sess-architect")
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	if !strings.Contains(notes, "reviewer died twice") {
		t.Errorf("the second death must reject with the reason as a note:\n%s", notes)
	}
}

func TestSwarmWorkerRunsTheResidentModel(t *testing.T) {
	h := newHarness(t)
	h.create(t, "wait for the gpu")
	h.fetch.resident = []string{"qwen3.8-27b"}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the spawn", func() bool { return h.spawn.count() == 1 })
	if got := h.spawn.argv(0); !strings.Contains(got, "-model qwen3.8-27b") {
		t.Errorf("the worker must run the resident model: %s", got)
	}
	h.waitFor(t, "the task in review", func() bool { return h.status(t, "t1") == "review" })
}

func TestSwarmListsWorkersAndStops(t *testing.T) {
	h := newHarness(t)
	h.create(t, "supervised")
	block := make(chan struct{})
	h.spawn.block = block
	h.spawn.onCall = func(ctx context.Context) {
		beat(ctx, 1)
	}
	h.fetch.resident = []string{"qwen3.8-review"}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker", Model: "qwen3.8-review"})
	var busy swarm.Worker
	h.waitFor(t, "a worker holding the task with a heartbeat", func() bool {
		for _, r := range h.ctl.List() {
			if r.Task == "t1" && !r.Heartbeat.IsZero() {
				busy = r
				return true
			}
		}
		return false
	})
	if busy.Role != "worker" || busy.Model != "qwen3.8-review" || busy.State != "running" {
		t.Errorf("worker row = %+v", busy)
	}
	if time.Since(busy.Heartbeat) > 5*time.Second {
		t.Errorf("heartbeat = %v, want the run stream's recent beat", busy.Heartbeat)
	}
	if got, err := h.ctl.Stop(); err != nil || !strings.Contains(got, "stopped") {
		t.Errorf("stop reply = %q err=%v", got, err)
	}
	if rows := h.ctl.List(); len(rows) != 0 {
		t.Errorf("stop must clear the rows: %+v", rows)
	}
	if got := h.status(t, "t1"); got != "pending" {
		t.Errorf("a stopped swarm must release its claim: status %q, want pending", got)
	}
	if _, err := os.Stat(filepath.Join(h.home, "swarm")); !os.IsNotExist(err) {
		t.Errorf("the worker's bytes are the run log's; no stream file is written beside it (%v)", err)
	}
}

func TestSwarmModelDefaultsToResidentAndOverrideWins(t *testing.T) {
	h := newHarness(t)
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.start(t, swarm.StartOpts{Count: 1, Role: "reviewer"})
	rows := h.ctl.List()
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	for _, r := range rows {
		if r.Model != "resident" {
			t.Errorf("model = %q, want the resident fleet (the roster shows it)", r.Model)
		}
	}
	h2 := newHarness(t)
	h2.start(t, swarm.StartOpts{Count: 1, Role: "reviewer", Model: "qwen3.8-workers"})
	if got := h2.ctl.List()[0].Model; got != "qwen3.8-workers" {
		t.Errorf("override model = %q, want qwen3.8-workers", got)
	}
}

func TestSwarmStartRefusalsByName(t *testing.T) {
	h := newHarness(t)
	ctx := core.WithSession(context.Background(), core.NewSession())
	for _, in := range []swarm.StartOpts{
		{Count: 1, Role: "boss"}, {Count: 1, Model: "nope"},
	} {
		if _, err := h.ctl.Start(ctx, in); err == nil {
			t.Errorf("Start(%+v) must refuse", in)
		}
	}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	reply := h.start(t, swarm.StartOpts{Count: 1, Role: "reviewer"})
	if !strings.Contains(reply, "added") {
		t.Errorf("a start against a running swarm must add workers: %q", reply)
	}
	if rows := h.ctl.List(); len(rows) != 2 {
		t.Errorf("workers = %d, want 2 (the added reviewer)", len(rows))
	}
}

func TestSwarmUnknownModelNamesTheKnown(t *testing.T) {
	h := newHarness(t)
	ctx := core.WithSession(context.Background(), core.NewSession())
	_, err := h.ctl.Start(ctx, swarm.StartOpts{Count: 1, Model: "nope"})
	if err == nil {
		t.Fatal("an unknown model must refuse")
	}
	if !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "qwen3.8-workers") {
		t.Errorf("unknown-model voice: %v", err)
	}
}

func TestSwarmTaskWorkerRecordLandsInTheSchedulerStore(t *testing.T) {
	h := newHarness(t)
	h.create(t, "recorded work")
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the task in review", func() bool { return h.status(t, "t1") == "review" })
	rows, err := h.schedDB.DB.Query(`SELECT name FROM jobs WHERE name LIKE 'delegate:%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("the task worker's run must be recorded as a delegate run")
	}
	var name string
	if err := rows.Scan(&name); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "delegate:") {
		t.Errorf("job name = %q", name)
	}
}

func TestSwarmReleasesClaimsOnStopWithAReviewHeld(t *testing.T) {
	h := newHarness(t)
	h.create(t, "stop me")
	h.spawn.block = make(chan struct{})
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the spawn running", func() bool { return h.spawn.count() == 1 })
	h.ctl.Stop()
	if got := h.status(t, "t1"); got != "pending" {
		t.Errorf("status = %q, want pending after stop", got)
	}
}

func TestSwarmEmptyQueueStopRepliesNoSwarm(t *testing.T) {
	h := newHarness(t)
	if got, err := h.ctl.Stop(); err == nil || !strings.Contains(err.Error(), "no swarm") {
		t.Errorf("stop with no swarm = %q err=%v", got, err)
	}
}

func TestSwarmReviewerNoVerdictCappedAtTwoRejectsThenFails(t *testing.T) {
	h := newHarness(t)
	h.create(t, "hopeless")
	h.spawn.queue = []sched.SpawnResult{
		{Exit: 0, Stdout: "done\n"},
		{Exit: 0},
		{Exit: 0},
		{Exit: 0, Stdout: "done\n"},
		{Exit: 0},
		{Exit: 0, Stdout: "done\n"},
		{Exit: 0},
	}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.start(t, swarm.StartOpts{Count: 1, Role: "reviewer"})
	h.waitFor(t, "the task failed after the reject cap", func() bool {
		return h.status(t, "t1") == "failed"
	})
	if got := h.spawn.count(); got != 7 {
		t.Fatalf("spawn calls = %d, want 7 (three work rounds plus four review rounds)", got)
	}
	notes, err := todostore.Notes(context.Background(), h.todoDB, proj, "t1", "sess-architect")
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	if !strings.Contains(notes, "rejected this twice") {
		t.Errorf("the capped fail must carry the note:\n%s", notes)
	}
}

func TestSwarmRetriesAreKeyedByTaskAcrossWorkers(t *testing.T) {
	h := newHarness(t)
	h.create(t, "shared retry budget")
	h.spawn.queue = []sched.SpawnResult{
		{Exit: 1, Stderr: "worker died\n"},
		{Exit: 0, Stdout: "done\n"},
		{Exit: 1, Stderr: "reviewer died\n"},
		{Exit: 0, Stdout: "done\n"},
		{Exit: 1, Stderr: "reviewer died\n"},
		{Exit: 0, Stdout: "done\n"},
		{Exit: 1, Stderr: "reviewer died\n"},
	}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.start(t, swarm.StartOpts{Count: 1, Role: "reviewer"})
	h.waitFor(t, "the task failed from the shared retry budget", func() bool {
		return h.status(t, "t1") == "failed"
	})
	if got := h.spawn.count(); got != 7 {
		t.Fatalf("spawn calls = %d, want 7 (the worker's death consumed the one retry the reviewer would have had)", got)
	}
	notes, err := todostore.Notes(context.Background(), h.todoDB, proj, "t1", "sess-architect")
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	if !strings.Contains(notes, "rejected this twice") {
		t.Errorf("the capped fail must carry the note:\n%s", notes)
	}
}

func TestSwarmStartAndStopReplyVoice(t *testing.T) {
	h := newHarness(t)
	reply := h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	want := "swarm: added 1 agent (role worker · model resident)"
	if reply != want {
		t.Errorf("start reply = %q, want %q", reply, want)
	}
	reply = h.start(t, swarm.StartOpts{Count: 2, Role: "reviewer"})
	want = "swarm: added 2 agents (role reviewer · model resident)"
	if reply != want {
		t.Errorf("a start against a running swarm replies %q, want %q", reply, want)
	}
	if strings.Contains(reply, "started") {
		t.Errorf("the start reply must never say started: %q", reply)
	}
	got, err := h.ctl.Stop()
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got != "swarm: stopped 3 agents" {
		t.Errorf("stop reply = %q, want stopped 3 agents", got)
	}
}

func TestHolderRefusalReleasesAndStopsTheWorker(t *testing.T) {
	h := newHarness(t)
	h.create(t, "held out")
	h.fetch.resident = []string{"glm5.3-flash"}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker", Model: "dsv4"})
	got := waitForNotices(t, h.fe, 2)
	if !strings.Contains(got[0], "held by glm5.3-flash") {
		t.Fatalf("the refusal must name the holder: %q", got[0])
	}
	if !strings.Contains(got[1], "w1 stopped") || !strings.Contains(got[1], "held by glm5.3-flash") {
		t.Fatalf("the stop notice must name the holder: %q", got[1])
	}
	h.waitFor(t, "the release", func() bool { return h.status(t, "t1") == "pending" })
	rows := h.ctl.List()
	if len(rows) != 1 || rows[0].State != swarm.StateExited {
		t.Fatalf("worker rows = %+v, want the worker stopped", rows)
	}
	if got := h.spawn.count(); got != 0 {
		t.Fatalf("spawn calls = %d, want the refusal to precede the spawn", got)
	}
}

func TestStoppedWorkerIsOutBeforeTheRouterWakes(t *testing.T) {
	h := newHarness(t)
	h.create(t, "held out", "held longer")
	h.fetch.resident = []string{"glm5.3-flash"}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker", Model: "dsv4"})
	got := waitForNotices(t, h.fe, 1)
	if !strings.Contains(got[0], "held by glm5.3-flash") {
		t.Fatalf("the stop notice must name the holder: %q", got[0])
	}
	h.waitFor(t, "both tasks pending", func() bool {
		return h.status(t, "t1") == "pending" && h.status(t, "t2") == "pending"
	})
	var t2Claims int
	if err := h.todoDB.DB.QueryRow(`SELECT count(*) FROM events WHERE op = 'claim' AND args LIKE '%t2%'`).Scan(&t2Claims); err != nil {
		t.Fatal(err)
	}
	if t2Claims != 0 {
		t.Fatalf("t2 claimed %d times: the worker must be out before the router wakes", t2Claims)
	}
	if got := h.spawn.count(); got != 0 {
		t.Fatalf("spawn calls = %d, want never", got)
	}
	rows := h.ctl.List()
	if len(rows) != 1 || rows[0].State != swarm.StateExited {
		t.Fatalf("worker rows = %+v, want the worker stopped", rows)
	}
}
