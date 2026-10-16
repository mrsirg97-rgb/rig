package swarm_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3"
)

func drainProtocol(sub evt.Submission, work evt.Closure, ctx context.Context) {
	select {
	case <-sub.Settled():
		work.Resolve(ctx)
	default:
		<-sub.Settled()
	}
	if sub.Started() {
		sub.Release()
	}
}

func drainWait(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newCap(t *testing.T, workers int) (evt.Cap, evt.Engine, context.Context) {
	t.Helper()
	engine := evt.NewEngine()
	go engine.Start(context.Background())
	t.Cleanup(engine.Stop)
	return evt.NewCap(engine, workers), engine, context.Background()
}

func capInFlight(engine evt.Engine, cap evt.Cap) int {
	done := make(chan struct{})
	var got int
	engine.Add(evt.Func(func(context.Context) { got = cap.InFlight(); close(done) }), rig.PriorityFleet)
	<-done
	return got
}

func TestDrainProtocolRunsTheQueuedWorkOnce(t *testing.T) {
	cap, engine, ctx := newCap(t, 1)
	holder := evt.Func(func(context.Context) {})
	subA := cap.Submit(ctx, holder, rig.PriorityFleet)
	var runs atomic.Int64
	var ran atomic.Bool
	queued := evt.Func(func(context.Context) {
		if !ran.CompareAndSwap(false, true) {
			return
		}
		runs.Add(1)
	})
	subB := cap.Submit(ctx, queued, rig.PriorityFleet)
	drainProtocol(subA, holder, ctx)
	drainWait(t, "the cap started the queued work", func() bool { return subB.Started() })
	drainProtocol(subB, queued, ctx)
	if got := runs.Load(); got != 1 {
		t.Fatalf("the queued closure ran %d times, want once (the caller's resolve and the cap's run must not both fire)", got)
	}
	if got := capInFlight(engine, cap); got != 0 {
		t.Fatalf("cap.InFlight() = %d, want the slot given back", got)
	}
}

func TestDrainProtocolReleasesOnlyWhatTheCapStarted(t *testing.T) {
	cap, engine, ctx := newCap(t, 1)
	holder := evt.Func(func(context.Context) {})
	subA := cap.Submit(ctx, holder, rig.PriorityFleet)
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	var gateErr error
	stopped := evt.Func(func(gate context.Context) { gateErr = gate.Err() })
	subB := cap.Submit(dead, stopped, rig.PriorityFleet)
	drainProtocol(subA, holder, ctx)
	drainProtocol(subB, stopped, dead)
	if gateErr == nil || !errors.Is(gateErr, context.Canceled) {
		t.Fatalf("the settle-stopped closure ran with %v, want the dead gate", gateErr)
	}
	if got := capInFlight(engine, cap); got != 0 {
		t.Fatalf("cap.InFlight() = %d, want 0 (a submission settled stopped never took a slot, so it must not give one back)", got)
	}
}
