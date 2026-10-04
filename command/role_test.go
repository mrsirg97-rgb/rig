package command_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/command"
)

func TestRoleBareShowsDefault(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{Role: func() string { return "" }}
	out, err := byName["role"].Run(context.Background(), "", env)
	want := "3 roles · active default\n" +
		"  default   [~] no stance: the plain prompt\n" +
		"  architect design first, build second\n" +
		"  reviewer  review only: hunt defects, build nothing"
	if err != nil || out != want {
		t.Fatalf("the bare reply = (%q, %v), want the roles with default marked", out, err)
	}
}

func TestRoleBareShowsActive(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{Role: func() string { return "architect" }}
	out, err := byName["role"].Run(context.Background(), "", env)
	if err != nil || !strings.Contains(out, "active architect") || !strings.Contains(out, "  architect [~] design first") {
		t.Fatalf("the bare reply = (%q, %v), want architect marked", out, err)
	}
}

func TestRoleSetCallsTheSeam(t *testing.T) {
	byName := allByName(t)
	set := ""
	env := &command.Env{
		Role: func() string { return "architect" },
		SetRole: func(ctx context.Context, name string) error {
			set = name
			return nil
		},
	}
	out, err := byName["role"].Run(context.Background(), "reviewer", env)
	if err != nil || out != "role: reviewer (next turn)" || set != "reviewer" {
		t.Fatalf("the set = (%q, %v) with seam %q, want the pinned reply and the set", out, err, set)
	}
}

func TestRoleUnknownNameRefusesNamingThree(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{Role: func() string { return "default" }}
	out, err := byName["role"].Run(context.Background(), "pirate", env)
	if err == nil || out != "" || err.Error() != `role: "pirate" is not a role (default, architect, reviewer)` {
		t.Fatalf("the unknown-name refusal = (%q, %v), want the pinned voice", out, err)
	}
}

func TestRoleSubCarriesTheThree(t *testing.T) {
	byName := allByName(t)
	subber, ok := byName["role"].(command.Subber)
	if !ok {
		t.Fatal("the role command must implement Sub()")
	}
	got := subber.Sub()
	if len(got) != 3 {
		t.Fatalf("Sub() = %d hints, want the shipped three", len(got))
	}
	wantNames := []string{"default", "architect", "reviewer"}
	for i, s := range got {
		if s.Name != wantNames[i] {
			t.Fatalf("Sub() %d = %q, want %q", i, s.Name, wantNames[i])
		}
		if s.Desc == "" {
			t.Fatalf("Sub() %d (%s) must carry a one-liner", i, s.Name)
		}
	}
}
