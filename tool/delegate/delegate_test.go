package delegate_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/models"

	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	"github.com/mrsirg97-rgb/rig/v2/tool/delegate"
)

func modelsJSON(statuses map[string]string) string {
	type status struct {
		Value string `json:"value"`
	}
	type model struct {
		ID     string `json:"id"`
		Status status `json:"status"`
	}
	var data []model
	for _, id := range []string{"qwen3.8-workers"} {
		st := "unloaded"
		if statuses != nil {
			st = statuses[id]
		}
		data = append(data, model{ID: id, Status: status{Value: st}})
	}
	b, _ := json.Marshal(map[string]any{"data": data})
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

func fakeFetch(resident string) func(url string) (json.RawMessage, error) {
	return fakeFetchSlots(resident)
}

func fakeFetchSlots(resident string, slots ...[]bool) func(url string) (json.RawMessage, error) {
	var mu sync.Mutex
	reads := 0
	return func(url string) (json.RawMessage, error) {
		switch {
		case strings.HasSuffix(url, "/v1/models"):
			return json.RawMessage(modelsJSON(nil)), nil
		case strings.HasSuffix(url, "/running"):
			if resident == "" {
				return json.RawMessage(runningJSON()), nil
			}
			return json.RawMessage(runningJSON(resident)), nil
		case strings.Contains(url, "/upstream/"):
			mu.Lock()
			defer mu.Unlock()
			served := []bool{false}
			if len(slots) > 0 {
				idx := reads
				if idx > len(slots)-1 {
					idx = len(slots) - 1
				}
				served = slots[idx]
				reads++
			}
			type slot struct {
				ID           int  `json:"id"`
				IsProcessing bool `json:"is_processing"`
			}
			var out []slot
			for i, p := range served {
				out = append(out, slot{ID: i, IsProcessing: p})
			}
			b, _ := json.Marshal(out)
			return json.RawMessage(b), nil
		}
		return nil, jsonError("unexpected url " + url)
	}
}

type jsonErr string

func (e jsonErr) Error() string { return string(e) }

func jsonError(s string) error { return jsonErr(s) }

type fakeSpawn struct {
	mu          sync.Mutex
	calls       []fakeCall
	result      sched.SpawnResult
	err         error
	record      func()
	block       <-chan struct{}
	deadlineSet bool
}

type fakeCall struct {
	Argv    []string
	Env     []string
	Cwd     string
	Ctx     context.Context
	Started time.Time
	Ended   time.Time
}

func (f *fakeSpawn) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSpawn) hadDeadline() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deadlineSet
}

func (f *fakeSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
	started := time.Now()
	f.mu.Lock()
	idx := len(f.calls)
	f.calls = append(f.calls, fakeCall{Argv: argv, Env: env, Cwd: cwd, Ctx: ctx, Started: started})
	if _, ok := ctx.Deadline(); ok {
		f.deadlineSet = true
	}
	f.mu.Unlock()
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	f.calls[idx].Ended = time.Now()
	f.mu.Unlock()
	if f.record != nil {
		f.record()
	}
	return f.result, f.err
}

func callsByStart(calls []fakeCall) []fakeCall {
	sort.Slice(calls, func(i, j int) bool { return calls[i].Started.Before(calls[j].Started) })
	return calls
}

func assertOverlap(t *testing.T, calls []fakeCall) {
	t.Helper()
	calls = callsByStart(calls)
	for i := 1; i < len(calls); i++ {
		if !calls[i].Started.Before(calls[i-1].Ended) {
			t.Fatalf("spawns %d and %d must overlap (started %v, ended %v)", i-1, i, calls[i-1].Started, calls[i].Started)
		}
	}
}

func assertSequential(t *testing.T, calls []fakeCall) {
	t.Helper()
	calls = callsByStart(calls)
	for i := 1; i < len(calls); i++ {
		if calls[i].Started.Before(calls[i-1].Ended) {
			t.Fatalf("spawn %d must not overlap spawn %d (started %v, previous ended %v)", i, i-1, calls[i].Started, calls[i-1].Ended)
		}
	}
}

