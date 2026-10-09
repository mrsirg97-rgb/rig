package swarm_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/evt"
	"github.com/mrsirg97-rgb/rig/v2/swarm"
)

func deliver(t *testing.T, engine evt.Engine, m broadcast.Member, message broadcast.Message) {
	t.Helper()
	m.Publish(context.Background(), func(error) {}, message)
	done := make(chan struct{})
	engine.Add(evt.Func(func(context.Context) { close(done) }), rig.PriorityFleet)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the room never delivered")
	}
}

func TestAForeignMessageNeverPublishesTheSwarm(t *testing.T) {
	h := newHarness(t)
	pubs := make(chan core.SwarmStatus, 32)
	h.room.Add(64).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if err == nil && m.Origin() == swarm.SupervisorID {
				if st, ok := m.Event().(core.SwarmStatus); ok {
					pubs <- st
				}
			}
		}
	})
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})

	voice := h.room.Add(rig.MemberDelegate)
	deliver(t, h.engine, voice, broadcast.NewMessage(rig.MemberDelegate, true, core.SwarmStatus{
		Workers: []core.SwarmWorker{{ID: 1, Role: "delegate", Task: "sweep", State: "running"}},
	}))
	deliver(t, h.engine, voice, broadcast.NewMessage(rig.MemberDelegate, true, core.ToolStart{
		Call: core.ToolCall{Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)},
	}))
	select {
	case st := <-pubs:
		t.Fatalf("the swarm spoke for a message that is not its own: %+v", st)
	case <-time.After(300 * time.Millisecond):
	}

	deliver(t, h.engine, voice, broadcast.Heartbeat(1, true))
	select {
	case st := <-pubs:
		if len(st.Workers) != 1 || st.Workers[0].ID != 1 || st.Workers[0].Heartbeat.IsZero() {
			t.Fatalf("the own worker's heartbeat published the wrong status: %+v", st)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the own worker's heartbeat never published")
	}
}
