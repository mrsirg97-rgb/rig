package command_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/command"
)

func TestThemeBareShowsTheDial(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Theme:    func() string { return "cool" },
		SetTheme: func(ctx context.Context, name string) error { return nil },
	}
	out, err := byName["theme"].Run(context.Background(), "", env)
	if err != nil || out != "theme: cool" {
		t.Fatalf("the bare reply = (%q, %v), want the pinned voice", out, err)
	}
}

func TestThemeBareUnsetReadsTheDefault(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Theme:    func() string { return "" },
		SetTheme: func(ctx context.Context, name string) error { return nil },
	}
	out, err := byName["theme"].Run(context.Background(), "", env)
	if err != nil || out != "theme: warm (default)" {
		t.Fatalf("the unset dial's bare reply = (%q, %v), want the pinned voice", out, err)
	}
}

func TestThemeBareCustomNamesTheFile(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Theme:    func() string { return "custom" },
		SetTheme: func(ctx context.Context, name string) error { return nil },
	}
	out, err := byName["theme"].Run(context.Background(), "", env)
	if err != nil || out != "theme: custom (theme.json)" {
		t.Fatalf("the custom dial's bare reply = (%q, %v), want the pinned voice", out, err)
	}
}

func TestThemeSetPassesThePresetThrough(t *testing.T) {
	byName := allByName(t)
	for _, preset := range []string{"warm", "cool", "custom"} {
		var got string
		env := &command.Env{
			Theme: func() string { return "" },
			SetTheme: func(ctx context.Context, name string) error {
				got = name
				return nil
			},
		}
		out, err := byName["theme"].Run(context.Background(), preset, env)
		if err != nil || out != "theme: "+preset {
			t.Fatalf("the %s reply = (%q, %v), want the pinned voice", preset, out, err)
		}
		if got != preset {
			t.Fatalf("SetTheme = %q, want the preset verbatim", got)
		}
	}
}

func TestThemeUnknownPresetRefuses(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Theme:    func() string { return "" },
		SetTheme: func(ctx context.Context, name string) error { return nil },
	}
	_, err := byName["theme"].Run(context.Background(), "dark", env)
	if err == nil || err.Error() != `theme: "dark" is not a preset (warm, cool, custom)` {
		t.Fatalf("the unknown preset = %v, want the named refusal", err)
	}
}

func TestThemeTwoFieldsRefuse(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Theme:    func() string { return "" },
		SetTheme: func(ctx context.Context, name string) error { return nil },
	}
	_, err := byName["theme"].Run(context.Background(), "warm extra", env)
	if err == nil || err.Error() != "theme: usage: theme [<preset>]" {
		t.Fatalf("the two-field line = %v, want the usage refusal", err)
	}
}

func TestThemeNoSeamRefuses(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{}
	_, err := byName["theme"].Run(context.Background(), "", env)
	if err == nil || err.Error() != "theme: no theme seam (the root did not wire one)" {
		t.Fatalf("the no-seam read = %v, want the named refusal", err)
	}
	_, err = byName["theme"].Run(context.Background(), "cool", env)
	if err == nil || err.Error() != "theme: no theme seam (the root did not wire one)" {
		t.Fatalf("the no-seam write = %v, want the named refusal", err)
	}
}

func TestThemeForeignEnvRefuses(t *testing.T) {
	byName := allByName(t)
	_, err := byName["theme"].Run(context.Background(), "", "not an env")
	if err == nil || !strings.HasPrefix(err.Error(), "command: env is *command.Env") {
		t.Fatalf("the foreign env = %v, want the wiring refusal", err)
	}
}

func TestThemeSubsAreTheThreePresets(t *testing.T) {
	byName := allByName(t)
	c, ok := byName["theme"].(command.Subber)
	if !ok {
		t.Fatal("theme must be a Subber (the TUI's argument-hints door)")
	}
	subs := c.Sub()
	var names []string
	for _, s := range subs {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "warm,cool,custom" {
		t.Fatalf("the hints = %v, want warm, cool, custom in order", names)
	}
	for _, s := range subs {
		if s.Desc == "" {
			t.Fatalf("the hint for %s carries no description", s.Name)
		}
	}
}
