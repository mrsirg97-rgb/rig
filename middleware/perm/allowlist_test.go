package perm_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/perm"
)

// countingExec records invocations and answers with a fixed result or error.
type countingExec struct {
	calls   int
	content string
}

func run(t *testing.T, mw core.ToolMiddleware, name string) (calls int, content string, err error) {
	t.Helper()
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		calls++
		return "executed", nil
	}
	exec = mw.Wrap(exec)
	content, err = exec(context.Background(), core.ToolCall{
		ID:   "c1",
		Name: name,
		Args: json.RawMessage(`{}`),
	})
	return calls, content, err
}

func mustExec(t *testing.T, exec core.ToolExec, name string) (string, error) {
	t.Helper()
	return exec(context.Background(), core.ToolCall{
		ID:   "c1",
		Name: name,
		Args: json.RawMessage(`{}`),
	})
}

func TestAllowsListed(t *testing.T) {
	calls, content, err := run(t, perm.Allowlist("bash"), "bash")
	if err != nil {
		t.Fatalf("listed tool denied: %v", err)
	}
	if calls != 1 || content != "executed" {
		t.Fatalf("allowed call must pass through once, got %d calls / %q", calls, content)
	}
}

func TestDeniesByDefault(t *testing.T) {
	calls, content, err := run(t, perm.Allowlist("bash"), "file")
	if calls != 0 {
		t.Fatalf("denied call reached the exec %d times", calls)
	}
	if err == nil {
		t.Fatal("denial must be attributed so downstream guards can bound it")
	}
	if !strings.Contains(content, "file") || !strings.Contains(content, "allow") {
		t.Fatalf("denial must feed back a refusal naming the tool and the allow-list, got %q", content)
	}
}

func TestMultipleNames(t *testing.T) {
	for _, name := range []string{"bash", "read", "write"} {
		calls, _, err := run(t, perm.Allowlist("bash", "read", "write"), name)
		if err != nil || calls != 1 {
			t.Fatalf("listed tool %q mishandled: %v", name, err)
		}
	}
	calls, _, err := run(t, perm.Allowlist("bash", "read", "write"), "edit")
	if err == nil || calls != 0 {
		t.Fatalf("unlisted tool must be denied, got %d exec calls / %v", calls, err)
	}
}

// TestApprovedPluginPasses (SPEC_PLUGINS 7, the presence reversal): a
// name the second door admits (an approved plugin, live in the table)
// passes the allow-list though it is absent from the static list.
func TestApprovedPluginPasses(t *testing.T) {
	door := func(name string) bool { return name == "forged" }
	calls, _, err := run(t, perm.AllowlistWithDoor([]string{"bash"}, door), "forged")
	if err != nil || calls != 1 {
		t.Fatalf("a live plugin must pass via the door, got %d calls / %v", calls, err)
	}
}

// TestPendingPluginRefused (SPEC_PLUGINS 7, named): a name the door
// does not admit (a pending plugin, not yet live in the table) is
// refused exactly like an unlisted native.
func TestPendingPluginRefused(t *testing.T) {
	door := func(name string) bool { return name == "forged" }
	calls, _, err := run(t, perm.AllowlistWithDoor([]string{"bash"}, door), "pending")
	if err == nil || calls != 0 {
		t.Fatalf("a not-yet-live plugin must be denied, got %d exec calls / %v", calls, err)
	}
}

// TestDeletedAfterReloadRefused (SPEC_PLUGINS 7, named): the door's
// answer is the live table's now; a plugin dropped by a reload (its
// file removed) is no longer admitted.
func TestDeletedAfterReloadRefused(t *testing.T) {
	live := map[string]bool{"forged": true}
	door := func(name string) bool { return live[name] }
	_, _, err := run(t, perm.AllowlistWithDoor([]string{"bash"}, door), "forged")
	if err != nil {
		t.Fatal("the live plugin must pass before the drop")
	}
	live["forged"] = false // the reload dropped it
	calls, _, err := run(t, perm.AllowlistWithDoor([]string{"bash"}, door), "forged")
	if err == nil || calls != 0 {
		t.Fatalf("a dropped plugin must be denied, got %d exec calls / %v", calls, err)
	}
}

// TestDoorNeverAdmitsNative (SPEC_PLUGINS 7, named): a native tool
// absent from the static list stays denied even with a door present.
// The door mirrors the live plugin table's IsPlugin — true for a live
// plugin only, never a native (the collision rule keeps the sets
// disjoint) — so a native name is never admitted by it.
func TestDoorNeverAdmitsNative(t *testing.T) {
	door := func(name string) bool { return name == "forged" }
	calls, _, err := run(t, perm.AllowlistWithDoor([]string{"bash"}, door), "read")
	if err == nil || calls != 0 {
		t.Fatalf("a native absent from the static list must stay denied, got %d exec calls / %v", calls, err)
	}
}

// TestNilDoorIsToday (SPEC_PLUGINS 7, named): a nil second door is
// today's behavior exactly — the static list alone decides.
func TestNilDoorIsToday(t *testing.T) {
	calls, _, err := run(t, perm.AllowlistWithDoor([]string{"bash"}, nil), "forged")
	if err == nil || calls != 0 {
		t.Fatalf("nil door must keep today's static-only denial, got %d exec calls / %v", calls, err)
	}
}
