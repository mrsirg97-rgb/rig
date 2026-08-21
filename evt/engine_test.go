package evt_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/evt"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(time.Millisecond)
	}
}

// The engine executes by priority then arrival: events added before
// Start drain in order (the C engine's contract over the queue's).
func TestEngineExecutesByPriorityThenArrival(t *testing.T) {
	e := evt.NewEngine()
	var mu sync.Mutex
	var order []int
	record := func(n int) evt.Context {
		return evt.Func(func(context.Context) {
			mu.Lock()
			order = append(order, n)
			mu.Unlock()
		})
	}
	e.Add(record(1), 1)
	e.Add(record(2), 5)
	e.Add(record(3), 5)
	e.Add(record(4), 3)
	last := make(chan struct{})
	e.Add(evt.Func(func(context.Context) { close(last) }), 0)
	go e.Start(context.Background())
	<-last
	e.Stop()
	mu.Lock()
	defer mu.Unlock()
	want := []int{2, 3, 4, 1}
	if len(order) != len(want) {
		t.Fatalf("order %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order %v, want %v", order, want)
		}
	}
}

// Multi-producer adds while running are all executed (libevt's
// multi-producer door), and the default idle wait does not spin.
func TestEngineMultiProducer(t *testing.T) {
	e := evt.NewEngine()
	var ran atomic.Int64
	go e.Start(context.Background())
	const producers, per = 4, 1000
	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				e.Add(evt.Func(func(context.Context) { ran.Add(1) }), i%7)
			}
		}(p)
	}
	wg.Wait()
	waitFor(t, func() bool { return ran.Load() == producers*per })
	e.Stop()
}

// An event may add an event: execution is outside the lock.
func TestEngineAddFromWithinAnEvent(t *testing.T) {
	e := evt.NewEngine()
	done := make(chan struct{})
	e.Add(evt.Func(func(context.Context) {
		e.Add(evt.Func(func(context.Context) { close(done) }), 9)
	}), 1)
	go e.Start(context.Background())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the nested add never ran (deadlock)")
	}
	e.Stop()
}

// WithTick runs the hook when idle (libevt's tick_fn), not while busy.
func TestEngineTickHookRunsWhenIdle(t *testing.T) {
	var ticks atomic.Int64
	e := evt.NewEngine(evt.WithTick(func(context.Context) {
		ticks.Add(1)
		time.Sleep(time.Millisecond)
	}))
	go e.Start(context.Background())
	waitFor(t, func() bool { return ticks.Load() >= 3 })
	ran := make(chan struct{})
	e.Add(evt.Func(func(context.Context) { close(ran) }), 1)
	<-ran
	e.Stop()
}

// Stop runs nothing further; the pending set stays visible, sorted.
func TestEngineStopLeavesPendingVisible(t *testing.T) {
	e := evt.NewEngine()
	started := make(chan struct{})
	release := make(chan struct{})
	e.Add(evt.Func(func(context.Context) { close(started); <-release }), 9)
	var ran atomic.Int64
	e.Add(evt.Func(func(context.Context) { ran.Add(1) }), 2)
	e.Add(evt.Func(func(context.Context) { ran.Add(1) }), 5)
	finished := make(chan struct{})
	go func() { e.Start(context.Background()); close(finished) }()
	<-started
	e.Stop()
	close(release)
	<-finished
	if ran.Load() != 0 {
		t.Fatalf("stop must run nothing further, ran %d", ran.Load())
	}
	p := e.Pending()
	if len(p) != 2 || p[0].Priority() != 5 || p[1].Priority() != 2 {
		t.Fatalf("pending %d sorted by priority, want [5 2]", len(p))
	}
}

// Cancelling Start's ctx is a Stop.
func TestEngineContextCancelStops(t *testing.T) {
	e := evt.NewEngine()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { e.Start(ctx); close(finished) }()
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return on cancel")
	}
}

// A negative priority clamps to 0 (libevt clamps at add).
func TestEngineNegativePriorityClampsToZero(t *testing.T) {
	e := evt.NewEngine()
	e.Add(evt.Func(func(context.Context) {}), -5)
	p := e.Pending()
	if len(p) != 1 || p[0].Priority() != 0 {
		t.Fatalf("pending %+v, want one event at priority 0", p)
	}
	if !e.Update(p[0].ID(), -1) {
		t.Fatal("update must find the pending id")
	}
	if e.Pending()[0].Priority() != 0 {
		t.Fatal("update clamps too")
	}
}

// The engine's Update raises a pending event: the latest-steer-wins
// rule phase 2 needs.
func TestEngineUpdateRaisesAPendingEvent(t *testing.T) {
	e := evt.NewEngine()
	var mu sync.Mutex
	var order []string
	record := func(s string) evt.Context {
		return evt.Func(func(context.Context) {
			mu.Lock()
			order = append(order, s)
			mu.Unlock()
		})
	}
	e.Add(record("a"), 5)
	low := e.Add(record("b"), 1)
	last := make(chan struct{})
	e.Add(evt.Func(func(context.Context) { close(last) }), 0)
	if !e.Update(low, 9) {
		t.Fatal("update must succeed before start")
	}
	go e.Start(context.Background())
	<-last
	e.Stop()
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "b" || order[1] != "a" {
		t.Fatalf("order %v, want [b a]", order)
	}
}
