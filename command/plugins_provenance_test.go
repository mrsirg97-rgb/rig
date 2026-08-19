package command_test

// The provenance verbs (SPEC_SANDBOX 2): /plugins pending lists the
// forge's landing zone with each file's DESCRIPTION so the operator
// reads before blessing; /plugins approve <name> moves one to the top
// level (the operator's verb, Frontend-side by construction). Approval
// never runs from a tool call.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

// homeWithZone builds a scratch rig home with the given pending files
// (name -> body) and, when named, an installed top-level plugin.
func homeWithZone(t *testing.T, pending map[string]string, installed ...string) string {
	t.Helper()
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	if err := os.MkdirAll(filepath.Join(pluginsDir, "pending"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range pending {
		if err := os.WriteFile(filepath.Join(pluginsDir, "pending", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range installed {
		if err := os.WriteFile(filepath.Join(pluginsDir, name), []byte("installed"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

const goodPending = `DESCRIPTION = "the fixture forge plugin"
SCHEMA = {"type": "object"}

def run(args):
    return "forged"
`

// TestPluginsPendingListsTheZoneWithDescriptions (SPEC_SANDBOX,
// named): /plugins pending lists the zone with each file's
// DESCRIPTION, in filename order; a file without one is named as
// absent; non-*.py files and subdirectories are not the zone's files.
func TestPluginsPendingListsTheZoneWithDescriptions(t *testing.T) {
	home := homeWithZone(t, map[string]string{
		"echo.py":   goodPending,
		"nodesc.py": "SCHEMA = {}\ndef run(a):\n    pass\n",
		"readme.md": "not a plugin",
	})
	if err := os.MkdirAll(filepath.Join(home, "plugins", "pending", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	zone := filepath.Join(home, "plugins", "pending")
	out, err := pluginsCmd(t).Run(context.Background(), "pending", &command.Env{
		Plugins:    []command.PluginInfo{},
		PluginsDir: filepath.Join(home, "plugins"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "plugins: 2 pending\n" +
		"  echo: the fixture forge plugin (" + zone + "/echo.py)\n" +
		"  nodesc: (no DESCRIPTION) (" + zone + "/nodesc.py)\n"
	if out != want {
		t.Fatalf("the rendering = %q, want %q", out, want)
	}
}

// TestPluginsPendingEmptyAndAbsentZone (SPEC_SANDBOX, named companion):
// an empty zone and a zone the home never grew are the same line —
// nothing to bless.
func TestPluginsPendingEmptyAndAbsentZone(t *testing.T) {
	for _, home := range []string{
		func() string {
			h := t.TempDir()
			if err := os.MkdirAll(filepath.Join(h, "plugins", "pending"), 0o755); err != nil {
				t.Fatal(err)
			}
			return h
		}(),
		t.TempDir(), // no zone at all
	} {
		out, err := pluginsCmd(t).Run(context.Background(), "pending", &command.Env{
			Plugins:    []command.PluginInfo{},
			PluginsDir: filepath.Join(home, "plugins"),
		})
		if err != nil || out != "plugins: no pending plugins" {
			t.Fatalf("(out, err) = (%q, %v), want (\"plugins: no pending plugins\", nil)", out, err)
		}
	}
}

// TestPluginsPendingNilSeam: Env without the seam — the no-seam voice,
// as the listing has.
func TestPluginsPendingNilSeam(t *testing.T) {
	_, err := pluginsCmd(t).Run(context.Background(), "pending", &command.Env{})
	if err == nil || !strings.Contains(err.Error(), "no plugins seam") {
		t.Fatalf("a nil seam must refuse with the no-seam voice, got %v", err)
	}
}

// namedTool is a named seam tool for the collision check (the model's
// world is what a plugin name collides with).
type namedTool struct{ name string }

func (n namedTool) Name() string                                                   { return n.name }
func (n namedTool) Description() string                                            { return "named" }
func (n namedTool) Schema() json.RawMessage                                        { return json.RawMessage(`{"type":"object"}`) }
func (n namedTool) Exec(ctx context.Context, args json.RawMessage) (string, error) { return "", nil }

// TestPluginsApproveMovesThePendingFile (SPEC_SANDBOX, named):
// approve moves pending/<name>.py to the top level — the file is
// gone from the zone, the success line names both sides, and a second
// approve of the same name refuses (nothing pending anymore).
func TestPluginsApproveMovesThePendingFile(t *testing.T) {
	home := homeWithZone(t, map[string]string{"forge.py": goodPending})
	pluginsDir := filepath.Join(home, "plugins")
	src := filepath.Join(pluginsDir, "pending", "forge.py")
	dst := filepath.Join(pluginsDir, "forge.py")

	out, err := pluginsCmd(t).Run(context.Background(), "approve forge", &command.Env{
		Plugins:    []command.PluginInfo{},
		PluginsDir: pluginsDir,
		Tools:      map[string]core.Tool{"bash": namedTool{name: "bash"}},
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, statErr := os.Stat(dst); statErr != nil {
		t.Fatalf("the approved plugin must land at the top level: %v", statErr)
	}
	if _, statErr := os.Stat(src); !os.IsNotExist(statErr) {
		t.Fatalf("the pending file must be gone after the move (stat: %v)", statErr)
	}
	if !strings.Contains(out, src) || !strings.Contains(out, dst) {
		t.Fatalf("the success line must name both sides, got %q", out)
	}

	_, err = pluginsCmd(t).Run(context.Background(), "approve forge", &command.Env{
		Plugins:    []command.PluginInfo{},
		PluginsDir: pluginsDir,
		Tools:      map[string]core.Tool{"bash": namedTool{name: "bash"}},
	})
	if err == nil || !strings.Contains(err.Error(), "no pending plugin") {
		t.Fatalf("a second approve must refuse, naming the absent pending file, got %v", err)
	}
}

// TestPluginsApproveNativeCollisionRefuses (SPEC_SANDBOX, named):
// approve of a name that collides with a native refuses — the
// existing rule at the new door, the same voice the startup
// collision carries, and nothing moves.
func TestPluginsApproveNativeCollisionRefuses(t *testing.T) {
	home := homeWithZone(t, map[string]string{"bash.py": goodPending})
	pluginsDir := filepath.Join(home, "plugins")
	_, err := pluginsCmd(t).Run(context.Background(), "approve bash", &command.Env{
		Plugins:    []command.PluginInfo{},
		PluginsDir: pluginsDir,
		Tools:      map[string]core.Tool{"bash": namedTool{name: "bash"}},
	})
	if err == nil {
		t.Fatal("a native-name collision must refuse")
	}
	want := `plugins: name collision: "bash" (bash.py) is already a native tool`
	if err.Error() != want {
		t.Fatalf("the refusal must be the existing rule's voice, got %q, want %q", err.Error(), want)
	}
	if _, statErr := os.Stat(filepath.Join(pluginsDir, "pending", "bash.py")); statErr != nil {
		t.Fatalf("a refused approve must leave the pending file in place: %v", statErr)
	}
}

// TestPluginsApproveInstalledCollisionRefuses: a top-level file of the
// same name is already installed — the move would clobber it, and a
// clobber is not an operator's verb by accident: refuse, naming the
// installed file.
func TestPluginsApproveInstalledCollisionRefuses(t *testing.T) {
	home := homeWithZone(t, map[string]string{"echo.py": goodPending}, "echo.py")
	pluginsDir := filepath.Join(home, "plugins")
	_, err := pluginsCmd(t).Run(context.Background(), "approve echo", &command.Env{
		Plugins:    []command.PluginInfo{},
		PluginsDir: pluginsDir,
		Tools:      map[string]core.Tool{"bash": namedTool{name: "bash"}},
	})
	if err == nil {
		t.Fatal("an installed collision must refuse")
	}
	if !strings.Contains(err.Error(), "already installed") || !strings.Contains(err.Error(), filepath.Join(pluginsDir, "echo.py")) {
		t.Fatalf("the refusal must name the installed file, got %v", err)
	}
}

// TestPluginsApproveBadNames: the name is the filename stem — a missing
// name is usage, an extra argument is usage, and a path is not a name.
func TestPluginsApproveBadNames(t *testing.T) {
	home := homeWithZone(t, map[string]string{"echo.py": goodPending})
	pluginsDir := filepath.Join(home, "plugins")
	env := &command.Env{PluginsDir: pluginsDir}
	for _, args := range []string{"approve", "approve echo extra", "approve ./echo", "approve .."} {
		_, err := pluginsCmd(t).Run(context.Background(), args, env)
		if err == nil {
			t.Fatalf("args %q must refuse", args)
		}
	}
}

// TestPluginsUnknownVerbUsage: a verb the set does not carry refuses
// with the full usage line — the verbs the set does carry are named.
func TestPluginsUnknownVerbUsage(t *testing.T) {
	home := t.TempDir()
	_, err := pluginsCmd(t).Run(context.Background(), "reload", &command.Env{
		Plugins:    []command.PluginInfo{},
		PluginsDir: filepath.Join(home, "plugins"),
	})
	if err == nil {
		t.Fatal("an unknown verb must refuse")
	}
	want := "plugins: usage: plugins | plugins pending | plugins approve <name>"
	if err.Error() != want {
		t.Fatalf("the usage line = %q, want %q", err.Error(), want)
	}
}
