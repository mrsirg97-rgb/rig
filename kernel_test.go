package rig

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
)

// stubCmd is a command-shaped stand-in for the registration tests.
type stubCmd struct{ name string }

func (s stubCmd) Name() string        { return s.name }
func (s stubCmd) Description() string { return "a test command" }
func (s stubCmd) Run(ctx context.Context, args string, env any) (string, error) {
	return s.name + ": " + args, nil
}

func TestWithCommandsRegistersTheRegistry(t *testing.T) {
	k := New(WithCommands(stubCmd{name: "alpha"}, stubCmd{name: "beta"}))
	if len(k.Commands) != 2 {
		t.Fatalf("kernel must carry the registered commands, got %d", len(k.Commands))
	}
	if k.Commands[0].Name() != "alpha" || k.Commands[1].Name() != "beta" {
		t.Fatalf("registration order must be kept: %v", namesOf(k.Commands))
	}
}

func TestWithCommandsDuplicateNamePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("a duplicate command name must panic at startup, loud and early (the tools' precedent)")
		} else if !strings.Contains(r.(string), "duplicate command name") {
			t.Fatalf("the panic must name the duplication: %v", r)
		}
	}()
	New(WithCommands(stubCmd{name: "dup"}, stubCmd{name: "other"}, stubCmd{name: "dup"}))
}

// TestKernelWithoutCommandsIsUnchanged (SPEC_COMMANDS 10): a kernel built
// without WithCommands carries an empty registry — the loop does not read
// it, and nothing in the kernel's shape changes.
func TestKernelWithoutCommandsIsUnchanged(t *testing.T) {
	k := New()
	if k.Commands != nil {
		t.Fatalf("the registry must be empty (nil) when unregistered, got %v", k.Commands)
	}
}

func namesOf(cmds []core.Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.Name())
	}
	return out
}

// The stub's Run must take the seam's shape verbatim (the spec's
// interface, decision 2): env is any, the reply is a string, the refusal
// an error.
var _ core.Command = stubCmd{}

func TestCommandSeamShape(t *testing.T) {
	var c core.Command = stubCmd{name: "x"}
	out, err := c.Run(context.Background(), "args", struct{}{})
	if err != nil || out != "x: args" {
		t.Fatalf("the seam must carry (ctx, args, env any) and return (string, error): %q %v", out, err)
	}
	if _, err := c.Run(context.Background(), "args", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("the env stays untyped at the seam: %v", err)
	}
}
