package command_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

var errStoreFault = errors.New("state: session s1: close: the store said no")

func allByName(t *testing.T) map[string]core.Command {
	t.Helper()
	out := map[string]core.Command{}
	for _, c := range command.All() {
		if _, dup := out[c.Name()]; dup {
			t.Fatalf("the standard set must not carry a duplicate name: %s", c.Name())
		}
		out[c.Name()] = c
	}
	return out
}

func TestAllIsTheStandardSet(t *testing.T) {
	byName := allByName(t)
	want := []string{"approve", "compact", "effort", "models", "new", "plugins", "role", "scheduler", "sessions", "steer", "todo"}
	if len(byName) != len(want) {
		t.Fatalf("the standard set has %d commands, want %d: %v", len(byName), len(want), names(byName))
	}
	for _, name := range want {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("the standard set is missing %q", name)
		}
		if c.Description() == "" {
			t.Fatalf("%q must carry a description (the TUI owns the discoverable surface)", name)
		}
	}
}

func names(m map[string]core.Command) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	return out
}

func TestCommandEnvRefusedLoud(t *testing.T) {
	byName := allByName(t)
	_, err := byName["compact"].Run(context.Background(), "", struct{ Foreign int }{})
	if err == nil || !strings.Contains(err.Error(), "*command.Env") {
		t.Fatalf("a foreign env must be refused loud naming *command.Env, got %v", err)
	}
	_, err = byName["steer"].Run(context.Background(), "", nil)
	if err == nil || !strings.Contains(err.Error(), "*command.Env") {
		t.Fatalf("a nil env must be refused loud naming *command.Env, got %v", err)
	}
}

type fakeSteer struct {
	mu         sync.Mutex
	slot       string
	hasSlot    bool
	live       bool
	steered    []string
	interrupts int
	cleared    int
}

func (f *fakeSteer) Steer(text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slot = text
	f.hasSlot = true
	f.steered = append(f.steered, text)
	return f.live
}

func (f *fakeSteer) Interrupt() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interrupts++
	return f.live
}

func (f *fakeSteer) ClearSlot() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slot = ""
	f.hasSlot = false
	f.cleared++
}

func (f *fakeSteer) LiveTurn() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live
}

func (f *fakeSteer) slotHeld() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hasSlot
}
