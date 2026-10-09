package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

func escArmSession(t *testing.T, stops *atomic.Int64) *scriptedSession {
	t.Helper()
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60), WithSize(sizeFixture(60, 20)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithIdleInterrupt(func() { stops.Add(1) }),
	)
	if line := s.prompt(promptMark(th), "go\n"); line != "go" {
		t.Fatalf("prompt = %q", line)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "delegate", Task: "t1", Heartbeat: time.Now(), State: "running"},
	}})
	s.await("delegating")
	return s
}

func TestOneEscPaintsTheLineAndStopsNothing(t *testing.T) {
	stops := new(atomic.Int64)
	s := escArmSession(t, stops)

	s.si.feed("\x1b")
	s.await("esc again to stop 1 worker")
	if stops.Load() != 0 {
		t.Fatalf("one esc stopped the batch %d times", stops)
	}
}

func TestTheSecondEscStopsTheBatch(t *testing.T) {
	stops := new(atomic.Int64)
	s := escArmSession(t, stops)

	s.si.feed("\x1b")
	s.await("esc again to stop 1 worker")
	s.si.feed("\x1b")
	deadline := time.Now().Add(2 * time.Second)
	for stops.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the second esc never stopped the batch")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAKeystrokeClearsTheArm(t *testing.T) {
	stops := new(atomic.Int64)
	s := escArmSession(t, stops)

	s.si.feed("\x1b")
	s.await("esc again to stop 1 worker")
	s.si.feed("a")
	s.screenUntil(t, 60, 20, 2*time.Second, func(j string) bool {
		return !strings.Contains(j, "esc again to stop")
	}, "a keystroke did not clear the arm")
	if stops.Load() != 0 {
		t.Fatalf("the cleared arm stopped the batch %d times", stops)
	}
}

func TestTheArmExpiresWithoutStopping(t *testing.T) {
	stops := new(atomic.Int64)
	s := escArmSession(t, stops)

	s.si.feed("\x1b")
	s.await("esc again to stop 1 worker")
	s.ticks <- time.Now().Add(escArmWindow + time.Second)
	s.screenUntil(t, 60, 20, 2*time.Second, func(j string) bool {
		return !strings.Contains(j, "esc again to stop")
	}, "the arm never expired")
	if stops.Load() != 0 {
		t.Fatalf("the expired arm stopped the batch %d times", stops)
	}
}
