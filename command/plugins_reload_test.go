package command_test

// The reload and the forge, command door (SPEC_PLUGINS 8, testing):
// /plugins reload passes the root's reply through (the command is
// sugar over capabilities the model has); /plugins create <text>
// queues the spec's template on the steer precedent (the command
// queues a line; it never dispatches a turn itself); the approve's
// tail is the reload's (SPEC_SANDBOX 2, post-8).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

const createTemplate = "author a plugin: %s; the contract is DESCRIPTION, SCHEMA, run(args) -> str; write it SELF-CONTAINED to the pending directory (SPEC_SANDBOX); call plugins_reload; test it with one call."

func wantUsage() string {
	return "plugins: usage: plugins | plugins pending | plugins approve <name> | plugins reload | plugins create <text>"
}

// TestPluginsReloadVerbPassesTheReplyThrough (SPEC_PLUGINS 8, named):
// the reload seam wired — the verb's reply is the root's verbatim; a
// nil seam refuses with the no-seam voice.
func TestPluginsReloadVerbPassesTheReplyThrough(t *testing.T) {
	reply := "plugins: reload: 1 loaded, 1 skipped\nloaded:\n  echo: the fixture echo plugin (/h/plugins/echo.py)\nskipped:\n  broken.py: NameError: name 'x' is not defined\n"
	calls := 0
	env := &command.Env{
		Plugins:    func() []command.PluginInfo { return nil },
		PluginsDir: t.TempDir(),
		Reload: func(ctx context.Context) (string, error) {
			calls++
			return reply, nil
		},
	}
	out, err := pluginsCmd(t).Run(context.Background(), "reload", env)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if out != reply {
		t.Fatalf("the reply = %q, want the root's verbatim", out)
	}
	if calls != 1 {
		t.Fatalf("the reload's action ran %d times, want 1", calls)
	}

	_, err = pluginsCmd(t).Run(context.Background(), "reload", &command.Env{
		Plugins:    func() []command.PluginInfo { return nil },
		PluginsDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "no reload seam") {
		t.Fatalf("a nil seam must refuse with the no-seam voice, got %v", err)
	}
}

// TestPluginsCreateQueuesTheTemplate (SPEC_PLUGINS 8, named): the
// queued line is 8's template with the operator's text spliced in
// (the steer precedent: the command queues a line and never dispatches
// a turn); the interrupt voice when a live turn was interrupted; a nil
// steer seam refuses; an empty text is usage.
func TestPluginsCreateQueuesTheTemplate(t *testing.T) {
	want := fmt.Sprintf(createTemplate, "an echo plugin")
	steer := &fakeSteer{}
	out, err := pluginsCmd(t).Run(context.Background(), "create an echo plugin", &command.Env{Steer: steer})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if steer.slot != want {
		t.Fatalf("the queued line = %q, want the spec's template with the operator's text spliced in:\n%q", steer.slot, want)
	}
	if out != "plugins: create: queued "+want {
		t.Fatalf("the reply = %q, want the queued line named", out)
	}

	// the interrupt voice (a live turn was interrupted by the queue).
	steer2 := &fakeSteer{live: true}
	out, err = pluginsCmd(t).Run(context.Background(), "create an echo plugin", &command.Env{Steer: steer2})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out != "plugins: create: queued "+want+" · turn interrupted" {
		t.Fatalf("the interrupt voice = %q, want the steer precedent's", out)
	}

	// no steering seam.
	_, err = pluginsCmd(t).Run(context.Background(), "create an echo plugin", &command.Env{})
	if err == nil || !strings.Contains(err.Error(), "no steering seam") {
		t.Fatalf("a nil steer seam must refuse, got %v", err)
	}

	// an empty text is usage.
	_, err = pluginsCmd(t).Run(context.Background(), "create", &command.Env{Steer: &fakeSteer{}})
	if err == nil || err.Error() != wantUsage() {
		t.Fatalf("an empty text must be usage, got %v", err)
	}
}

// TestPluginsUsageNamesTheReloadAndCreateVerbs (SPEC_PLUGINS 8,
// named): the unknown verb's usage line carries the set's whole shape.
func TestPluginsUsageNamesTheReloadAndCreateVerbs(t *testing.T) {
	home := t.TempDir()
	_, err := pluginsCmd(t).Run(context.Background(), "frobnicate", &command.Env{
		Plugins:    func() []command.PluginInfo { return nil },
		PluginsDir: filepath.Join(home, "plugins"),
	})
	if err == nil {
		t.Fatal("an unknown verb must refuse")
	}
	if err.Error() != wantUsage() {
		t.Fatalf("the usage line = %q, want %q", err.Error(), wantUsage())
	}
}

// TestPluginsApproveMovesThenReloads (SPEC_PLUGINS 8, named —
// SPEC_SANDBOX 2 post-8): the move lands and the reply carries the
// move's line plus the reload's reply; a reload failure after the move
// keeps the move (the disk is the truth) and names the failure; a root
// without the reload seam is the move only (the pre-8 voice, intact).
func TestPluginsApproveMovesThenReloads(t *testing.T) {
	t.Run("the move plus the reload's reply", func(t *testing.T) {
		home := homeWithZone(t, map[string]string{"forge.py": goodPending})
		pluginsDir := filepath.Join(home, "plugins")
		src := filepath.Join(pluginsDir, "pending", "forge.py")
		dst := filepath.Join(pluginsDir, "forge.py")
		calls := 0
		out, err := pluginsCmd(t).Run(context.Background(), "approve forge", &command.Env{
			Plugins:    func() []command.PluginInfo { return nil },
			PluginsDir: pluginsDir,
			Tools:      map[string]core.Tool{"bash": namedTool{name: "bash"}},
			Reload: func(ctx context.Context) (string, error) {
				calls++
				return "plugins: reload: 1 loaded, 0 skipped", nil
			},
		})
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		if calls != 1 {
			t.Fatalf("the reload's action ran %d times, want 1", calls)
		}
		if _, statErr := os.Stat(dst); statErr != nil {
			t.Fatalf("the approved plugin must land at the top level: %v", statErr)
		}
		want := "plugins: approved forge (" + src + " -> " + dst + ")\nplugins: reload: 1 loaded, 0 skipped"
		if out != want {
			t.Fatalf("the reply = %q, want the move's line plus the reload's reply:\n%q", out, want)
		}
	})

	t.Run("a reload failure keeps the move", func(t *testing.T) {
		home := homeWithZone(t, map[string]string{"forge.py": goodPending})
		pluginsDir := filepath.Join(home, "plugins")
		_, err := pluginsCmd(t).Run(context.Background(), "approve forge", &command.Env{
			Plugins:    func() []command.PluginInfo { return nil },
			PluginsDir: pluginsDir,
			Tools:      map[string]core.Tool{"bash": namedTool{name: "bash"}},
			Reload: func(ctx context.Context) (string, error) {
				return "", errors.New("discovery: kernel exited (code 1)")
			},
		})
		if err == nil {
			t.Fatal("a reload failure must refuse")
		}
		if !strings.Contains(err.Error(), "plugins: approved forge") {
			t.Fatalf("the move's line must ride the refusal (the disk is the truth), got %v", err)
		}
		if !strings.Contains(err.Error(), "the reload failed: discovery: kernel exited (code 1)") {
			t.Fatalf("the refusal must name the reload's failure, got %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(pluginsDir, "forge.py")); statErr != nil {
			t.Fatalf("the move must stand after a failed reload: %v", statErr)
		}
	})

	t.Run("a pre-8 root is the move only", func(t *testing.T) {
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
		want := "plugins: approved forge (" + src + " -> " + dst + "); the discovery loads it at the next start"
		if out != want {
			t.Fatalf("the pre-8 voice = %q, want the move only:\n%q", out, want)
		}
	})
}