type harness struct {
	home    string
	rigHome string
	db      sched.DB
}

func newHarness(t *testing.T, sessionCwd string) *harness {
	t.Helper()
	home := t.TempDir()
	rigHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rigHome, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, _, _, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatalf("open scheduler store: %v", err)
	}
	return &harness{home: home, rigHome: rigHome, db: db}
}

func (h *harness) newTool(t *testing.T, fetch sched.Fetch, spawn sched.Spawn) delegate.Delegate {
	t.Helper()
	return delegate.New(delegate.Opts{
		DB:           h.db,
		Home:         h.home,
		RigHome:      h.rigHome,
		StateDir:     filepath.Join(h.rigHome, "sessions"),
		SwapURL:      "http://127.0.0.1:8090",
		WorkerCmd:    []string{"/x/rig"},
		DefaultModel: "qwen3.8-workers",
		Sandbox:      "off",
		Fetch:        fetch,
		Spawn:        spawn,
		Models:       modelTable(t),
	})
}

func modelTable(t *testing.T) func() models.Table {
	t.Helper()
	tbl, err := models.New(
		models.Model{ID: "other-model", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker},
		models.Model{ID: "qwen3.8-workers", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker},
	)
	if err != nil {
		t.Fatal(err)
	}
	return func() models.Table { return tbl }
}

func seedSession(t *testing.T, rigHome, cwd string) string {
	t.Helper()
	db, _, _, err := store.Open(state.StorePath(rigHome, cwd), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	defer db.DB.Close()
	id := "sess-delegate"
	if err := state.RecordSession(context.Background(), db, id, cwd, "qwen3.8-workers", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	return id
}

func runArgs(task string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"task": task})
	return b
}

func runArgsModel(model string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"task": "t", "model": model})
	return b
}

func TestDelegateHappyPathFeedsBackAndRecords(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	wd, _ := os.Getwd()
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "the answer"}}

	spawn.record = func() { seedSession(t, h.rigHome, wd) }
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	out, err := tool.Exec(context.Background(), runArgs("do the sweep"))
	if err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if !strings.Contains(out, "the answer") {
		t.Fatalf("the last message must be fed back:\n%s", out)
	}
	if !strings.Contains(out, "delegate: exit 0 · ") || !strings.Contains(out, "· session ") || !strings.Contains(out, " · log runs/") {
		t.Fatalf("the trailer must carry exit, duration, session id, log path:\n%s", out)
	}
	c := spawn.calls[0]
	if c.Cwd != wd || len(c.Argv) < 5 || c.Argv[1] != "-p" {
		t.Fatalf("spawn argv/cwd: %v %v", c.Argv, c.Cwd)
	}
	if c.Argv[2] != "-" {
		t.Fatalf("the prompt must not ride argv: %v", c.Argv)
	}
	if prompt, _ := sched.PromptFrom(c.Ctx); !strings.Contains(prompt, "do the sweep") {
		t.Fatalf("the prompt on the spawn context must carry the task: %q", prompt)
	}
	foundSession := false
	for i := range c.Argv[:len(c.Argv)-1] {
		if c.Argv[i] == "-session-id" && c.Argv[i+1] != "" && strings.Contains(out, "· session "+c.Argv[i+1]+" ·") {
			foundSession = true
		}
	}
	if !foundSession {
		t.Fatalf("the worker argv and trailer must carry the same explicit session: %v\n%s", c.Argv, out)
	}

	var name, cron, state string
	if err := h.db.DB.QueryRow(`SELECT name, cron, state FROM jobs`).Scan(&name, &cron, &state); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "delegate:") || cron != "once" || state != "done" {
		t.Fatalf("a fired ad-hoc job is a consumed once row: name=%q cron=%q state=%q", name, cron, state)
	}
	var logPath string
	if err := h.db.DB.QueryRow(`SELECT log_path FROM runs`).Scan(&logPath); err != nil {
		t.Fatal(err)
	}
	if logPath == "" || !strings.HasPrefix(logPath, "runs/") {
		t.Fatalf("run log path = %q", logPath)
	}
	if _, err := os.Stat(filepath.Join(h.home, filepath.FromSlash(logPath))); err != nil {
		t.Fatalf("log file missing: %v", err)
	}
}

