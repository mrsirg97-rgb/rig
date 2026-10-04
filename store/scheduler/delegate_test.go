package scheduler_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/evt"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

type delegateSpawn struct {
	mu      sync.Mutex
	calls   []delegateCall
	result  sched.SpawnResult
	err     error
	block   chan struct{}
	onSpawn func(ctx context.Context, observe func([]byte))
}

type delegateCall struct {
	Argv []string
	Cwd  string
	Ctx  context.Context
}

func (f *delegateSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, delegateCall{Argv: argv, Cwd: cwd, Ctx: ctx})
	f.mu.Unlock()
	if f.onSpawn != nil {
		f.onSpawn(ctx, observe)
	}
	if f.block != nil {
		<-f.block
	}
	return f.result, f.err
}

func (f *delegateSpawn) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func delegateFetch(t *testing.T, busyFirst bool, failing string) func(url string) (json.RawMessage, error) {
	t.Helper()
	var mu sync.Mutex
	runningCalls := 0
	return func(url string) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		if failing != "" {
			return nil, jsonError(failing)
		}
		switch {
		case strings.HasSuffix(url, "/v1/models"):
			return json.RawMessage(modelsJSON(map[string]string{"qwen3.8-27b-workers": "unloaded"})), nil
		case strings.HasSuffix(url, "/running"):
			runningCalls++
			if busyFirst && runningCalls == 1 {
				return json.RawMessage(runningJSON("qwen3.8-27b")), nil
			}
			return json.RawMessage(runningJSON()), nil
		}
		return nil, jsonError("unexpected url " + url)
	}
}

func delegateInput(t *testing.T, fetch sched.Fetch, spawn sched.Spawn, mutate func(*sched.DelegateInput)) sched.DelegateInput {
	t.Helper()
	home := t.TempDir()
	db, _, _, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatalf("open scheduler store: %v", err)
	}
	in := sched.DelegateInput{
		DB:            db,
		Home:          home,
		Session:       "sess-super",
		Cwd:           t.TempDir(),
		Task:          "do the thing",
		Model:         "qwen3.8-27b-workers",
		WorkerSession: "worker-sess",
		Fetch:         fetch,
		Spawn:         spawn,
		WorkerCmd:     []string{"/x/rig"},
		SwapURL:       "http://127.0.0.1:8090",
		Timeout:       time.Minute,
		Sandbox:       "off",
	}
	if mutate != nil {
		mutate(&in)
	}
	return in
}

func TestDelegateHolderSkipStillRefuses(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	in := delegateInput(t, delegateFetch(t, true, ""), spawn.spawn, nil)
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("a named model while another is resident must refuse")
	} else if !strings.Contains(err.Error(), "held by") {
		t.Errorf("holder voice: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatalf("no spawn may happen on a skip: %d", spawn.count())
	}
}

func TestDelegateCheckFailureFailsClosed(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	in := delegateInput(t, delegateFetch(t, true, "models down"), spawn.spawn, nil)
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("a failed gate check must refuse")
	} else if !strings.Contains(err.Error(), "gate check failed") {
		t.Errorf("gate voice: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatalf("no spawn may happen on a failed check: %d", spawn.count())
	}
}

