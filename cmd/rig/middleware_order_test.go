package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/approve"
	"github.com/mrsirg97-rgb/rig/middleware/toolset"
)

func chainFor(t *testing.T, r *root, inner core.ToolExec) core.ToolExec {
	t.Helper()
	exec := inner
	for _, mw := range r.canonicalMiddleware() {
		exec = mw.Wrap(exec)
	}
	return exec
}

func TestCanonicalMiddlewareExpandsPathsBeforeTheGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	r := testRoot(nullFrontend{})
	r.live = toolset.New()
	r.approve = approve.Manual
	asked := ""
	r.askDoor = func(ctx context.Context, prompt string) bool {
		asked = prompt
		return true
	}
	exec := chainFor(t, r, func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	})
	if _, err := exec(context.Background(), core.ToolCall{ID: "c1", Name: "write", Args: json.RawMessage(`{"path":"~/x","content":"hi"}`)}); err != nil {
		t.Fatal(err)
	}
	if asked == "" {
		t.Fatal("a mutating call in manual mode must reach the ask door")
	}
	if !strings.Contains(asked, filepath.Join(home, "x")) {
		t.Fatalf("the gate must judge the expanded path, prompt %q", asked)
	}
}

func TestCanonicalMiddlewareDeniesUnknownToolsInsideTheRoundCap(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.live = toolset.New()
	r.rounds = 1
	exec := chainFor(t, r, func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	})
	_, err := exec(context.Background(), core.ToolCall{ID: "c1", Name: "nosuchtool", Args: json.RawMessage(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "not in the allow-list") {
		t.Fatalf("the first unknown call must reach the allow-list, got %v", err)
	}
	_, err = exec(context.Background(), core.ToolCall{ID: "c2", Name: "nosuchtool", Args: json.RawMessage(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "round cap") {
		t.Fatalf("a denied call spends the round budget: the second call must cap, got %v", err)
	}
}

func TestCanonicalMiddlewareAsksOnlyForAllowListedCalls(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.live = toolset.New()
	r.approve = approve.Manual
	asks := 0
	r.askDoor = func(ctx context.Context, prompt string) bool {
		asks++
		return true
	}
	exec := chainFor(t, r, func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	})
	_, err := exec(context.Background(), core.ToolCall{ID: "c1", Name: "nosuchtool", Args: json.RawMessage(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "not in the allow-list") {
		t.Fatalf("the unknown call must be refused at the allow-list, got %v", err)
	}
	if asks != 0 {
		t.Fatal("the gate must not spend the operator's attention on a call the allow-list refuses")
	}
}

func TestCanonicalMiddlewareContributesNoGuidelines(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.live = toolset.New()
	if g := guidelinesOf(r.canonicalMiddleware()); g != "" {
		t.Fatalf("the canonical chain must stay guideline-free (buildSystem harvests it byte-stable): %q", g)
	}
}
