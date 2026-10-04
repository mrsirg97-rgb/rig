package delegate_test

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/evt"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/tool/delegate"
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
	until  func() bool
}

func (f *statusSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	id, w, ok := sched.FleetFrom(ctx)
	if !ok {
		panic("the spawn context carries no fleet pipe")
	}
	wire := broadcast.NewPipeTransport(id, w, broadcast.NewJSONEncoder())
	for i := 0; i < 30; i++ {
		wire.Send(ctx, func(error) {}, broadcast.Heartbeat(id, true))
	}
	deadline := time.Now().Add(2 * time.Second)
	for f.until != nil && !f.until() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	return f.result, nil
}

func TestDelegateEmitsSwarmStatus(t *testing.T) {
	h := newHarness(t, "/tmp/wt")
	fe := &recordFrontend{}
	engine := evt.NewEngine()
	room := broadcast.NewRoom("fleet", func(origin int64) broadcast.Transport {
		return broadcast.NewLoopTransport(origin, engine, rig.PriorityFleet)
	})
	room.Add(-1).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if err == nil && m.Event() != nil {
				fe.Notify(m.Event())
			}
		}
	})
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
		Sandbox:      "off",
		Room:         room,
	})
	if _, err := tool.Exec(context.Background(), json.RawMessage(`{"task":"sweep the floor"}`)); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if n := len(engine.Pending()); n != 4 {
		t.Fatalf("before the loop runs: the claim and the exit are one pending frame per other member (the frontend, the minted worker), the thirty heartbeats one per listener (the frontend, the tool), got %d", n)
	}
	go engine.Start(context.Background())
	defer engine.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for len(fe.statuses()) < 1 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	st := fe.statuses()
	if len(st) != 1 {
		t.Fatalf("statuses = %d, want the one latest frame", len(st))
	}
	last := st[len(st)-1]
	if len(last.Workers) != 0 {
		t.Fatalf("the latest frame is the exit, which clears the worker row: %+v", last)
	}
	if last.Pending != 0 || last.Review != 0 {
		t.Fatalf("a delegate carries zero queue counts: %+v", last)
	}
}

func TestDelegateFramesRunningThenCleared(t *testing.T) {
	h := newHarness(t, "/tmp/wt")
	fe := &recordFrontend{}
	engine := evt.NewEngine()
	go engine.Start(context.Background())
	defer engine.Stop()
	room := broadcast.NewRoom("fleet", func(origin int64) broadcast.Transport {
		return broadcast.NewLoopTransport(origin, engine, rig.PriorityFleet)
	})
	room.Add(-1).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if err == nil && m.Event() != nil {
				fe.Notify(m.Event())
			}
		}
	})
	spawn := &statusSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	spawn.until = func() bool {
		for _, s := range fe.statuses() {
			if len(s.Workers) == 1 && !s.Workers[0].Heartbeat.IsZero() {
				return true
			}
		}
		return false
	}
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
		Sandbox:      "off",
		Room:         room,
	})
	if _, err := tool.Exec(context.Background(), json.RawMessage(`{"task":"sweep the floor"}`)); err != nil {
		t.Fatalf("exec: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := fe.statuses()
		if len(st) > 0 && len(st[len(st)-1].Workers) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the exit frame never landed: %+v", st)
		}
		time.Sleep(2 * time.Millisecond)
	}
	st := fe.statuses()
	if len(st) < 2 {
		t.Fatalf("statuses = %d, want at least the claim and the exit", len(st))
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
		t.Fatalf("the pipe never updated the heartbeat")
	}
}
