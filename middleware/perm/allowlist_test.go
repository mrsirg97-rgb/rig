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
