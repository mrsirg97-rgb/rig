package state_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/store/state"
)

// a resumed session's own reasoning (SPEC_LINEAGE): the trailing run of assistant turns of the last one's model -
// the turns before the switch another model's, not sent back to this one
func TestTrailingReasoningIsTheLastModelsRun(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	sid := "lineage-switch"
	if err := state.RecordSession(ctx, db, sid, "/w", "qwen3.8-flash-next-1m", "v"); err != nil {
		t.Fatal(err)
	}
	s := func(v string) *string { return &v }
	for _, m := range []struct {
		role, text       string
		reasoning, model *string
	}{
		{"user", "hey", nil, nil},
		{"assistant", "hi", s("ds one"), s("osmos-ud-iq2_xxs")},
		{"user", "chillin", nil, nil},
		{"assistant", "cool", s("ds two"), s("osmos-ud-iq2_xxs")},
		{"user", "hey dude", nil, nil},
		{"assistant", "still here", s("qwen one"), s("osmos-qwen38-ud-iq3_xxs-1m")},
		{"user", "life", nil, nil},
		{"assistant", "", nil, s("osmos-qwen38-ud-iq3_xxs-1m")}, // a tool call's turn: no reasoning
		{"assistant", "ok", s("qwen two"), s("osmos-qwen38-ud-iq3_xxs-1m")},
	} {
		if _, err := state.RecordMessage(ctx, db, sid, m.role, m.text, m.reasoning, nil, m.model); err != nil {
			t.Fatal(err)
		}
	}
	got, err := state.TrailingReasoning(ctx, db, sid, "qwen3.8-flash-next-1m")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"qwen one", "qwen two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("trailing reasoning = %q, want %q", got, want)
	}
	// resumed on another row than the session was last on: the trailing turns are not that row's
	if got, err := state.TrailingReasoning(ctx, db, sid, "deepseek-1017-ud-l"); err != nil || len(got) != 0 {
		t.Fatalf("another row's resume: none (%q, %v)", got, err)
	}
}

func TestTrailingReasoningOfNoTurnsIsNone(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "lineage-none", "/w", "m", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, "lineage-none", "user", "hello", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := state.TrailingReasoning(ctx, db, "lineage-none", "m")
	if err != nil || len(got) != 0 {
		t.Fatalf("no assistant turn: none (%q, %v)", got, err)
	}
}
