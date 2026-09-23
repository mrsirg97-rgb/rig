package delegate_test

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/tool/delegate"
)

type recordFrontend struct {
	mu     sync.Mutex
	events []core.Event
}

func (r *recordFrontend) Input(ctx context.Context) (string, error) { return "", io.EOF }

func (r *recordFrontend) Notify(ev core.Event) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func (r *recordFrontend) statuses() []core.SwarmStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []core.SwarmStatus
	for _, ev := range r.events {
		if s, ok := ev.(core.SwarmStatus); ok {
			out = append(out, s)
		}
	}
	return out
}

type statusSpawn struct {
	mu     sync.Mutex
	calls  int
	result sched.SpawnResult
}

func (f *statusSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	for i := 0; i < 30; i++ {
		observe([]byte("rig: heartbeat\n"))
	}
	return f.result, nil
}

func TestDelegateEmitsSwarmStatus(t *testing.T) {
	h := newHarness(t, "/tmp/wt")
	fe := &recordFrontend{}
	spawn := &statusSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	tool := delegate.New(delegate.Opts{
		DB:           h.db,
		Home:         h.home,
		RigHome:      h.rigHome,
		StateDir:     filepath.Join(h.rigHome, "sessions"),
		SwapURL:      "http://127.0.0.1:8090",
		WorkerCmd:    []string{"/x/rig"},
		DefaultModel: "qwen3.8-workers",
		Fetch:        fakeFetch(""),
		Spawn:        spawn.spawn,
		Slots:        1,
		Sandbox:      "off",
		Notify:       fe.Notify,
	})
	if _, err := tool.Exec(context.Background(), json.RawMessage(`{"task":"sweep the floor"}`)); err != nil {
		t.Fatalf("exec: %v", err)
	}
	st := fe.statuses()
	if len(st) < 2 {
		t.Fatalf("statuses = %d, want the claim and the exit", len(st))
	}
	if len(st) > 4 {
		t.Fatalf("30 byte observes coalesced into %d statuses, want a few", len(st))
	}
	first := st[0]
	if len(first.Workers) != 1 || first.Workers[0].Task != "sweep the floor" ||
		first.Workers[0].State != "running" || first.Workers[0].Role != "worker" {
		t.Fatalf("the first status is not the running delegate: %+v", first)
	}
	last := st[len(st)-1]
	if len(last.Workers) != 0 {
		t.Fatalf("the exit status must clear the worker row: %+v", last)
	}
	if last.Pending != 0 || last.Review != 0 {
		t.Fatalf("a delegate carries zero queue counts: %+v", last)
	}
	heartbeat := false
	for _, s := range st {
		if len(s.Workers) == 1 && !s.Workers[0].Heartbeat.IsZero() {
			heartbeat = true
		}
	}
	if !heartbeat {
		t.Fatalf("the bytes never updated the heartbeat")
	}
}
