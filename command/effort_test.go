package command_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
)

// TestEffortBareShowsActiveAndAvailable (SPEC_MODES, named): the pinned
// reply — the active level and the row's available ones, in the row's
// order.
func TestEffortBareShowsActiveAndAvailable(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Effort:      func() string { return "xhigh" },
		Efforts:     func() []string { return []string{"low", "medium", "xhigh"} },
		ActiveModel: func() string { return "huihui3.8" },
	}
	out, err := byName["effort"].Run(context.Background(), "", env)
	if err != nil || out != "effort: xhigh (available: low, medium, xhigh)" {
		t.Fatalf("the bare reply = (%q, %v), want the pinned voice", out, err)
	}
}

// TestEffortBareServerDefault (SPEC_MODES, named): with the dial unset
// the active level reads "server default" — today's bytes — beside the
// available list.
func TestEffortBareServerDefault(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Effort:      func() string { return "" },
		Efforts:     func() []string { return []string{"low", "medium", "xhigh"} },
		ActiveModel: func() string { return "huihui3.8" },
	}
	out, err := byName["effort"].Run(context.Background(), "", env)
	if err != nil || out != "effort: server default (available: low, medium, xhigh)" {
		t.Fatalf("the unset dial's bare reply = (%q, %v), want the pinned voice", out, err)
	}
}

// TestEffortBareNoLevelsShowsActiveOnly (SPEC_MODES, named): a row that
// names no levels — the dial is off — still shows on bare, without an
// available clause.
func TestEffortBareNoLevelsShowsActiveOnly(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Effort:      func() string { return "" },
		Efforts:     func() []string { return nil },
		ActiveModel: func() string { return "huihui3.8" },
	}
	out, err := byName["effort"].Run(context.Background(), "", env)
	if err != nil || out != "effort: server default" {
		t.Fatalf("the dial-off bare reply = (%q, %v), want the active alone", out, err)
	}
}

// TestEffortSetCallsTheSeam (SPEC_MODES, named): with an argument the
// command sets the dial and replies the pinned "(next turn)" — a dial,
// not a per-turn syntax, so the reply names the turn it reaches.
func TestEffortSetCallsTheSeam(t *testing.T) {
	byName := allByName(t)
	set := ""
	env := &command.Env{
		Efforts:     func() []string { return []string{"low", "medium", "xhigh"} },
		ActiveModel: func() string { return "huihui3.8" },
		SetEffort: func(ctx context.Context, level string) error {
			set = level
			return nil
		},
	}
	out, err := byName["effort"].Run(context.Background(), "xhigh", env)
	if err != nil || out != "effort: xhigh (next turn)" || set != "xhigh" {
		t.Fatalf("the set = (%q, %v) with seam %q, want the pinned reply and the set", out, err, set)
	}
}

// TestEffortUnknownLevelRefusesNamingList (SPEC_MODES, named): a level
// outside the row's list refuses, naming the level, the model, and the
// list — the pinned voice.
func TestEffortUnknownLevelRefusesNamingList(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Efforts:     func() []string { return []string{"low", "medium", "xhigh"} },
		ActiveModel: func() string { return "huihui3.8" },
	}
	out, err := byName["effort"].Run(context.Background(), "turbo", env)
	if err == nil || out != "" || err.Error() != `effort: "turbo" is not a level for huihui3.8 (available: low, medium, xhigh)` {
		t.Fatalf("the unknown-level refusal = (%q, %v), want the pinned voice", out, err)
	}
}

// TestEffortNoLevelsRefusesNamingKey (SPEC_MODES, named): a row without
// efforts refuses an argument, naming the key — the dial is off for that
// row.
func TestEffortNoLevelsRefusesNamingKey(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Efforts:     func() []string { return nil },
		ActiveModel: func() string { return "huihui3.8" },
	}
	out, err := byName["effort"].Run(context.Background(), "low", env)
	if err == nil || out != "" || err.Error() != `effort: huihui3.8 names no levels (models.json: "efforts")` {
		t.Fatalf("the dial-off refusal = (%q, %v), want the pinned voice", out, err)
	}
}

// TestEffortSubCarriesLevelsInRowOrder (SPEC_MODES, named): the menu
// hints carry the row's levels, in the row's order, each described with
// a generic one-liner (the words are the model's).
func TestEffortSubCarriesLevelsInRowOrder(t *testing.T) {
	cmds := command.All()
	env := &command.Env{
		Efforts: func() []string { return []string{"low", "medium", "xhigh"} },
	}
	command.EffortHints(cmds, env)
	var got []command.Sub
	for _, c := range cmds {
		if c.Name() == "effort" {
			got = c.(command.Subber).Sub()
		}
	}
	wantNames := []string{"low", "medium", "xhigh"}
	if len(got) != len(wantNames) {
		t.Fatalf("Sub() = %d hints, want %d", len(got), len(wantNames))
	}
	for i, s := range got {
		if s.Name != wantNames[i] {
			t.Fatalf("Sub() %d = %q, want the row's order %q", i, s.Name, wantNames[i])
		}
		if s.Desc == "" {
			t.Fatalf("Sub() %d (%s) must carry a description", i, s.Name)
		}
	}
}