func TestDelegateWorkerHeartbeatIsPublishedAsItsMember(t *testing.T) {
	engine := evt.NewEngine()
	go engine.Start(context.Background())
	defer engine.Stop()
	room := broadcast.NewRoom("fleet", func(origin int64) broadcast.Transport {
		return broadcast.NewLoopTransport(origin, engine, rig.PriorityFleet)
	})
	heard := make(chan int64, 8)
	room.Add(0).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if err == nil && m.Event() == nil {
				heard <- m.Origin()
			}
		}
	})
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	var env []string
	spawn.onSpawn = func(ctx context.Context, observe func([]byte)) {
		id, w, ok := sched.FleetFrom(ctx)
		if !ok {
			t.Error("the spawn context carries no fleet pipe")
			return
		}
		broadcast.NewPipeTransport(id, w, broadcast.NewJSONEncoder()).Send(ctx, func(error) {}, broadcast.Heartbeat(id, true))
	}
	in := delegateInput(t, delegateFetch(t, false, ""), func(ctx context.Context, argv []string, cwd string, e []string, observe func([]byte)) (sched.SpawnResult, error) {
		env = e
		return spawn.spawn(ctx, argv, cwd, e, observe)
	}, func(in *sched.DelegateInput) {
		in.Member = room.Add(7)
	})
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	select {
	case origin := <-heard:
		if origin != 7 {
			t.Fatalf("heartbeat origin = %d, want the worker's member 7", origin)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the worker's heartbeat never reached the room")
	}
	if !slices.Contains(env, sched.FleetEnv+"=7") {
		t.Fatalf("the child must learn its member id from the env, got %v", env)
	}
}

func TestDelegateSpawnCtxCancelsTheWorker(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 1, TimedOut: true, Stderr: "killed\n"}}
	spawnCtx, cancel := context.WithCancel(context.Background())
	captured := make(chan context.Context, 1)
	spawn.onSpawn = func(ctx context.Context, observe func([]byte)) {
		captured <- ctx
	}
	in := delegateInput(t, delegateFetch(t, false, ""), spawn.spawn, func(in *sched.DelegateInput) {
		in.SpawnCtx = spawnCtx
	})
	done := make(chan error, 1)
	go func() {
		_, err := sched.Delegate(in)
		done <- err
	}()
	seen := <-captured
	cancel()
	select {
	case <-seen.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the spawn context must be cancelled with SpawnCtx")
	}
	if err := <-done; err != nil {
		t.Fatalf("delegate: %v", err)
	}
}

func TestDelegateDefaultsAreUnchanged(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	in := delegateInput(t, delegateFetch(t, false, ""), spawn.spawn, nil)
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	if spawn.calls[0].Ctx == nil {
		t.Fatal("the default spawn context must not be nil")
	}
	if in.Member != nil {
		t.Fatal("the default input names no member; a worker nobody listens to gets no pipe")
	}
	if _, err := os.Stat(filepath.Join(in.Home, "runs")); err != nil {
		t.Fatalf("the run record must still land: %v", err)
	}
}

func TestABareFireWithNoAllowRunsAllowNoneAndNoReportBack(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	in := delegateInput(t, delegateFetch(t, false, ""), spawn.spawn, nil)
	in.Bare = true
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	var allow string
	for i, a := range spawn.calls[0].Argv {
		switch a {
		case "-allow":
			allow = spawn.calls[0].Argv[i+1]
		case "-p":
			if spawn.calls[0].Argv[i+1] != "-" {
				t.Fatalf("the prompt must not ride argv: %v", spawn.calls[0].Argv)
			}
		}
	}
	prompt, _ := sched.PromptFrom(spawn.calls[0].Ctx)
	if allow != sched.NoToolsAllow {
		t.Fatalf("a no-tools fire runs with no tool at all: %v", spawn.calls[0].Argv)
	}
	if prompt != in.Task {
		t.Fatalf("a fire with no tools cannot report back through rem: %q", prompt)
	}
}

func TestRemoteDelegateNeverConsultsTheSwap(t *testing.T) {
	failing := func(url string) (json.RawMessage, error) {
		return nil, jsonError("the swap must never be consulted: " + url)
	}
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	in := delegateInput(t, failing, spawn.spawn, func(in *sched.DelegateInput) {
		in.Model = "brain"
		in.Models = func() models.Table {
			tbl, err := models.New(models.Model{ID: "brain", Window: 1024, MaxTokens: 128, Reserve: 8, KeepRecent: 8, Role: models.RoleWorker, Remote: true, BaseURL: "https://endpoint.example/v1"})
			if err != nil {
				t.Fatal(err)
			}
			return tbl
		}
	})
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if spawn.count() != 1 {
		t.Fatalf("a remote delegate must spawn without the gate, spawned %d times", spawn.count())
	}
}

