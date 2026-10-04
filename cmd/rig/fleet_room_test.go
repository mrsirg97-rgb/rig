package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	"github.com/mrsirg97-rgb/rig/v2/swarm"
)

type recordingFrontend struct {
	mu     sync.Mutex
	events []core.Event
}

func (f *recordingFrontend) Input(ctx context.Context) (string, error) { return "", nil }

func (f *recordingFrontend) Notify(ev core.Event) {
	f.mu.Lock()
	f.events = append(f.events, ev)
	f.mu.Unlock()
}

func (f *recordingFrontend) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type explodingFrontend struct{}

func (explodingFrontend) Input(ctx context.Context) (string, error) { return "", nil }
func (explodingFrontend) Notify(ev core.Event)                      { panic("frontend exploded") }

func awaitCount(t *testing.T, f *recordingFrontend, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for f.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d events, have %d", n, f.count())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func recorderFor(r *root, fe core.Frontend) *state.Recorder {
	return state.NewRecorder(fe, r.sdb, "/tmp/wt", r.activeID, Version, r.session.ID, r.session)
}

func TestARoomMessageReachesTheRecorderThatExistsAtDelivery(t *testing.T) {
	r := testRoot(nullFrontend{})
	storeRoot(t, r)
	r.fleet()
	go r.engine.Start(context.Background())
	defer r.engine.Stop()
	first := &recordingFrontend{}
	r.rec = recorderFor(r, first)
	member := r.room.Add(swarm.SupervisorID)
	member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(swarm.SupervisorID, true, core.SwarmNotice{Text: "w1 died"}))
	awaitCount(t, first, 1)
	second := &recordingFrontend{}
	r.rec = recorderFor(r, second)
	member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(swarm.SupervisorID, true, core.SwarmNotice{Text: "after the swap"}))
	awaitCount(t, second, 1)
	if first.count() != 1 {
		t.Fatalf("the old recorder must not get the post-swap frames, got %d", first.count())
	}
	if n, ok := second.events[0].(core.SwarmNotice); !ok || n.Text != "after the swap" {
		t.Fatalf("the new recorder got %+v", second.events[0])
	}
}

func TestAPanickingFrontendIsRecoveredLoudAndTheRoomKeepsDelivering(t *testing.T) {
	r := testRoot(nullFrontend{})
	storeRoot(t, r)
	r.fleet()
	errOut := &lockedBuffer{}
	r.errOut = errOut
	go r.engine.Start(context.Background())
	defer r.engine.Stop()
	r.rec = recorderFor(r, explodingFrontend{})
	member := r.room.Add(swarm.SupervisorID)
	member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(swarm.SupervisorID, true, core.SwarmNotice{Text: "boom"}))
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(errOut.String(), "frontend exploded") {
		if time.Now().After(deadline) {
			t.Fatalf("the panic must land loud on stderr, got %q", errOut.String())
		}
		time.Sleep(2 * time.Millisecond)
	}
	calm := &recordingFrontend{}
	r.rec = recorderFor(r, calm)
	member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(swarm.SupervisorID, true, core.SwarmNotice{Text: "still here"}))
	awaitCount(t, calm, 1)
}

func TestHeartbeatsAreOneFrameUntilTheLoopRuns(t *testing.T) {
	r := testRoot(nullFrontend{})
	storeRoot(t, r)
	r.fleet()
	fe := &recordingFrontend{}
	r.rec = recorderFor(r, fe)
	member := r.room.Add(swarm.SupervisorID)
	for i := 0; i < 20; i++ {
		member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(swarm.SupervisorID, true, core.SwarmStatus{Pending: i}))
	}
	if n := len(r.engine.Pending()); n != 1 {
		t.Fatalf("twenty status snapshots before the loop runs are one pending event, got %d", n)
	}
	go r.engine.Start(context.Background())
	defer r.engine.Stop()
	awaitCount(t, fe, 1)
	if st := fe.events[0].(core.SwarmStatus); st.Pending != 19 {
		t.Fatalf("the frame that lands is the latest, got pending %d", st.Pending)
	}
}
