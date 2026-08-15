package guard_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/middleware/guard"
)

type exec struct {
	calls    int
	failures int // first N calls error
	content  string
}

func (e *exec) Exec(ctx context.Context, call core.ToolCall) (string, error) {
	e.calls++
	if e.calls <= e.failures {
		return "fed back", errors.New("synthetic failure")
	}
	return e.content, nil
}

// The bounded-repetition invariant, table-driven: executions must equal the
// smaller of the failures offered and the limit.
func TestBoundsRepetition(t *testing.T) {
	cases := []struct {
		limit     int
		failures  int
		wantCalls int
		wantErr   bool
	}{
		{3, 0, 1, false},
		{3, 1, 2, false},
		{3, 3, 3, true}, // exhausted exactly at the bound
		{3, 9, 3, true}, // beyond the bound: bounded anyway
		{1, 5, 1, true},
		{5, 5, 5, true},
		{5, 4, 5, false},
	}
	for _, tc := range cases {
		e := &exec{failures: tc.failures, content: "ok"}
		var next core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
			return e.Exec(ctx, call)
		}
		next = guard.Retry(tc.limit)(next)
		_, err := next(context.Background(), core.ToolCall{Name: "bash", Args: json.RawMessage(`{}`)})
		if e.calls != tc.wantCalls {
			t.Fatalf("limit=%d failures=%d: %d executions, want %d",
				tc.limit, tc.failures, e.calls, tc.wantCalls)
		}
		if gotErr := err != nil; gotErr != tc.wantErr {
			t.Fatalf("limit=%d failures=%d: err=%v, want err=%v", tc.limit, tc.failures, err, tc.wantErr)
		}
	}
}

func TestExhaustionSurfacesTheLastAttempt(t *testing.T) {
	e := &exec{failures: 3, content: ""}
	var next core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	}
	next = guard.Retry(3)(next)
	content, err := next(context.Background(), core.ToolCall{Name: "bash"})
	if err == nil {
		t.Fatal("exhaustion must surface the final failure")
	}
	if content != "fed back" {
		t.Fatalf("exhaustion must surface the fed-back string for the model, got %q", content)
	}
}

func TestLimitBelowOneClampsToOne(t *testing.T) {
	e := &exec{failures: 9, content: ""}
	var next core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	}
	next = guard.Retry(0)(next)
	if _, err := next(context.Background(), core.ToolCall{}); err == nil {
		t.Fatal("degenerate limit must still execute exactly once and surface the failure")
	}
	if e.calls != 1 {
		t.Fatalf("degenerate limit executed %d times, want 1", e.calls)
	}
}
