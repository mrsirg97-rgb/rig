package command_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
)

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
