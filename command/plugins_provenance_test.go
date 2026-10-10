package command_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/core"
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
	want := "2 pending plugins\n" +
		"  echo   [ ] the fixture forge plugin · " + zone + "/echo.py\n" +
		"  nodesc [ ] (no DESCRIPTION) · " + zone + "/nodesc.py"
	if out != want {
		t.Fatalf("the rendering = %q, want %q", out, want)
	}
}

type namedTool struct{ name string }

func (n namedTool) Name() string                                                   { return n.name }
func (n namedTool) Description() string                                            { return "named" }
func (n namedTool) Schema() json.RawMessage                                        { return json.RawMessage(`{"type":"object"}`) }
func (n namedTool) Exec(ctx context.Context, args json.RawMessage) (string, error) { return "", nil }

func TestPluginsApproveInstalledReplaces(t *testing.T) {
	home := homeWithZone(t, map[string]string{"echo.py": goodPending}, "echo.py")
	pluginsDir := filepath.Join(home, "plugins")
	src := filepath.Join(pluginsDir, "pending", "echo.py")
	dst := filepath.Join(pluginsDir, "echo.py")
	out, err := pluginsCmd(t).Run(context.Background(), "approve echo", &command.Env{
		Plugins:    func() []command.PluginInfo { return nil },
		PluginsDir: pluginsDir,
		Tools:      map[string]core.Tool{"bash": namedTool{name: "bash"}},
	})
	if err != nil {
		t.Fatalf("an approve over an installed name is an update, got %v", err)
	}
	if !strings.Contains(out, "replacing the installed one") || !strings.Contains(out, src) || !strings.Contains(out, dst) {
		t.Fatalf("the reply must name the replacement and both sides, got %q", out)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != goodPending {
		t.Fatalf("the installed file must carry the pending source: %v %q", err, string(got))
	}
	if _, statErr := os.Stat(src); !os.IsNotExist(statErr) {
		t.Fatalf("the pending file must be gone after the replace (stat: %v)", statErr)
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
