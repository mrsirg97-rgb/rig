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

// TestPolicyCompactSeamForcesBelowTrigger (SPEC_COMMANDS 3, named): the
// forced seam runs the same internal action as the trigger path — split,
// summarize, rewrite, reflect — below the trigger, where the trigger
// would pass through; it spends the once budget as the trigger path
// does; the caller owns delivery (the action never emits, decision 5).
func TestPolicyCompactSeamForcesBelowTrigger(t *testing.T) {
	// below the trigger: size (no anchor) est(2x1000B = 500) <= 900
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 800)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400)})
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 800)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400)})

	summary := []core.Event{core.TextDelta{Text: "SUM"}, core.Done{Usage: core.Usage{Prompt: 812, Completion: 640}}}
	prov := &scriptedProvider{turns: []scriptedTurn{{events: summary}}}
	fe := &captureFrontend{}
	reflected := 0
	pol, err := compact.New(prov, fe, s, "S", testRow,
		compact.WithAutoReflect(func(ctx context.Context, sum string) error {
			reflected++
			return nil
		}))
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
	// the rewrite: [summary row] + tail
	if len(s.Messages) == 0 || s.Messages[0].Role != core.RoleUser || !strings.HasPrefix(s.Messages[0].Content, "[compaction] ") {
		t.Fatalf("the transcript must be rewritten to [summary] + tail: %+v", s.Messages)
	}
	if !strings.Contains(s.Messages[0].Content, "SUM") {
		t.Fatalf("the summary must carry the model's text: %+v", s.Messages)
	}
	// the event, for the caller to deliver
	if ev.Summary == "" || ev.Usage.Prompt != 812 {
		t.Fatalf("the event must carry the summary and the usage: %+v", ev)
	}
	// the caller owns delivery: the frontend got nothing (decision 5)
	if n := len(fe.snapshot()); n != 0 {
		t.Fatalf("the action must never emit (the caller delivers), got %d events", n)
	}
	if reflected != 1 {
		t.Fatalf("a forced compaction is a compaction: AutoReflect must be called once, got %d", reflected)
	}
	if prov.calls() != 1 {
		t.Fatalf("exactly one summary call, got %d", prov.calls())
	}
}

// TestPolicyCompactNothingToDrop (SPEC_COMMANDS 3): the action's own
// boundary — an empty older prefix is 'nothing to drop', the transcript
// untouched.
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

// TestPolicyCompactSpendsTheBudget (SPEC_COMMANDS 3, named): the forced
// compact spends the once budget as the trigger path does — the next
// context-length fault, with the transcript not grown past the compact's
// key, surfaces without recovery.
func TestPolicyCompactSpendsTheBudget(t *testing.T) {
	// a fixture with an older prefix, under the trigger.
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 800)})
	s.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("a", 400)})

	summary := []core.Event{core.TextDelta{Text: "SUM"}, core.Done{}}
	fault := []core.Event{core.Fault{Err: errors.New("prompt is too long: context length exceeded")}}
	// call order: the forced summary, then the next main call (fault).
	prov := &scriptedProvider{turns: []scriptedTurn{{events: summary}, {events: fault}}}
	pol, err := compact.New(prov, &captureFrontend{}, s, "S", testRow)
	if err != nil {
		t.Fatal(err)
	}
	if _, compacted, err := pol.Compact(context.Background()); err != nil || !compacted {
		t.Fatalf("the forced compact must run: %v", err)
	}

	// the next main call faults with context length; the transcript has
	// not grown past the compact's key — the recovery is not owed.
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
	// one summary call only: the forced one — the budget is spent.
	if prov.calls() != 2 {
		t.Fatalf("exactly the forced summary call plus the faulted main call, got %d calls", prov.calls())
	}
}

// TestPolicyCompactSummaryInputDoesNotFit (SPEC_COMMANDS 3): the
// action's loud refusal, the numbers named.
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
