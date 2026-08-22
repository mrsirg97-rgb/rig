package command_test

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
		Plugins:    func() []command.PluginInfo { return nil },
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

func TestPluginsPendingEmptyAndAbsentZone(t *testing.T) {
	for _, home := range []string{
		func() string {
			h := t.TempDir()
			if err := os.MkdirAll(filepath.Join(h, "plugins", "pending"), 0o755); err != nil {
				t.Fatal(err)
			}
			return h
		}(),
		t.TempDir(),
	} {
		out, err := pluginsCmd(t).Run(context.Background(), "pending", &command.Env{
			Plugins:    func() []command.PluginInfo { return nil },
			PluginsDir: filepath.Join(home, "plugins"),
		})
		if err != nil || out != "plugins: no pending plugins" {
			t.Fatalf("(out, err) = (%q, %v), want (\"plugins: no pending plugins\", nil)", out, err)
		}
	}
}

func TestPluginsPendingNilSeam(t *testing.T) {
	_, err := pluginsCmd(t).Run(context.Background(), "pending", &command.Env{})
	if err == nil || !strings.Contains(err.Error(), "no plugins seam") {
		t.Fatalf("a nil seam must refuse with the no-seam voice, got %v", err)
	}
}

type namedTool struct{ name string }

func (n namedTool) Name() string                                                   { return n.name }
func (n namedTool) Description() string                                            { return "named" }
func (n namedTool) Schema() json.RawMessage                                        { return json.RawMessage(`{"type":"object"}`) }
func (n namedTool) Exec(ctx context.Context, args json.RawMessage) (string, error) { return "", nil }

func TestPluginsApproveMovesThePendingFile(t *testing.T) {
	home := homeWithZone(t, map[string]string{"forge.py": goodPending})
	pluginsDir := filepath.Join(home, "plugins")
	src := filepath.Join(pluginsDir, "pending", "forge.py")
	dst := filepath.Join(pluginsDir, "forge.py")

	out, err := pluginsCmd(t).Run(context.Background(), "approve forge", &command.Env{
		Plugins:    func() []command.PluginInfo { return nil },
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
		Plugins:    func() []command.PluginInfo { return nil },
		PluginsDir: pluginsDir,
		Tools:      map[string]core.Tool{"bash": namedTool{name: "bash"}},
	})
	if err == nil || !strings.Contains(err.Error(), "no pending plugin") {
		t.Fatalf("a second approve must refuse, naming the absent pending file, got %v", err)
	}
}

func TestPluginsApproveNativeCollisionRefuses(t *testing.T) {
	home := homeWithZone(t, map[string]string{"bash.py": goodPending})
	pluginsDir := filepath.Join(home, "plugins")
	_, err := pluginsCmd(t).Run(context.Background(), "approve bash", &command.Env{
		Plugins:    func() []command.PluginInfo { return nil },
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

func TestPluginsApproveInstalledCollisionRefuses(t *testing.T) {
	home := homeWithZone(t, map[string]string{"echo.py": goodPending}, "echo.py")
	pluginsDir := filepath.Join(home, "plugins")
	_, err := pluginsCmd(t).Run(context.Background(), "approve echo", &command.Env{
		Plugins:    func() []command.PluginInfo { return nil },
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

func TestPluginsUnknownVerbUsage(t *testing.T) {
	home := t.TempDir()
	_, err := pluginsCmd(t).Run(context.Background(), "frobnicate", &command.Env{
		Plugins:    func() []command.PluginInfo { return nil },
		PluginsDir: filepath.Join(home, "plugins"),
	})
	if err == nil {
		t.Fatal("an unknown verb must refuse")
	}
	want := "plugins: usage: plugins | plugins pending | plugins disabled | plugins approve <name> | plugins reload | plugins create <text> | plugins enable <name> | plugins disable <name>"
	if err.Error() != want {
		t.Fatalf("the usage line = %q, want %q", err.Error(), want)
	}
}
