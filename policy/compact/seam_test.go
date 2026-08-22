package compact_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
	compact "github.com/mrsirg97-rgb/rig/policy/compact"
)

func TestPolicyCompactSeamForcesBelowTrigger(t *testing.T) {

	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 800)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400)})
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 800)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400)})

	summary := []core.Event{core.TextDelta{Text: "SUM"}, core.Done{Usage: core.Usage{Prompt: 812, Completion: 640}}}
	prov := &scriptedProvider{turns: []scriptedTurn{{events: summary}}}
	fe := &captureFrontend{}
	pol, err := compact.New(prov, fe, s, "S", testRow)
	if err != nil {
		t.Fatal(err)
	}

	ev, compacted, err := pol.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !compacted {
		t.Fatal("the forced seam must compact a transcript with an older prefix")
	}

	if len(s.Messages) == 0 || s.Messages[0].Role != core.RoleUser || !strings.HasPrefix(s.Messages[0].Content, "[compaction] ") {
		t.Fatalf("the transcript must be rewritten to [summary] + tail: %+v", s.Messages)
	}
	if !strings.Contains(s.Messages[0].Content, "SUM") {
		t.Fatalf("the summary must carry the model's text: %+v", s.Messages)
	}

	if ev.Summary == "" || ev.Usage.Prompt != 812 {
		t.Fatalf("the event must carry the summary and the usage: %+v", ev)
	}

	evs := fe.snapshot()
	if len(evs) != 1 {
		t.Fatalf("the action emits exactly the cue (the caller delivers Compacted), got %v", evs)
	}
	if _, ok := evs[0].(core.Compacting); !ok {
		t.Fatalf("event 0 = %T, want the Compacting cue", evs[0])
	}
	if prov.calls() != 1 {
		t.Fatalf("exactly one summary call, got %d", prov.calls())
	}
}

func TestPolicyCompactNothingToDrop(t *testing.T) {
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: "one small message"})
	prov := &scriptedProvider{}
	pol, err := compact.New(prov, &captureFrontend{}, s, "S", testRow)
	if err != nil {
		t.Fatal(err)
	}
	before := len(s.Messages)
	ev, compacted, err := pol.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if compacted {
		t.Fatalf("a single message has no older prefix: got %+v", ev)
	}
	if len(s.Messages) != before {
		t.Fatalf("the transcript must be untouched: %d -> %d", before, len(s.Messages))
	}
	if prov.calls() != 0 {
		t.Fatalf("nothing to drop must make no summary call, got %d", prov.calls())
	}
}

func TestPolicyCompactSpendsTheBudget(t *testing.T) {

	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 800)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400)})

	summary := []core.Event{core.TextDelta{Text: "SUM"}, core.Done{}}
	fault := []core.Event{core.Fault{Err: errors.New("prompt is too long: context length exceeded")}}

	prov := &scriptedProvider{turns: []scriptedTurn{{events: summary}, {events: fault}}}
	pol, err := compact.New(prov, &captureFrontend{}, s, "S", testRow)
	if err != nil {
		t.Fatal(err)
	}
	if _, compacted, err := pol.Compact(context.Background()); err != nil || !compacted {
		t.Fatalf("the forced compact must run: %v", err)
	}

	provider := compact.Decorator(prov, pol)
	msgs, err := pol.Assemble(context.Background(), s)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	ch, err := provider.Stream(context.Background(), core.Request{Messages: msgs})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var events []core.Event
	for ev := range ch {
		events = append(events, ev)
	}
	var sawFault bool
	for _, ev := range events {
		if f, ok := ev.(core.Fault); ok {
			sawFault = true
			if !strings.Contains(f.Err.Error(), "context length") {
				t.Fatalf("the original fault must surface, got %v", f.Err)
			}
		}
	}
	if !sawFault {
		t.Fatalf("the context-length fault must surface (no recovery), got %v", events)
	}

	if prov.calls() != 2 {
		t.Fatalf("exactly the forced summary call plus the faulted main call, got %d calls", prov.calls())
	}
}

func TestPolicyCompactSummaryInputDoesNotFit(t *testing.T) {
	small := models.Model{Role: models.RoleInteractive, ID: "local", Window: 300, MaxTokens: 100, Reserve: 50, KeepRecent: 50}
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 2000)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 200)})
	prov := &scriptedProvider{}
	pol, err := compact.New(prov, &captureFrontend{}, s, "S", small)
	if err != nil {
		t.Fatal(err)
	}
	_, compacted, err := pol.Compact(context.Background())
	if compacted {
		t.Fatal("a summary input that does not fit the window must refuse")
	}
	if err == nil || !strings.Contains(err.Error(), "the summary input alone does not fit the window") ||
		!strings.Contains(err.Error(), "window 300") {
		t.Fatalf("the refusal must name the boundary and the numbers, got %v", err)
	}
}