func runReason(t *testing.T, in sched.DelegateInput, id string) string {
	t.Helper()
	row := in.DB.DB.QueryRow(`SELECT reason FROM runs WHERE job_id = ?`, id)
	var reason string
	if err := row.Scan(&reason); err != nil {
		t.Fatalf("run reason: %v", err)
	}
	return reason
}

func TestDelegateRecordsCanceledReason(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: -1}}
	spawn.onSpawn = func(ctx context.Context, observe func([]byte)) {
		<-ctx.Done()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := delegateInput(t, delegateFetch(t, false, ""), spawn.spawn, func(in *sched.DelegateInput) {
		in.SpawnCtx = ctx
	})
	done := make(chan struct{})
	go func() {
		if _, err := sched.Delegate(in); err != nil {
			t.Errorf("delegate: %v", err)
		}
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the canceled delegate did not return")
	}
	if got := runReason(t, in, "j1"); got != "canceled" {
		t.Errorf("a canceled spawn must record the reason, got %q", got)
	}
}

func TestDelegateRecordsSignalReason(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: -1, Signal: syscall.SIGKILL}}
	in := delegateInput(t, delegateFetch(t, false, ""), spawn.spawn, nil)
	res, err := sched.Delegate(in)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if res.Exit != -1 {
		t.Fatalf("exit = %d, want -1", res.Exit)
	}
	if got := runReason(t, in, "j1"); got != "killed by signal 9" {
		t.Errorf("a signal death must record the signal, got %q", got)
	}
}

func TestDelegateRecordsTimeoutReason(t *testing.T) {
	timeoutSpawn := &delegateSpawn{result: sched.SpawnResult{Exit: 1, TimedOut: true}}
	in := delegateInput(t, delegateFetch(t, false, ""), timeoutSpawn.spawn, nil)
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if got := runReason(t, in, "j1"); got != "killed after timeout" {
		t.Errorf("a timed-out worker must record the reason, got %q", got)
	}
}

func TestADelegateFireNamesItsDeathAndItsLog(t *testing.T) {
	h, _ := setupJob(t, realCwd(t, "del"), nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: -1, Signal: 13}}
	res, err := sched.Delegate(sched.DelegateInput{
		DB: h.db, Home: h.home, Session: "sess-del", Cwd: h.sessCwd, Task: "do it",
		WorkerSession: "wsess-del", Model: "qwen3.8-workers",
		Fetch: fakeFetch([]string{"qwen3.8-27b-workers"}, fetchOpts{statuses: map[string]string{"qwen3.8-27b-workers": "loaded"}}),
		Spawn: spawn.spawn, WorkerCmd: []string{"/x/rig"},
		SwapURL: "http://127.0.0.1:8090",
		RigHome: h.rigHome, StateDir: filepath.Join(h.rigHome, "sessions"),
		Bare: true, Sandbox: "off",
		Models: modelTable(t, "qwen3.8-27b-workers"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != "killed by signal 13" {
		t.Fatalf("the result carries the spawn's death: %q", res.Reason)
	}
	if res.LogRel == "" {
		t.Fatal("the result carries the run log path")
	}
	ferr := res.FireError(h.home)
	if ferr == nil {
		t.Fatal("a dead fire is an error")
	}
	if !strings.Contains(ferr.Error(), "killed by signal 13") || !strings.Contains(ferr.Error(), res.LogRel) {
		t.Fatalf("the fire error names the death and the log: %v", ferr)
	}
	if !strings.Contains(ferr.Error(), "the review fire ended exit -1 (timed out false)") {
		t.Fatalf("the old wording stays: %v", ferr)
	}
}

func TestAHealthyFireIsNoError(t *testing.T) {
	res := sched.DelegateResult{Exit: 0}
	if err := res.FireError("home"); err != nil {
		t.Fatalf("exit 0 is no error: %v", err)
	}
}
