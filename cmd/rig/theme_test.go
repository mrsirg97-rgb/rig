package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/config"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/frontend/tui"
)

type themeRepaintFrontend struct {
	nullFrontend
	mu  sync.Mutex
	got tui.Theme
}

func (f *themeRepaintFrontend) RepaintTheme(th tui.Theme) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = th
}

func (f *themeRepaintFrontend) repainted() tui.Theme {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.got
}

func themeRoot(t *testing.T, fe core.Frontend) *root {
	t.Helper()
	r := testRoot(fe)
	r.rigHome = t.TempDir()
	r.themeTrueColor = true
	return r
}

func TestSwitchThemePersistsAndRepaints(t *testing.T) {
	fe := &themeRepaintFrontend{}
	r := themeRoot(t, fe)
	if err := r.switchTheme(context.Background(), "cool"); err != nil {
		t.Fatalf("switchTheme(cool): %v", err)
	}
	cfg, err := config.Load(r.rigHome, r.cwd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.Theme != "cool" {
		t.Fatalf("settings.json theme = %q, want cool (the choice is persistent)", cfg.Settings.Theme)
	}
	if r.theme != "cool" {
		t.Fatalf("r.theme = %q, want cool", r.theme)
	}
	if fe.repainted().Name() != "cool" {
		t.Fatalf("the frontend was not repainted with cool")
	}
	if fe.repainted().Slot("accent") != "#8a9bbd" {
		t.Fatalf("the repainted palette = %s, want cool's accent", fe.repainted().Slot("accent"))
	}
}

func TestSwitchThemeWarmIsTheDefaultPalette(t *testing.T) {
	fe := &themeRepaintFrontend{}
	r := themeRoot(t, fe)
	if err := r.switchTheme(context.Background(), "warm"); err != nil {
		t.Fatalf("switchTheme(warm): %v", err)
	}
	if got := fe.repainted().Slot("text"); got != "#d8d8d8" {
		t.Fatalf("warm's text = %q, want the default palette", got)
	}
}

func TestSwitchThemeUnknownPresetRefuses(t *testing.T) {
	r := themeRoot(t, &themeRepaintFrontend{})
	err := r.switchTheme(context.Background(), "dark")
	if err == nil || err.Error() != `theme: "dark" is not a preset (warm, cool, custom)` {
		t.Fatalf("the unknown preset = %v, want the named refusal", err)
	}
}

func TestSwitchThemeCustomWithoutTheFileRefusesLoud(t *testing.T) {
	r := themeRoot(t, &themeRepaintFrontend{})
	err := r.switchTheme(context.Background(), "custom")
	want := "theme: no theme.json in the rig home (" + filepath.Join(r.rigHome, "theme.json") + ")"
	if err == nil || err.Error() != want {
		t.Fatalf("custom without the file = %v, want %q", err, want)
	}
}

func TestSwitchThemeCustomLoadsTheFile(t *testing.T) {
	fe := &themeRepaintFrontend{}
	r := themeRoot(t, fe)
	if err := os.WriteFile(filepath.Join(r.rigHome, "theme.json"), []byte(`{"base": "oled", "slots": {"accent": "#ff9e64"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.switchTheme(context.Background(), "custom"); err != nil {
		t.Fatalf("switchTheme(custom): %v", err)
	}
	if fe.repainted().Slot("accent") != "#ff9e64" {
		t.Fatalf("the repainted accent = %s, want the file's override", fe.repainted().Slot("accent"))
	}
	cfg, err := config.Load(r.rigHome, r.cwd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.Theme != "custom" {
		t.Fatalf("settings.json theme = %q, want custom", cfg.Settings.Theme)
	}
}

func TestSwitchThemeCustomWithAMalformedFileRefusesByName(t *testing.T) {
	r := themeRoot(t, &themeRepaintFrontend{})
	if err := os.WriteFile(filepath.Join(r.rigHome, "theme.json"), []byte(`{"base": oled}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := r.switchTheme(context.Background(), "custom")
	if err == nil || !strings.Contains(err.Error(), "config: ") {
		t.Fatalf("the malformed file = %v, want the config voice naming the file", err)
	}
}

func TestThemeReadNameMintsFromTheDialAndTheFile(t *testing.T) {
	doc := json.RawMessage(`{"base": "cool"}`)
	cases := []struct {
		key  string
		doc  json.RawMessage
		want string
	}{
		{"", nil, ""},
		{"", doc, "custom"},
		{"cool", nil, "cool"},
		{"cool", doc, "cool"},
		{"custom", doc, "custom"},
	}
	for _, c := range cases {
		if got := themeName(c.key, c.doc); got != c.want {
			t.Errorf("themeName(%q with file=%v) = %q, want %q", c.key, c.doc != nil, got, c.want)
		}
	}
}

func TestSwitchThemeWithTheFileOnDiskReadsTheDial(t *testing.T) {
	fe := &themeRepaintFrontend{}
	r := themeRoot(t, fe)
	if err := os.WriteFile(filepath.Join(r.rigHome, "theme.json"), []byte(`{"base": "oled", "slots": {"accent": "#ff9e64"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := config.ReadTheme(r.rigHome)
	if err != nil {
		t.Fatal(err)
	}
	r.theme = themeName("", doc)
	if r.theme != "custom" {
		t.Fatalf("the minted read = %q, want custom (the file paints when no key is set)", r.theme)
	}
	if err := r.switchTheme(context.Background(), "cool"); err != nil {
		t.Fatalf("switchTheme(cool): %v", err)
	}
	if env := r.commandEnv(); env.Theme() != "cool" {
		t.Fatalf("the bare read = %q, want cool (the dial beats the file)", env.Theme())
	}
}
