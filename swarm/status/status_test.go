package status_test

import (
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/swarm/status"
)

type rec struct {
	mu     sync.Mutex
	events []core.Event
}

func (r *rec) Notify(ev core.Event) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func TestEmitterBuildsOnlyWhenAFrameIsDue(t *testing.T) {
	r := &rec{}
	e := status.New(r.Notify)
	var mu sync.Mutex
	builds := 0
	build := func() core.SwarmStatus {
		mu.Lock()
		builds++
		mu.Unlock()
		return core.SwarmStatus{}
	}
	for i := 0; i < 10; i++ {
		e.Emit(build)
	}
	if got := builds; got != 1 {
		t.Fatalf("builds = %d, want 1 (the snapshot fold must not run per stream chunk)", got)
	}
	time.Sleep(300 * time.Millisecond)
	e.Emit(build)
	e.Emit(build)
	if got := builds; got != 2 {
		t.Fatalf("builds after the window = %d, want 2", got)
	}
	e.Force(build)
	if got := builds; got != 3 {
		t.Fatalf("force builds = %d, want 3", got)
	}
	r.mu.Lock()
	n := len(r.events)
	r.mu.Unlock()
	if n != 3 {
		t.Fatalf("delivered = %d, want 3", n)
	}
}
