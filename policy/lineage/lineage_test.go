package lineage_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/policy/lineage"
)

// a provider that answers each call with its script's next events and keeps the requests it was sent
type scripted struct {
	mu    sync.Mutex
	turns [][]evt.Signal
	got   []core.Request
}

func (p *scripted) Stream(ctx context.Context, req core.Request) (<-chan evt.Signal, error) {
	p.mu.Lock()
	evs := p.turns[len(p.got)]
	p.got = append(p.got, req)
	p.mu.Unlock()
	out := make(chan evt.Signal, len(evs))
	for _, e := range evs {
		out <- e
	}
	close(out)
	return out, nil
}

func drain(t *testing.T, p core.Provider, req core.Request) {
	t.Helper()
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
}

func done() evt.Signal { return core.Done{StopReason: "stop"} }

func assistant(content, reasoning string) core.Message {
	return core.Message{Role: core.RoleAssistant, Content: content, Reasoning: reasoning}
}

// the session that failed: DeepSeek's terse reasoning sent to Qwen after a switch, Qwen copying it into an empty turn
func TestAnotherModelsReasoningIsNotSent(t *testing.T) {
	inner := &scripted{turns: [][]evt.Signal{{done()}}}
	p := lineage.Decorator(inner, lineage.NewOwn())
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "not much rn"},
		assistant("Cool, same.", "The user is just chatting. No task. Keep it brief."),
		{Role: core.RoleUser, Content: "oh you know, life"},
	}
	drain(t, p, core.Request{Messages: msgs})
	sent := inner.got[0].Messages
	if sent[1].Reasoning != "" || sent[1].Content != "Cool, same." {
		t.Fatalf("another model's turn must keep its text and lose its reasoning: %+v", sent[1])
	}
	if msgs[1].Reasoning == "" {
		t.Fatal("the session's own message must not be changed: only the request's copy")
	}
}

func TestItsOwnReasoningIsSent(t *testing.T) {
	inner := &scripted{turns: [][]evt.Signal{
		{core.ReasoningDelta{Text: "think "}, core.ReasoningDelta{Text: "hard"}, core.TextDelta{Text: "hi"}, done()},
		{done()},
	}}
	p := lineage.Decorator(inner, lineage.NewOwn())
	drain(t, p, core.Request{Messages: []core.Message{{Role: core.RoleUser, Content: "q"}}})
	drain(t, p, core.Request{Messages: []core.Message{
		{Role: core.RoleUser, Content: "q"}, assistant("hi", "think hard"), {Role: core.RoleUser, Content: "q2"},
	}})
	if got := inner.got[1].Messages[1].Reasoning; got != "think hard" {
		t.Fatalf("the model's own reasoning (its deltas joined) must be sent back: %q", got)
	}
}

// the empty-turn guard resamples inside one stream: the loop's message joins the discarded attempt's reasoning and
// the kept one's, and so must the model's own record
func TestReasoningAcrossAnEmptyTurnIsItsOwn(t *testing.T) {
	inner := &scripted{turns: [][]evt.Signal{
		{core.ReasoningDelta{Text: "a"}, core.EmptyTurn{Resample: 1, Limit: 2}, core.ReasoningDelta{Text: "b"}, core.TextDelta{Text: "x"}, done()},
		{done()},
	}}
	p := lineage.Decorator(inner, lineage.NewOwn())
	drain(t, p, core.Request{})
	drain(t, p, core.Request{Messages: []core.Message{assistant("x", "ab")}})
	if got := inner.got[1].Messages[0].Reasoning; got != "ab" {
		t.Fatalf("reasoning across an empty turn's resample is the model's own: %q", got)
	}
}

func TestSeededReasoningIsOwn(t *testing.T) { // a resumed session: the trailing turns of its model
	inner := &scripted{turns: [][]evt.Signal{{done()}}}
	p := lineage.Decorator(inner, lineage.NewOwn("from the store"))
	drain(t, p, core.Request{Messages: []core.Message{assistant("a", "from the store"), assistant("b", "not ours")}})
	sent := inner.got[0].Messages
	if sent[0].Reasoning != "from the store" || sent[1].Reasoning != "" {
		t.Fatalf("seeded reasoning kept, the rest dropped: %+v", sent)
	}
}

func TestOwnIsSharedByEveryDecoratorOfTheRow(t *testing.T) { // a rebuilt pair (a role switch) keeps the row's record
	own := lineage.NewOwn()
	inner := &scripted{turns: [][]evt.Signal{{core.ReasoningDelta{Text: "mine"}, done()}, {done()}}}
	drain(t, lineage.Decorator(inner, own), core.Request{})
	drain(t, lineage.Decorator(inner, own), core.Request{Messages: []core.Message{assistant("", "mine")}})
	if inner.got[1].Messages[0].Reasoning != "mine" {
		t.Fatal("a second decorator over the same record must send the row's own reasoning")
	}
}

// signed reasoning (Anthropic's, OpenRouter's) must go back as it came: it is left alone
func TestReasoningDetailsAreLeftAlone(t *testing.T) {
	inner := &scripted{turns: [][]evt.Signal{{done()}}}
	p := lineage.Decorator(inner, lineage.NewOwn())
	m := assistant("a", "signed thinking")
	m.ReasoningDetails = json.RawMessage(`[{"type":"reasoning.encrypted","data":"x"}]`)
	drain(t, p, core.Request{Messages: []core.Message{m}})
	if !reflect.DeepEqual(inner.got[0].Messages[0], m) {
		t.Fatalf("a message with reasoning details must be sent unchanged: %+v", inner.got[0].Messages[0])
	}
}
