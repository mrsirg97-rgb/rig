package guard_test

// The pane-aligned cases (SPEC_HARDENING decision 7): name keying, the
// per-turn clear, and the note at the bound — failing first against the
// old args-digest keying that had no turn awareness at all.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/middleware/guard"
)

// Drifting args of one tool share the budget: a model failing `edit` with
// drifting args cannot dodge the bound by varying the call. (The old
// TestDistinctCallsAreCountedSeparately codified the opposite; decision 7
// inverts it into this shared-budget invariant.)
func TestDriftingArgsShareOneBound(t *testing.T) {
	e := &failingExec{calls: map[string]int{}}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	}
	exec = guard.Bound(2).Wrap(exec)

	a := core.ToolCall{ID: "c1", Name: "edit", Args: json.RawMessage(`{"path":"a"}`)}
	b := core.ToolCall{ID: "c2", Name: "edit", Args: json.RawMessage(`{"path":"b"}`)}
	var last string
	for i := 0; i < 4; i++ {
		content, _ := exec(context.Background(), a)
		last = content
		content, _ = exec(context.Background(), b)
		last = content
	}
	// the first two failures (one per args shape) consume the shared budget;
	// the rest are refused without executing
	if e.calls[`{"path":"a"}`] != 1 || e.calls[`{"path":"b"}`] != 1 {
		t.Fatalf("drifting args must share the budget, got %+v", e.calls)
	}
	if e.total != 2 {
		t.Fatalf("total executions %d, want 2 (the shared bound)", e.total)
	}
	if !strings.Contains(last, "stop reissuing") {
		t.Fatalf("the over-bound issuance must refuse, naming the bound, got %q", last)
	}
}

// A new user message is a new budget: the loop's TurnStart fan-out clears
// the tool's count.
func TestBudgetClearsAtTheTurnBoundary(t *testing.T) {
	mw := guard.Bound(1)
	obs, ok := mw.(core.TurnObserver)
	if !ok {
		t.Fatal("the guard must implement TurnObserver (the loop fans it out)")
	}
	fail := 0
	var exec core.ToolExec = mw.Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		fail++
		return "fed back", errors.New("synthetic failure")
	})
	call := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)}

	if _, err := exec(context.Background(), call); err == nil {
		t.Fatal("the first failure must feed back")
	}
	if content, _ := exec(context.Background(), call); !strings.Contains(content, "stop reissuing") {
		t.Fatalf("the second failure of the turn must be refused at limit 1, got %q", content)
	}
	obs.TurnStart(context.Background(), core.NewSession()) // the loop's L6 fan-out
	// the fake always fails: a fresh budget shows up as executed-and-fed-
	// back, not refused.
	if content, _ := exec(context.Background(), call); strings.Contains(content, "stop reissuing") {
		t.Fatalf("the new turn must start with a fresh budget (executed, not refused), got %q", content)
	}
	if fail != 2 {
		t.Fatalf("executions %d, want 2 (one per turn at limit 1; the refusals never execute)", fail)
	}
}

// The limit-th failure of a tool in a turn carries pane's note, verbatim,
// appended to the fed-back content.
func TestBoundFailureCarriesTheNoteVerbatim(t *testing.T) {
	var exec core.ToolExec = guard.Bound(3).Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		return "tool failed", errors.New("synthetic failure")
	})
	call := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)}
	var last string
	for i := 0; i < 3; i++ {
		last, _ = exec(context.Background(), call)
	}
	note := "[retry-guard] bash failed 3× in a row this turn. The error is above; read it and change the call, or stop calling this tool. Do not retry blindly."
	if !strings.Contains(last, note) {
		t.Fatalf("the third failure must carry the note verbatim:\ngot:  %q\nwant: %q", last, note)
	}
}

// The note is appended to the tool's own fed-back content, not a
// replacement of it: the error stays above the note.
func TestNoteAppendsToTheToolContent(t *testing.T) {
	var exec core.ToolExec = guard.Bound(1).Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		return "the tool said no", errors.New("tool said no")
	})
	content, _ := exec(context.Background(), core.ToolCall{ID: "c1", Name: "edit", Args: json.RawMessage(`{}`)})
	if !strings.HasPrefix(content, "the tool said no") || !strings.Contains(content, "[retry-guard] edit failed 1× in a row this turn") {
		t.Fatalf("the note must extend, not replace, the fed-back content: %q", content)
	}
}

// A refusal is an error (the fed-back failure marker) with the bound
// named — the loop's ToolResult carries it as Err.
func TestRefusalIsAnErrorNamingTheBound(t *testing.T) {
	var exec core.ToolExec = guard.Bound(1).Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		return "fed back", errors.New("synthetic failure")
	})
	call := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)}
	exec(context.Background(), call)
	content, err := exec(context.Background(), call)
	if err == nil {
		t.Fatal("the refusal must be a fed-back failure (Err non-nil)")
	}
	if !strings.Contains(content, "bound exhausted") || !strings.Contains(err.Error(), "bound exhausted") {
		t.Fatalf("the refusal must name the bound in both content and error: %q %v", content, err)
	}
}

// An empty fed-back content carries the note alone: the model still gets
// the teaching, and the error is the marker.
func TestNoteAloneWhenTheToolContentIsEmpty(t *testing.T) {
	var exec core.ToolExec = guard.Bound(1).Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		return "", errors.New("boom")
	})
	content, err := exec(context.Background(), core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("the failure must feed back")
	}
	if !strings.Contains(content, "[retry-guard] bash failed 1× in a row this turn") {
		t.Fatalf("an empty tool content must carry the note alone: %q", content)
	}
}
