package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	rig "github.com/mrsirg97-rgb/rig/v3"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
)

type muteMember struct {
	machineroom.Member
	asks int
}

func (m *muteMember) Admits(version string) bool {
	m.asks++
	return version == machineroom.JobStatusSince
}

func (m *muteMember) Act(payload evt.Signal, key int64, priority int, posted func(admitted bool)) {}

func TestTheTellerAsksTheRoomGateOnceNotPerPost(t *testing.T) {
	stub := &muteMember{}
	tell := jobTeller(stub, nil)
	tell(machineroom.JobStatus{Job: "j1", State: machineroom.JobRunning})
	tell(machineroom.JobStatus{Job: "j2", State: machineroom.JobRunning})
	if stub.asks != 1 {
		t.Fatalf("the room gate was asked %d times across two posts, want once at the teller's build", stub.asks)
	}
}

type tellFixture struct {
	notices chan evt.Notice
	cancel  context.CancelFunc
}

func newTellFixture(t *testing.T) (tellFixture, broadcast.Member) {
	t.Helper()
	engine, fleetroom := newFleet()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go engine.Start(ctx)
	f := tellFixture{notices: make(chan evt.Notice, 4), cancel: cancel}
	voice := fleetroom.Add(rig.MemberDecision)
	fleetroom.Add(rig.MemberFrontend).Subscribe(ctx, func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if n, ok := m.Signal().(evt.Notice); ok {
				f.notices <- n
			}
		}
	})
	return f, voice
}

func TestATerminalPostNeverAcknowledgedReturnsInsideTheBound(t *testing.T) {
	_, voice := newTellFixture(t)
	tell := jobTeller(&muteMember{}, voice)
	done := make(chan struct{})
	go func() {
		tell(machineroom.JobStatus{Job: "j1", State: machineroom.JobDone})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the teller never came back from a terminal post whose acknowledgement was dropped")
	}
}

func TestAGivenUpTerminalPostIsSaidLoud(t *testing.T) {
	f, voice := newTellFixture(t)
	tell := jobTeller(&muteMember{}, voice)
	go tell(machineroom.JobStatus{Job: "j1", State: machineroom.JobDone})
	select {
	case n := <-f.notices:
		if n.Level != evt.LevelError {
			t.Fatalf("the give-up said %v, want an error", n.Level)
		}
		if !strings.Contains(n.Text, "lost") {
			t.Fatalf("the give-up must say the status may be lost: %q", n.Text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the give-up was silent")
	}
}