func TestDelegateDescriptionAndSchemaSpeakWorkspace(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	d := tool.Description()
	for _, want := range []string{
		"the workspace must be under the session's workspace or the rig home",
		"a model that is not resident refuses, naming the holder",
	} {
		if !strings.Contains(d, want) {
			t.Fatalf("description missing %q:\n%s", want, d)
		}
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	if got := schema.Properties["workspace"].Description; got != "where the worker runs; must be under the session's workspace or the rig home" {
		t.Fatalf("cwd description %q", got)
	}
}

func TestDelegateCwdRefusalNamesThePath(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	b, _ := json.Marshal(map[string]any{"task": "t", "workspace": "/elsewhere"})
	out, err := tool.Exec(context.Background(), b)
	if err == nil || !strings.Contains(err.Error(), "outside the session's workspace") {
		t.Fatalf("the cwd refusal must name the path: (%q, %v)", out, err)
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn on a refused cwd")
	}
}

func TestDelegateCwdSymlinkEscapeRefuses(t *testing.T) {
	h := newHarness(t, "")
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	b, _ := json.Marshal(map[string]any{"task": "t", "workspace": link})
	_, err = tool.Exec(context.Background(), b)
	if err == nil || !strings.Contains(err.Error(), "outside the session's workspace") {
		t.Fatalf("the resolved symlink escape must refuse: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatal("no spawn on a symlink escape")
	}
}

func TestDelegateCwdFileRefuses(t *testing.T) {
	h := newHarness(t, "")
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	b, _ := json.Marshal(map[string]any{"task": "t", "workspace": file})
	_, err = tool.Exec(context.Background(), b)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("a file cwd must refuse naming the directory rule: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatal("no spawn on a file cwd")
	}
}

func TestDelegateUnnamedModelRunsTheResidentModel(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}}
	tool := h.newTool(t, fakeFetch("other-model"), spawn.spawn)
	if _, err := tool.Exec(context.Background(), runArgs("t")); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	if got := strings.Join(spawn.calls[0].Argv, " "); !strings.Contains(got, "-model other-model") {
		t.Fatalf("the unnamed worker must run the resident model: %s", got)
	}
}

func TestDelegateNamedModelWhileAnotherResidentRefuses(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{}
	tool := h.newTool(t, fakeFetch("other-model"), spawn.spawn)
	out, err := tool.Exec(context.Background(), runArgsModel("brain"))
	if err == nil || !strings.Contains(err.Error(), "held by other-model") {
		t.Fatalf("the busy refusal must name the holder: (%q, %v)", out, err)
	}
	if !strings.Contains(err.Error(), "once-job") {
		t.Fatalf("the busy refusal must teach the escape hatch: %v", err)
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn on a busy GPU")
	}
}

func TestDelegateFanOutOnTwoFreeSlotsSpawnsBoth(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	block := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}, block: block}
	tool := h.newTool(t, fakeFetchSlots("qwen3.8-workers", []bool{false, false}), spawn.spawn)
	done := make(chan error, 2)
	for _, task := range []string{"t1", "t2"} {
		go func(task string) {
			_, err := tool.Exec(context.Background(), runArgs(task))
			done <- err
		}(task)
	}

	deadline := time.Now().Add(2 * time.Second)
	for spawn.count() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("both delegates never started (count %d)", spawn.count())
		}
		time.Sleep(time.Millisecond)
	}
	close(block)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("call %d must succeed: %v", i, err)
		}
	}
	if spawn.count() != 2 {
		t.Fatalf("two spawns, got %d", spawn.count())
	}
	assertOverlap(t, spawn.calls)
}

