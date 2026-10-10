package perm_test

import (
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/middleware/perm"
)

func TestAllowsListed(t *testing.T) {
	calls, content, err := pluginCall(t, perm.Allowlist("bash"), "bash", `{}`)
	if err != nil {
		t.Fatalf("listed tool denied: %v", err)
	}
	if calls != 1 || content != "executed" {
		t.Fatalf("allowed call must pass through once, got %d calls / %q", calls, content)
	}
}

func TestDeniesByDefault(t *testing.T) {
	calls, content, err := pluginCall(t, perm.Allowlist("bash"), "file", `{}`)
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

func TestApprovedPluginPasses(t *testing.T) {
	door := func(name string) bool { return name == "forged" }
	calls, _, err := pluginCall(t, perm.AllowlistWithDoor([]string{"bash"}, door), "forged", `{}`)
	if err != nil || calls != 1 {
		t.Fatalf("a live plugin must pass via the door, got %d calls / %v", calls, err)
	}
}

func TestPendingPluginRefused(t *testing.T) {
	door := func(name string) bool { return name == "forged" }
	calls, _, err := pluginCall(t, perm.AllowlistWithDoor([]string{"bash"}, door), "pending", `{}`)
	if err == nil || calls != 0 {
		t.Fatalf("a not-yet-live plugin must be denied, got %d exec calls / %v", calls, err)
	}
}

func TestDoorNeverAdmitsNative(t *testing.T) {
	door := func(name string) bool { return name == "forged" }
	calls, _, err := pluginCall(t, perm.AllowlistWithDoor([]string{"bash"}, door), "read", `{}`)
	if err == nil || calls != 0 {
		t.Fatalf("a native absent from the static list must stay denied, got %d exec calls / %v", calls, err)
	}
}
