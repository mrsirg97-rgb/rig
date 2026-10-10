package command_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/command"
)

const createTemplate = "author a plugin: %s; the contract is DESCRIPTION, SCHEMA, run(args) -> str; write it SELF-CONTAINED to the pending directory (SPEC_SANDBOX); the operator installs it with /plugins approve; then call it through the plugin door and test it with one call."

func wantUsage() string {
	return "plugins: usage: plugins | plugins pending | plugins disabled | plugins approve <name> | plugins reload | plugins create <text> | plugins enable <name> | plugins disable <name>"
}

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

	steer2 := &fakeSteer{live: true}
	out, err = pluginsCmd(t).Run(context.Background(), "create an echo plugin", &command.Env{Steer: steer2})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out != "plugins: create: queued "+want+" · turn interrupted" {
		t.Fatalf("the interrupt voice = %q, want the steer precedent's", out)
	}

	_, err = pluginsCmd(t).Run(context.Background(), "create an echo plugin", &command.Env{})
	if err == nil || !strings.Contains(err.Error(), "no steering seam") {
		t.Fatalf("a nil steer seam must refuse, got %v", err)
	}

	_, err = pluginsCmd(t).Run(context.Background(), "create", &command.Env{Steer: &fakeSteer{}})
	if err == nil || err.Error() != wantUsage() {
		t.Fatalf("an empty text must be usage, got %v", err)
	}
}

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
