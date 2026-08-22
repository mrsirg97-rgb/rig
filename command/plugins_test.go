package command_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

// pluginsCmd pulls the /plugins entry out of the standard set (the
// same seam the frontends dispatch).
func pluginsCmd(t *testing.T) core.Command {
	t.Helper()
	for _, c := range command.All() {
		if c.Name() == "plugins" {
			return c
		}
	}
	t.Fatal("the standard set is missing the plugins command")
	return nil
}

// TestPluginsListRendersLoadedAndSkipped (SPEC_PLUGINS, named): Env
// with loaded and skipped rows — the exact rendering: the header's
// counts, the loaded rows' name/description/file, the skipped rows'
// file/reason, in file order.
func TestPluginsListRendersLoadedAndSkipped(t *testing.T) {
	env := &command.Env{Plugins: func() []command.PluginInfo {
		return []command.PluginInfo{
			{Name: "echo", Description: "the fixture echo plugin", File: "/home/u/.rig/plugins/echo.py"},
			{Name: "broken", File: "/home/u/.rig/plugins/broken.py", Skipped: true, Reason: "NameError: name 'x' is not defined"},
			{Name: "missing", File: "/home/u/.rig/plugins/missing.py", Skipped: true, Reason: "missing SCHEMA"},
		}
	}}
	out, err := pluginsCmd(t).Run(context.Background(), "", env)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := `plugins: 1 loaded, 2 skipped
loaded:
  echo: the fixture echo plugin (/home/u/.rig/plugins/echo.py)
skipped:
  broken.py: NameError: name 'x' is not defined
  missing.py: missing SCHEMA
`
	if out != want {
		t.Fatalf("the rendering = %q, want %q", out, want)
	}
}

// TestPluginsNoArgsRefusal (SPEC_PLUGINS, named): args the set does
// not carry — the usage voice.
func TestPluginsNoArgsRefusal(t *testing.T) {
	_, err := pluginsCmd(t).Run(context.Background(), "reload extra", &command.Env{Plugins: func() []command.PluginInfo { return nil }})
	if err == nil || !strings.Contains(err.Error(), "usage: plugins") {
		t.Fatalf("args given must refuse with the usage voice, got %v", err)
	}
}

// TestPluginsNilSeamRefusal (SPEC_PLUGINS, named): Env.Plugins nil —
// the no-seam voice.
func TestPluginsNilSeamRefusal(t *testing.T) {
	_, err := pluginsCmd(t).Run(context.Background(), "", &command.Env{})
	if err == nil || !strings.Contains(err.Error(), "no plugins seam") {
		t.Fatalf("a nil seam must refuse with the no-seam voice, got %v", err)
	}
}

// TestPluginsNone (SPEC_PLUGINS, named): an empty (non-nil) slice —
// plugins: none.
func TestPluginsNone(t *testing.T) {
	out, err := pluginsCmd(t).Run(context.Background(), "", &command.Env{Plugins: func() []command.PluginInfo { return []command.PluginInfo{} }})
	if err != nil || out != "plugins: none" {
		t.Fatalf("(out, err) = (%q, %v), want (\"plugins: none\", nil)", out, err)
	}
}

// SPEC_GROWTH 9 (amended): the switch is a directory. disable moves the
// file into plugins/disabled/ and reloads; enable moves it back; both
// refuse by name when the file is not where the verb expects it, or
// when the destination is taken.
func TestPluginsDisableAndEnableMoveTheFileAndReload(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "echo.py"), []byte("DESCRIPTION = \"echo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	env := &command.Env{PluginsDir: dir, Reload: func(ctx context.Context) (string, error) { reloads++; return "plugins: reload: ok", nil }}
	out, err := pluginsCmd(t).Run(context.Background(), "disable echo", env)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if !strings.Contains(out, "plugins: disabled echo") || !strings.Contains(out, "reload: ok") {
		t.Fatalf("disable reply %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "disabled", "echo.py")); err != nil {
		t.Fatalf("the file must be in plugins/disabled/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "echo.py")); !os.IsNotExist(err) {
		t.Fatal("the file must be gone from plugins/")
	}
	out, err = pluginsCmd(t).Run(context.Background(), "disabled", env)
	if err != nil || !strings.Contains(out, "1 disabled") || !strings.Contains(out, "echo: echo") {
		t.Fatalf("the disabled zone listing: %q %v", out, err)
	}
	if _, err := pluginsCmd(t).Run(context.Background(), "disable echo", env); err == nil || !strings.Contains(err.Error(), "no plugin") {
		t.Fatalf("a second disable must refuse by name, got %v", err)
	}
	out, err = pluginsCmd(t).Run(context.Background(), "enable echo", env)
	if err != nil || !strings.Contains(out, "plugins: enabled echo") {
		t.Fatalf("enable: %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "echo.py")); err != nil {
		t.Fatalf("the file must be back in plugins/: %v", err)
	}
	if reloads != 2 {
		t.Fatalf("each move reloads once, got %d", reloads)
	}
	if _, err := pluginsCmd(t).Run(context.Background(), "enable echo", env); err == nil || !strings.Contains(err.Error(), "no plugin") {
		t.Fatalf("enabling a live plugin must refuse by name, got %v", err)
	}
	if _, err := pluginsCmd(t).Run(context.Background(), "disable ../x", env); err == nil || !strings.Contains(err.Error(), "not a plugin name") {
		t.Fatalf("a path is not a name, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "disabled", "echo.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginsCmd(t).Run(context.Background(), "disable echo", env); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a taken destination must refuse, got %v", err)
	}
}
