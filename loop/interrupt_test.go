package loop_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	rig "github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/loop"
)

type requestLog struct {
	inner core.Provider
	reqs  [][]core.Message
}

func (p *requestLog) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	p.reqs = append(p.reqs, append([]core.Message(nil), req.Messages...))
	return p.inner.Stream(ctx, req)
}

func steered(t *testing.T, cut []core.Event, at func(core.Event) bool) (*core.Session, *requestLog) {
	t.Helper()
	p := &requestLog{inner: &scriptedProvider{turns: []scriptedTurn{
		{events: cut, holdAfter: true},
		{events: []core.Event{textEv("delivered"), doneEv()}},
	}}}
	f := &steeringFrontend{inputs: []string{"speak", "steer"}, steerAt: at}
	session := core.NewSession()
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
		rig.WithTools(&scriptedTool{name: "echo"}),
	)
	k.Session = session
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("steering must not end the run, got %v", err)
	}
	if hasFault(f.events) {
		t.Fatal("steering is not a fault")
	}
	return session, p
}

func isText(ev core.Event) bool { _, ok := ev.(core.TextDelta); return ok }

func isReasoning(ev core.Event) bool { _, ok := ev.(core.ReasoningDelta); return ok }

func isCall(ev core.Event) bool { _, ok := ev.(core.ToolCallEvent); return ok }

func TestAReplyCutDuringReasoningStaysInTheTranscript(t *testing.T) {
	session, _ := steered(t, []core.Event{core.ReasoningDelta{Text: "weighing "}}, isReasoning)
	wantTranscript(t, session,
		core.Message{Role: core.RoleUser, Content: "speak"},
		core.Message{Role: core.RoleAssistant, Reasoning: "weighing "},
		core.Message{Role: core.RoleUser, Content: "steer"},
		core.Message{Role: core.RoleAssistant, Content: "delivered"},
	)
}

func TestACallThatStreamedBeforeTheCutIsNotInTheTranscript(t *testing.T) {
	call := core.ToolCall{ID: "c1", Name: "echo", Args: json.RawMessage(`{}`)}
	session, _ := steered(t, []core.Event{textEv("running it "), callEv(call)}, isCall)
	wantTranscript(t, session,
		core.Message{Role: core.RoleUser, Content: "speak"},
		core.Message{Role: core.RoleAssistant, Content: "running it "},
		core.Message{Role: core.RoleUser, Content: "steer"},
		core.Message{Role: core.RoleAssistant, Content: "delivered"},
	)
}

func TestACutBeforeTheFirstTokenAppendsNothing(t *testing.T) {
	p := &scriptedProvider{turns: []scriptedTurn{
		{holdAfter: true},
		{events: []core.Event{textEv("delivered"), doneEv()}},
	}}
	f := &steeringFrontend{inputs: []string{"speak", "steer"}}
	hold := &holdingProvider{inner: p, started: func() { f.handle() }}
	session := core.NewSession()
	k := rig.New(
		rig.WithProvider(hold),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
	)
	k.Session = session
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("steering must not end the run, got %v", err)
	}
	wantTranscript(t, session,
		core.Message{Role: core.RoleUser, Content: "speak"},
		core.Message{Role: core.RoleUser, Content: "steer"},
		core.Message{Role: core.RoleAssistant, Content: "delivered"},
	)
}

type holdingProvider struct {
	inner   core.Provider
	started func()
	fired   bool
}

func (p *holdingProvider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	ch, err := p.inner.Stream(ctx, req)
	if !p.fired {
		p.fired = true
		p.started()
	}
	return ch, err
}

func TestTheRequestAfterASteerExtendsTheRequestBeforeIt(t *testing.T) {
	_, p := steered(t, []core.Event{core.ReasoningDelta{Text: "weighing "}, textEv("partial ")}, isText)
	if len(p.reqs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(p.reqs))
	}
	before, after := p.reqs[0], p.reqs[1]
	if len(after) != len(before)+2 {
		t.Fatalf("the steer adds the cut reply and the operator's message: %d -> %d messages", len(before), len(after))
	}
	if !reflect.DeepEqual(after[:len(before)], before) {
		t.Fatalf("the request before the steer is not a prefix of the one after it:\n%s\n---\n%s", dumpList(before), dumpList(after))
	}
	if after[len(before)].Role != core.RoleAssistant || after[len(before)+1].Role != core.RoleUser {
		t.Fatalf("after the prefix: the cut reply, then the steer; got %s", dumpList(after[len(before):]))
	}
}
