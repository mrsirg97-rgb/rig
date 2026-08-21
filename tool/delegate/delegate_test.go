package delegate_test

import (
	"context"
	"encoding/json"
	"github.com/mrsirg97-rgb/rig/core"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/store/state"
	"github.com/mrsirg97-rgb/rig/tool/delegate"
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

// fakeFetch answers the busy probes: a resident other model means busy.
func fakeFetch(resident string) func(url string) (json.RawMessage, error) {
	return func(url string) (json.RawMessage, error) {
		switch {
		case strings.HasSuffix(url, "/v1/models"):
			if resident == "" {
				return json.RawMessage(modelsJSON(nil)), nil
			}
			return json.RawMessage(modelsJSON(nil)), nil
		case strings.HasSuffix(url, "/running"):
			if resident == "" {
				return json.RawMessage(runningJSON()), nil
			}
			return json.RawMessage(runningJSON(resident)), nil
		}
		return nil, jsonError("unexpected url " + url)
	}
}

type jsonErr string

func (e jsonErr) Error() string { return string(e) }

func jsonError(s string) error { return jsonErr(s) }

// fakeSpawn records calls; an optional record runs before the pinned
// result returns (the happy path writes the worker's session row).
type fakeSpawn struct {
	mu       sync.Mutex
	calls    []fakeCall
	result   sched.SpawnResult
	err      error
	record   func()
	block    <-chan struct{}
	deadline time.Duration
}

type fakeCall struct {
	Argv []string
	Cwd  string
	Ctx  context.Context
}

func (f *fakeSpawn) spawn(ctx context.Context, argv []string, cwd string) (sched.SpawnResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{Argv: argv, Cwd: cwd, Ctx: ctx})
	f.mu.Unlock()
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
	if f.block != nil {
		<-f.block
	}
	if f.record != nil {
		f.record()
	}
	return f.result, f.err
}

// harness wires a scratch scheduler home + cwd-scope store and a rig home
// with a state-store directory.
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
	db, _, err := store.Open(filepath.Join(home, sched.CwdHash(sessionCwd)+".sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatalf("open scheduler store: %v", err)
	}
	return &harness{home: home, rigHome: rigHome, db: db}
}

func (h *harness) newTool(t *testing.T, fetch sched.Fetch, spawn sched.Spawn) core.Tool {
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
	})
}

// seedSession writes the worker's session row into the state store the
// tool queries (SPEC_DELEGATE 3: the transcript is resumable).
func seedSession(t *testing.T, rigHome, cwd string) string {
	t.Helper()
	db, _, err := store.Open(state.StorePath(rigHome, cwd), state.Statements(), state.SchemaVersion)
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

func TestDelegateHappyPathFeedsBackAndRecords(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	wd, _ := os.Getwd()
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "the answer"}}
	// the worker records its session row at startup (the resumable transcript).
	spawn.record = func() { seedSession(t, h.rigHome, wd) }
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	out, err := tool.Exec(context.Background(), runArgs("do the sweep"))
	if err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if !strings.Contains(out, "the answer") {
		t.Fatalf("the last message must be fed back:\n%s", out)
	}
	if !strings.Contains(out, "delegate: exit 0 · ") || !strings.Contains(out, "· session sess-delegate · log runs/") {
		t.Fatalf("the trailer must carry exit, duration, session id, log path:\n%s", out)
	}
	c := spawn.calls[0]
	if c.Cwd != wd || len(c.Argv) < 5 || c.Argv[1] != "-p" {
		t.Fatalf("spawn argv/cwd: %v %v", c.Argv, c.Cwd)
	}
	if !strings.Contains(c.Argv[2], "do the sweep") {
		t.Fatalf("the prompt must carry the task: %q", c.Argv[2])
	}

	// the record: an ad-hoc job row (no crontab, name delegate:...) and a
	// run with a log path.
	var name, cron, state string
	if err := h.db.DB.QueryRow(`SELECT name, cron, state FROM jobs`).Scan(&name, &cron, &state); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "delegate:") || cron != "once" || state != "active" {
		t.Fatalf("ad-hoc job row: name=%q cron=%q state=%q", name, cron, state)
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

func TestDelegateCwdRefusalNamesThePath(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	b, _ := json.Marshal(map[string]any{"task": "t", "cwd": "/elsewhere"})
	out, err := tool.Exec(context.Background(), b)
	if err == nil || !strings.Contains(err.Error(), "outside the session's cwd") {
		t.Fatalf("the cwd refusal must name the path: (%q, %v)", out, err)
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn on a refused cwd")
	}
}

func TestDelegateBusyRefusalNamesTheHolder(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{}
	tool := h.newTool(t, fakeFetch("other-model"), spawn.spawn)
	out, err := tool.Exec(context.Background(), runArgs("t"))
	if err == nil || !strings.Contains(err.Error(), "held by other-model") {
		t.Fatalf("the busy refusal must name the holder: (%q, %v)", out, err)
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn on a busy GPU")
	}
}

func TestDelegateTimeoutNamesItAndTheSpawnSawTheDeadline(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	wd, _ := os.Getwd()
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 1, TimedOut: true, Stderr: "hung"}}
	spawn.record = func() { seedSession(t, h.rigHome, wd) }
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	b, _ := json.Marshal(map[string]any{"task": "t", "timeoutMs": 100})
	out, err := tool.Exec(context.Background(), b)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("the timeout must be a named error: (%q, %v)", out, err)
	}
	if !strings.Contains(err.Error(), "process tree killed") {
		t.Fatalf("the timeout error must name the kill: %v", err)
	}
	if spawn.deadline <= 0 || spawn.deadline > 200*time.Millisecond {
		t.Fatalf("the spawn must carry the timeoutMs deadline, got %v", spawn.deadline)
	}
}

func TestDelegateOneInFlightRefuses(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	block := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}, block: block}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	first := make(chan string, 1)
	go func() {
		out, err := tool.Exec(context.Background(), runArgs("t"))
		if err != nil {
			first <- "err: " + err.Error()
			return
		}
		first <- out
	}()
	// wait for the first spawn to be in flight
	deadline := time.Now().Add(2 * time.Second)
	for len(spawn.calls) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first spawn never started")
		}
		time.Sleep(time.Millisecond)
	}
	out, err := tool.Exec(context.Background(), runArgs("t2"))
	if err == nil || !strings.Contains(err.Error(), "already in flight") {
		t.Fatalf("the second call must refuse: (%q, %v)", out, err)
	}
	close(block)
	<-first
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
