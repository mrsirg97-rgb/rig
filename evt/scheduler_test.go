package evt_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/evt"
)

func TestSchedulerStartErrorsAreNamed(t *testing.T) {
	if err := evt.NewScheduler(nil).Start(); !errors.Is(err, evt.ErrNoEngine) {
		t.Fatalf("nil engine: %v, want ErrNoEngine", err)
	}
	s := evt.NewScheduler(evt.NewEngine())
	if err := s.Start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	defer s.Stop()
	if err := s.Start(); !errors.Is(err, evt.ErrStarted) {
		t.Fatalf("second start: %v, want ErrStarted", err)
	}
}

func TestSchedulerScheduleStopJoins(t *testing.T) {
	s := evt.NewScheduler(evt.NewEngine())
	var ran atomic.Int64
	s.Schedule(evt.Func(func(context.Context) { ran.Add(1) }), 1)
	select {
	case <-s.Done():
	default:
		t.Fatal("Done must read closed before Start")
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return ran.Load() == 1 })
	select {
	case <-s.Done():
		t.Fatal("Done must be open while running")
	default:
	}
	s.Stop()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Stop must join the loop")
	}
	s.Stop()
}

func TestSchedulerNilEngineSchedulesNothing(t *testing.T) {
	if id := evt.NewScheduler(nil).Schedule(evt.Func(func(context.Context) {}), 1); id != 0 {
		t.Fatalf("id %d, want 0", id)
	}
}
