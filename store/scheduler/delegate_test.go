package scheduler_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
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
		Slots:         1,
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

func TestDelegateBusyWaitWaitsForTheGpuSlot(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	in := delegateInput(t, delegateFetch(t, true, ""), spawn.spawn, func(in *sched.DelegateInput) {
		in.WaitBusy = true
	})
	started := time.Now()
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if spawn.count() != 1 {
		t.Fatalf("spawn calls = %d, want 1", spawn.count())
	}
	if time.Since(started) < time.Second {
		t.Fatalf("the busy wait must poll the swap before spawning (waited %v)", time.Since(started))
	}
}

func TestDelegateBusySkipStillRefuses(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	in := delegateInput(t, delegateFetch(t, true, ""), spawn.spawn, nil)
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("a busy GPU with the default policy must refuse")
	} else if !strings.Contains(err.Error(), "busy") {
		t.Errorf("busy voice: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatalf("no spawn may happen on a skip: %d", spawn.count())
	}
}

func TestDelegateBusyCheckFailureFailsClosed(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	in := delegateInput(t, delegateFetch(t, true, "models down"), spawn.spawn, func(in *sched.DelegateInput) {
		in.WaitBusy = true
	})
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("a failed busy check must refuse")
	} else if !strings.Contains(err.Error(), "busy check failed") {
		t.Errorf("busy-check voice: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatalf("no spawn may happen on a failed check: %d", spawn.count())
	}
}

func TestDelegateObserveStreamsTheWorkerBytes(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	var observed []byte
	spawn.onSpawn = func(ctx context.Context, observe func([]byte)) {
		observe([]byte("rig: heartbeat\n"))
	}
	in := delegateInput(t, delegateFetch(t, false, ""), spawn.spawn, func(in *sched.DelegateInput) {
		in.Observe = func(p []byte) { observed = append(observed, p...) }
	})
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if !strings.Contains(string(observed), "rig: heartbeat") {
		t.Errorf("the observer must see the worker's bytes, got %q", observed)
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
	if in.Observe != nil {
		t.Fatal("the default observer must be nil")
	}
	if _, err := os.Stat(filepath.Join(in.Home, "runs")); err != nil {
		t.Fatalf("the run record must still land: %v", err)
	}
}
