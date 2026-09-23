package swarm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
	"github.com/mrsirg97-rgb/rig/swarm"
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
		models.Model{ID: "qwen3.8-workers", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker},
		models.Model{ID: "qwen3.8-review", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker},
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
	mu      sync.Mutex
	busy    int
	failing string
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
		return json.RawMessage(runningJSON()), nil
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
	onCall func(observe func([]byte))
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
		f.onCall(observe)
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
		}
	}
	return result, f.err
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

type harness struct {
	todoDB  store.DB
	schedDB store.DB
	home    string
	cwd     string
	spawn   *fakeSpawn
	fetch   *fetchState
	ctl     *swarm.Controller
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{}
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
	h.spawn = &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	h.fetch = &fetchState{}
	h.ctl = swarm.New(swarm.Opts{
		TodoDB:        todoDB,
		SchedDB:       schedDB,
		Home:          h.home,
		Project:       func(ctx context.Context, session string) (todostore.Project, error) { return proj, nil },
		Cwd:           h.cwd,
		WorkerCmd:     []string{"/x/rig"},
		Fetch:         h.fetch.fetch,
		Spawn:         h.spawn.spawn,
		SwapURL:       "http://127.0.0.1:8090",
		Sandbox:       "off",
		RigHome:       t.TempDir(),
		StateDir:      t.TempDir(),
		FleetModel:    "qwen3.8-workers",
		ReviewerModel: "qwen3.8-review",
		Models:        func() models.Table { return modelRows(t) },
		Poll:          20 * time.Millisecond,
	})
	t.Cleanup(func() { h.ctl.Stop() })
	return h
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
	for i, text := range []string{"write the parser", "write the tests", "write the docs"} {
		if !strings.Contains(h.spawn.argv(i), text) {
			t.Errorf("spawn %d must carry the task brief %q:\n%s", i, text, h.spawn.argv(i))
		}
		if !strings.Contains(h.spawn.argv(i), "The supervisor owns this board entry") {
			t.Errorf("spawn %d must tell the worker the supervisor owns the board entry:\n%s", i, h.spawn.argv(i))
		}
	}
	h.waitFor(t, "both workers exited", func() bool {
		rows := h.ctl.List()
		return len(rows) == 2 && rows[0].State == "exited" && rows[1].State == "exited"
	})
	rows := h.ctl.List()
	if rows[0].Done+rows[1].Done != 3 {
		t.Errorf("done total = %d, want 3 (%+v)", rows[0].Done+rows[1].Done, rows)
	}
	for _, r := range rows {
		if r.Model != "qwen3.8-workers" {
			t.Errorf("worker model = %q, want the fleet's", r.Model)
		}
	}
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
	read, err := todostore.ReadAll(context.Background(), h.todoDB, proj, "sess-architect")
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !strings.Contains(read, "tests are missing") {
		t.Errorf("the rejection reason must be a note:\n%s", read)
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

func TestSwarmDeadWorkerClaimReleasedAndRestartedOnce(t *testing.T) {
	h := newHarness(t)
	h.create(t, "survive a crash")
	h.spawn.queue = []sched.SpawnResult{
		{Exit: 1, Stderr: "the worker died\n"},
		{Exit: 0, Stdout: "done\n"},
	}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the first spawn", func() bool { return h.spawn.count() == 1 })
	h.waitFor(t, "the dead claim released", func() bool {
		rows, err := h.todoDB.DB.Query(`SELECT op FROM events WHERE scope = 'swarm' AND op = 'release'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		return rows.Next()
	})
	h.waitFor(t, "the retry spawn", func() bool { return h.spawn.count() == 2 })
	if !strings.Contains(h.spawn.argv(1), "survive a crash") {
		t.Errorf("the retry must carry the same task brief:\n%s", h.spawn.argv(1))
	}
	h.waitFor(t, "the task submitted for review", func() bool {
		return h.status(t, "t1") == "review"
	})
	h.waitFor(t, "the worker exited", func() bool {
		rows := h.ctl.List()
		return len(rows) == 1 && rows[0].State == "exited"
	})
	rows := h.ctl.List()
	if rows[0].Done != 1 || rows[0].Failed != 0 {
		t.Errorf("worker counters = done %d failed %d, want 1/0", rows[0].Done, rows[0].Failed)
	}
	releaseRows, err := h.todoDB.DB.Query(`SELECT op FROM events WHERE scope = 'swarm' AND op = 'release'`)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRows.Close()
	if !releaseRows.Next() {
		t.Fatal("the dead worker's claim must be released (a release event on the log)")
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
	read, err := todostore.Read(context.Background(), h.todoDB, proj, "sess-architect")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "reviewer died twice") {
		t.Errorf("the second death must reject with the reason as a note:\n%s", read)
	}
}

func TestSwarmExitsAfterThreeEmptyClaims(t *testing.T) {
	h := newHarness(t)
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the worker exited", func() bool {
		rows := h.ctl.List()
		return len(rows) == 1 && rows[0].State == "exited"
	})
	if got := h.spawn.count(); got != 0 {
		t.Fatalf("an empty queue must spawn nothing, got %d spawns", got)
	}
	rows := h.ctl.List()
	if rows[0].Done != 0 || rows[0].Failed != 0 {
		t.Errorf("empty-worker counters = %+v", rows[0])
	}
}

func TestSwarmBusyWaitsAtLlamaSwap(t *testing.T) {
	h := newHarness(t)
	h.create(t, "wait for the gpu")
	h.fetch.busy = 2
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the spawn after the gpu freed", func() bool { return h.spawn.count() == 1 })
	h.waitFor(t, "the task in review", func() bool { return h.status(t, "t1") == "review" })
}

func TestSwarmListsWorkersAndStops(t *testing.T) {
	h := newHarness(t)
	h.create(t, "supervised")
	block := make(chan struct{})
	h.spawn.block = block
	h.spawn.onCall = func(observe func([]byte)) {
		observe([]byte("rig: heartbeat\n"))
	}
	h.start(t, swarm.StartOpts{Count: 2, Role: "worker", Model: "qwen3.8-review"})
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
	stream, err := os.ReadFile(filepath.Join(h.home, "swarm", fmt.Sprintf("w%d.stream", busy.ID)))
	if err != nil {
		t.Fatalf("the run stream: %v", err)
	}
	if !strings.Contains(string(stream), "rig: heartbeat") {
		t.Errorf("the run stream must carry the worker's heartbeat:\n%s", stream)
	}
}

func TestSwarmReviewerModelDefaultAndOverride(t *testing.T) {
	h := newHarness(t)
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.start(t, swarm.StartOpts{Count: 1, Role: "reviewer"})
	rows := h.ctl.List()
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	for _, r := range rows {
		if r.Role == "reviewer" && r.Model != "qwen3.8-review" {
			t.Errorf("reviewer model = %q, want the configured reviewer model", r.Model)
		}
		if r.Role == "worker" && r.Model != "qwen3.8-workers" {
			t.Errorf("worker model = %q, want the fleet model", r.Model)
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
		{Count: 0}, {Count: 17}, {Count: 1, Role: "boss"}, {Count: 1, Model: "nope"},
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
	read, err := todostore.ReadAll(context.Background(), h.todoDB, proj, "sess-architect")
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !strings.Contains(read, "rejected this twice") {
		t.Errorf("the capped fail must carry the note:\n%s", read)
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
	read, err := todostore.ReadAll(context.Background(), h.todoDB, proj, "sess-architect")
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !strings.Contains(read, "rejected this twice") {
		t.Errorf("the capped fail must carry the note:\n%s", read)
	}
}
