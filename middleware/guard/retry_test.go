package guard_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/middleware/guard"
)

// failingExec always fails, counting executions per distinct args.
type failingExec struct {
	calls map[string]int
	total int
}

func (e *failingExec) Exec(ctx context.Context, call core.ToolCall) (string, error) {
	key := string(call.Args)
	e.calls[key]++
	e.total++
	return "fed back", errors.New("synthetic failure")
}

func TestRepetitionIsBoundedWithoutSilentRetry(t *testing.T) {
	for _, limit := range []int{1, 3, 5} {
		e := &failingExec{calls: map[string]int{}}
		var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
			return e.Exec(ctx, call)
		}
		exec = guard.Retry(limit)(exec)

		call := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"flaky"}`)}
		var lastErr error
		for i := 0; i <= limit; i++ {
			// each issuance executes at most once; the (limit+1)th is refused
			if _, lastErr = exec(context.Background(), call); i == limit {
				break
			}
		}

		if e.calls[string(call.Args)] != limit {
			t.Fatalf("limit=%d: identical failing call executed %d times across issuances, want exactly %d (once each)",
				limit, e.calls[string(call.Args)], limit)
		}
		if e.total != limit {
			t.Fatalf("limit=%d: total executions %d, want %d", limit, e.total, limit)
		}
		if lastErr == nil || !strings.Contains(lastErr.Error(), "stop reissuing") {
			t.Fatalf("limit=%d: exhaustion must name the bound and tell the model to stop, got %v", limit, lastErr)
		}
	}
}

func TestSuccessfulReissuanceStaysUnbounded(t *testing.T) {
	total := 0
	ok := func(ctx context.Context, call core.ToolCall) (string, error) {
		total++
		return "ok", nil
	}
	var exec core.ToolExec = ok
	exec = guard.Retry(2)(exec)

	call := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"poll"}`)}
	for i := 0; i < 10; i++ {
		if _, err := exec(context.Background(), call); err != nil {
			t.Fatalf("successful re-issuance %d must not count toward the bound: %v", i, err)
		}
	}
	if total != 10 {
		t.Fatalf("polling executed %d times, want 10 (unbounded)", total)
	}
}

func TestDistinctCallsAreCountedSeparately(t *testing.T) {
	e := &failingExec{calls: map[string]int{}}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	}
	exec = guard.Retry(2)(exec)

	a := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"a"}`)}
	b := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"b"}`)}
	for i := 0; i < 3; i++ {
		exec(context.Background(), a)
		exec(context.Background(), b)
	}
	if e.calls[`{"command":"a"}`] != 2 || e.calls[`{"command":"b"}`] != 2 {
		t.Fatalf("distinct calls must keep separate budgets, got %+v", e.calls)
	}
	if e.total != 4 {
		t.Fatalf("total executions %d, want 4", e.total)
	}
}
