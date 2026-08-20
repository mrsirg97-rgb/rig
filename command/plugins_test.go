package command_test

import (
	"context"
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
