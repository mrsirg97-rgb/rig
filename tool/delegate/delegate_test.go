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

func (f *fakeSpawn) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSpawn) spawn(ctx context.Context, argv []string, cwd string) (sched.SpawnResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{Argv: argv, Cwd: cwd, Ctx: ctx})
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
	f.mu.Unlock()
	if f.block != nil {
		<-f.block
	}
	if f.record != nil {
		f.record()
	}
	return f.result, f.err
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

func (h *harness) newTool(t *testing.T, fetch sched.Fetch, spawn sched.Spawn) core.Tool {
	t.Helper()
	return h.newToolSlots(t, 0, fetch, spawn)
}

func (h *harness) newToolSlots(t *testing.T, slots int, fetch sched.Fetch, spawn sched.Spawn) core.Tool {
	t.Helper()
	return delegate.New(delegate.Opts{
		DB:           h.db,
		Home:         h.home,
		RigHome:      h.rigHome,
		StateDir:     filepath.Join(h.rigHome, "sessions"),
		SwapURL:      "http://127.0.0.1:8090",
		WorkerCmd:    []string{"/x/rig"},
		DefaultModel: "qwen3.8-workers",
		Slots:        slots,
		Sandbox:      "off",
		Fetch:        fetch,
		Spawn:        spawn,
	})
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
	if !strings.Contains(c.Argv[2], "do the sweep") {
		t.Fatalf("the prompt must carry the task: %q", c.Argv[2])
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
	b, _ := json.Marshal(map[string]any{"task": "t", "cwd": link})
	_, err = tool.Exec(context.Background(), b)
	if err == nil || !strings.Contains(err.Error(), "outside the session's cwd") {
		t.Fatalf("the resolved symlink escape must refuse: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatal("no spawn on a symlink escape")
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

	deadline := time.Now().Add(2 * time.Second)
	for spawn.count() == 0 {
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

func TestDelegateSlotsGateCounts(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	block := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}, block: block}
	tool := h.newToolSlots(t, 2, fakeFetch(""), spawn.spawn)
	done := make(chan struct{}, 2)
	for _, task := range []string{"t1", "t2"} {
		go func(task string) {
			out, err := tool.Exec(context.Background(), runArgs(task))
			if err != nil {
				t.Logf("call %s: %v (%s)", task, err, out)
			}
			done <- struct{}{}
		}(task)
	}

	deadline := time.Now().Add(2 * time.Second)
	for spawn.count() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("both slots never started (count %d)", spawn.count())
		}
		time.Sleep(time.Millisecond)
	}
	out, err := tool.Exec(context.Background(), runArgs("t3"))
	if err == nil || !strings.Contains(err.Error(), "delegate slots are full (slots 2)") {
		t.Fatalf("the third call must refuse naming the full set: (%q, %v)", out, err)
	}
	close(block)
	<-done
	<-done
	if spawn.count() != 2 {
		t.Fatalf("exactly the slots' worth may run, got %d spawns", spawn.count())
	}
}

func TestDelegateSlotsOneKeepsTheStandingVoice(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	block := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}, block: block}
	tool := h.newToolSlots(t, 1, fakeFetch(""), spawn.spawn)
	first := make(chan struct{}, 1)
	go func() {
		_, _ = tool.Exec(context.Background(), runArgs("t"))
		first <- struct{}{}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for spawn.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("spawn never started")
		}
		time.Sleep(time.Millisecond)
	}
	out, err := tool.Exec(context.Background(), runArgs("t2"))
	if err == nil || !strings.Contains(err.Error(), "a delegation is already in flight (this session)") {
		t.Fatalf("slots 1 must keep the standing voice: (%q, %v)", out, err)
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
