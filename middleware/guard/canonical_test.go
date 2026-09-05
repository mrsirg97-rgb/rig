package guard_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/guard"
)

func TestCanonicallyIdenticalArgsShareTheStreak(t *testing.T) {
	e := &failingExec{calls: map[string]int{}}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	}
	exec = guard.Bound(2).Wrap(exec)

	original := core.ToolCall{ID: "c1", Name: "edit", Args: json.RawMessage(`{"path":"a","flag":true}`)}
	reordered := core.ToolCall{ID: "c2", Name: "edit", Args: json.RawMessage(`{"flag":true,"path":"a"}`)}
	spaced := core.ToolCall{ID: "c3", Name: "edit", Args: json.RawMessage(` { "path" : "a" , "flag" : true } `)}

	for _, call := range []core.ToolCall{original, reordered} {
		content, err := exec(context.Background(), call)
		if err == nil {
			t.Fatalf("the failure must feed back, got %q", content)
		}
	}
	content, err := exec(context.Background(), spaced)
	if err == nil || !strings.Contains(content, "stop reissuing") {
		t.Fatalf("the third canonically identical retry must refuse, got %q (%v)", content, err)
	}
	if e.total != 2 {
		t.Fatalf("total executions %d, want 2 (key order and whitespace are not a changed call)", e.total)
	}
}

func TestCanonicalIdentityRespectsValuesNotKeys(t *testing.T) {
	e := &failingExec{calls: map[string]int{}}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	}
	exec = guard.Bound(1).Wrap(exec)

	a := core.ToolCall{ID: "c1", Name: "edit", Args: json.RawMessage(`{"path":"a"}`)}
	b := core.ToolCall{ID: "c2", Name: "edit", Args: json.RawMessage(`{"path":"b"}`)}

	content, err := exec(context.Background(), a)
	if err == nil {
		t.Fatalf("the first failure must feed back, got %q", content)
	}
	content, err = exec(context.Background(), b)
	if err == nil {
		t.Fatalf("the changed value must execute (and fail), got %q", content)
	}
	if strings.Contains(content, "stop reissuing") {
		t.Fatalf("a changed value must reset the count and execute, got %q", content)
	}
	if e.total != 2 {
		t.Fatalf("total executions %d, want 2 (the changed value is a fresh streak)", e.total)
	}
}
