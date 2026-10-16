package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/middleware/approve"
	"github.com/mrsirg97-rgb/rig/v3/middleware/toolset"
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
	home := shortTemp(t)
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

func TestCanonicalMiddlewareAsksForThePluginDoor(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.live = toolset.New()
	r.natives = map[string]bool{"plugin": true}
	r.allow = []string{"plugin"}
	r.approve = approve.Manual
	asked := ""
	r.askDoor = func(ctx context.Context, prompt string) bool {
		asked = prompt
		return true
	}
	exec := chainFor(t, r, func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	})
	if _, err := exec(context.Background(), core.ToolCall{ID: "c1", Name: "plugin", Args: json.RawMessage(`{"action":"run","name":"syshealth","args":{}}`)}); err != nil {
		t.Fatal(err)
	}
	if asked == "" || !strings.Contains(asked, "plugin") {
		t.Fatalf("the plugin door must pause in manual mode, prompt %q", asked)
	}
}

func TestCanonicalMiddlewareContributesNoGuidelines(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.live = toolset.New()
	if g := guidelinesOf(r.canonicalMiddleware()); g != "" {
		t.Fatalf("the canonical chain must stay guideline-free (buildSystem harvests it byte-stable): %q", g)
	}
}

type tapIndexer struct {
	touched []string
}

func (t *tapIndexer) Touch(path string) { t.touched = append(t.touched, path) }

type tapTool struct{ name string }

func (t tapTool) Name() string        { return t.name }
func (t tapTool) Description() string { return "a stub the tap test resolves" }
func (t tapTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t tapTool) Exec(context.Context, json.RawMessage) (string, error) { return "ran", nil }

func tapRoot(t *testing.T, name string) (*root, *tapIndexer) {
	t.Helper()
	ix := &tapIndexer{}
	r := testRoot(nullFrontend{})
	r.graph = ix
	r.live = toolset.New(tapTool{name: name})
	return r, ix
}

func TestTheGraphTapFiresOnceForAResolvedNativeCall(t *testing.T) {
	r, ix := tapRoot(t, "read")
	exec := chainFor(t, r, func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	})
	if _, err := exec(context.Background(), core.ToolCall{ID: "c1", Name: "read", Args: json.RawMessage(`{"path":"notes.md"}`)}); err != nil {
		t.Fatal(err)
	}
	if len(ix.touched) != 1 || ix.touched[0] != "notes.md" {
		t.Fatalf("a resolved native call must touch the code map exactly once, got %v", ix.touched)
	}
}

func TestAResolveMissFallsThroughToTheLoopWithoutATouch(t *testing.T) {
	r, ix := tapRoot(t, "read")
	r.allow = append(r.allow, "nosuchtool")
	exec := chainFor(t, r, func(ctx context.Context, call core.ToolCall) (string, error) {
		return "", fmt.Errorf("unknown tool: %s", call.Name)
	})
	_, err := exec(context.Background(), core.ToolCall{ID: "c1", Name: "nosuchtool", Args: json.RawMessage(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("a resolve miss must fall through to the loop's unknown-tool error, got %v", err)
	}
	if len(ix.touched) != 0 {
		t.Fatalf("a failed call must not touch the code map, got %v", ix.touched)
	}
}

func TestARefusedCallNeverTouchesTheMap(t *testing.T) {
	r, ix := tapRoot(t, "write")
	r.approve = approve.Manual
	asked := false
	r.askDoor = func(ctx context.Context, prompt string) bool {
		asked = true
		return false
	}
	exec := chainFor(t, r, func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	})
	out, err := exec(context.Background(), core.ToolCall{ID: "c1", Name: "write", Args: json.RawMessage(`{"path":"~/x","content":"hi"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !asked {
		t.Fatal("a mutating call in manual mode must reach the ask door")
	}
	if !strings.Contains(out, "declined") {
		t.Fatalf("the denial is the refusal the model reads, got %q", out)
	}
	if len(ix.touched) != 0 {
		t.Fatalf("a refused call must not touch the code map, got %v", ix.touched)
	}
}