func TestDelegateSecondFanOutOnASingleSlotSendsAndWaits(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	block := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}, block: block}
	tool := h.newTool(t, fakeFetchSlots("qwen3.8-workers", []bool{false}, []bool{true}), spawn.spawn)
	first := make(chan struct{}, 1)
	go func() {
		_, _ = tool.Exec(context.Background(), runArgs("t1"))
		first <- struct{}{}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for spawn.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first delegate never spawned")
		}
		time.Sleep(time.Millisecond)
	}
	second := make(chan struct{}, 1)
	go func() {
		_, _ = tool.Exec(context.Background(), runArgs("t2"))
		second <- struct{}{}
	}()
	deadline = time.Now().Add(2 * time.Second)
	for spawn.count() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the second delegate must send and wait on the server's queue, spawns = %d", spawn.count())
		}
		time.Sleep(time.Millisecond)
	}
	close(block)
	<-first
	<-second
}

func TestDelegateNoRecursionRefuses(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	t.Setenv(sched.DelegateEnv, "1")
	out, err := tool.Exec(context.Background(), runArgs("t"))
	if err == nil || !strings.Contains(err.Error(), "no recursion") {
		t.Fatalf("the marker must refuse by name: (%q, %v)", out, err)
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn on a refused recursion")
	}
}

func TestDelegateOffProfileHandsTheChildTheRecursionMark(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	if _, err := tool.Exec(context.Background(), runArgs("do the sweep")); err != nil {
		t.Fatalf("off-profile delegate: %v", err)
	}
	env := spawn.calls[0].Env
	if env == nil {
		t.Fatal("the child env must be explicit at the spawn, not nil-inherit")
	}
	found := false
	for _, e := range env {
		if e == sched.DelegateEnv+"=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the child env must carry %s=1: %v", sched.DelegateEnv, env)
	}
}

func TestDelegateLeavesTheProcessEnvAlone(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	var seen string
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	wrapped := func(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
		seen = os.Getenv(sched.DelegateEnv)
		return spawn.spawn(ctx, argv, cwd, env, observe)
	}
	tool := h.newTool(t, fakeFetch(""), wrapped)
	if _, err := tool.Exec(context.Background(), runArgs("do the sweep")); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if seen != "" {
		t.Fatalf("the runner's own env must stay clean during the spawn: %s=%q", sched.DelegateEnv, seen)
	}
	if os.Getenv(sched.DelegateEnv) != "" {
		t.Fatalf("the runner's env must stay clean after the run: %s=%q", sched.DelegateEnv, os.Getenv(sched.DelegateEnv))
	}
}

func TestDelegateCapsOutputWithTheSize(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	wd, _ := os.Getwd()
	big := strings.Repeat("x", 256*1024+100)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: big}}
	spawn.record = func() { seedSession(t, h.rigHome, wd) }
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	out, err := tool.Exec(context.Background(), runArgs("t"))
	if err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if !strings.Contains(out, "[TRUNCATED: ") || !strings.Contains(out, " bytes total]") {
		t.Fatalf("the cap marker must name the full size:\n%s", out[:60])
	}
}

func TestDelegateRunAndExecShareTheirChecks(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "the answer"}}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	ctx := context.Background()

	_, runErr := tool.Run(ctx, "   ", "", "")
	_, execErr := tool.Exec(ctx, runArgs("   "))
	if runErr == nil || execErr == nil {
		t.Fatalf("a blank task refuses on both doors, run=%v exec=%v", runErr, execErr)
	}
	if runErr.Error() != "delegate: task is required" || execErr.Error() != runErr.Error() {
		t.Fatalf("the refusal is the same words on either door: run %q exec %q", runErr, execErr)
	}

	outside := filepath.Join(t.TempDir(), "elsewhere")
	_, runErr = tool.Run(ctx, "do the sweep", outside, "")
	_, execErr = tool.Exec(ctx, json.RawMessage(`{"task":"do the sweep","workspace":"`+outside+`"}`))
	if runErr == nil || execErr == nil {
		t.Fatalf("a workspace outside the session's cwd and the rig home refuses on both doors, run=%v exec=%v", runErr, execErr)
	}
	if runErr.Error() != execErr.Error() {
		t.Fatalf("the workspace refusal is the same words on either door: run %q exec %q", runErr, execErr)
	}
	if spawn.count() != 0 {
		t.Fatalf("a refused call spawns nothing, got %d", spawn.count())
	}
}
