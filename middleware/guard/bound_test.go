package guard_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/guard"
)

type failingExec struct {
	mu    sync.Mutex
	calls map[string]int
	total int
}

func (e *failingExec) Exec(ctx context.Context, call core.ToolCall) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
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
		exec = guard.Bound(limit).Wrap(exec)

		call := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"flaky"}`)}
		var lastErr error
		for i := 0; i <= limit; i++ {

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
	exec = guard.Bound(2).Wrap(exec)

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

func TestSuccessfulReissuanceResetsTheCount(t *testing.T) {

	var total int
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		total++
		if total == 3 {
			return "ok", nil
		}
		return "fed back", errors.New("synthetic failure")
	}
	exec = guard.Bound(3).Wrap(exec)

	call := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"flaky"}`)}
	var last string
	for i := 0; i < 4; i++ {
		content, _ := exec(context.Background(), call)
		last = content
	}
	if !strings.Contains(last, "fed back") {
		t.Fatalf("the fourth issuance must execute (the success resets the count), got %q", last)
	}
	if total != 4 {
		t.Fatalf("total executions %d, want 4 (the success resets the count)", total)
	}
}
