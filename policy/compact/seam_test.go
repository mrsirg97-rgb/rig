package compact_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	compact "github.com/mrsirg97-rgb/rig/v2/policy/compact"
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
	if len(evs) != 2 {
		t.Fatalf("the action emits the cue and the summarizing phase (the caller delivers Compacted), got %v", evs)
	}
	if _, ok := evs[0].(core.Compacting); !ok {
		t.Fatalf("event 0 = %T, want the Compacting cue", evs[0])
	}
	if p, ok := evs[1].(core.Phase); !ok || p.Name != "summarizing" {
		t.Fatalf("event 1 = %+v, want the summarizing phase", evs[1])
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
