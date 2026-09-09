package cutoff_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/cutoff"
)

type countingExec struct {
	calls int
}

func (e *countingExec) Exec(ctx context.Context, call core.ToolCall) (string, error) {
	e.calls++
	return "ran", nil
}

func wrapped(t *testing.T, e *countingExec) core.ToolExec {
	t.Helper()
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	}
	return cutoff.Middleware().Wrap(exec)
}

func TestMarkedCallRefusesWithoutExecuting(t *testing.T) {
	e := &countingExec{}
	exec := wrapped(t, e)
	call := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command": "cat /tmp/longfile`), Cut: "length"}

	content, err := exec(context.Background(), call)

	if e.calls != 0 {
		t.Fatalf("the tool executed %d times, want 0 (a cut call must never run)", e.calls)
	}
	if err == nil {
		t.Fatal("a cut call must refuse with an error")
	}
	if content != "" {
		t.Fatalf("refusal content = %q, want empty (the error carries the teaching text)", content)
	}
	if !strings.Contains(err.Error(), "cut off") || !strings.Contains(err.Error(), "bash") {
		t.Fatalf("refusal must name the cause and the call, got %q", err)
	}
}

func TestUnmarkedCallExecutes(t *testing.T) {
	e := &countingExec{}
	exec := wrapped(t, e)
	call := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)}

	if _, err := exec(context.Background(), call); err != nil {
		t.Fatalf("unmarked call refused: %v", err)
	}
	if e.calls != 1 {
		t.Fatalf("tool executed %d times, want 1", e.calls)
	}
}

func TestMalformedMarkedCallTeaches(t *testing.T) {
	e := &countingExec{}
	exec := wrapped(t, e)
	call := core.ToolCall{ID: "c1", Name: "edit", Args: json.RawMessage(`{"path": "/tmp/x" `), Cut: "stop"}

	_, err := exec(context.Background(), call)

	if e.calls != 0 {
		t.Fatalf("the tool executed %d times, want 0", e.calls)
	}
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("a malformed call must teach the malformed cause, got %v", err)
	}
}
