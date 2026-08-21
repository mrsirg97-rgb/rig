package guard_test

// The streak rule, the per-turn clear, and the note at the bound.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/guard"
)

// Drifting args each get a fresh streak (SPEC_HARDENING decision 7, as
// amended): the bound strikes identical retries only, and a call differing
// from the last failed args resets the tool's count. The consequence this
// case pins, named and accepted: two failing calls alternating within one
// turn never trip the bound. (The pre-amendment version of this case,
// TestDriftingArgsShareOneBound, asserted the opposite.)
func TestDriftingArgsEachGetAFreshStreak(t *testing.T) {
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
	// every alternation resets the other call's streak, so every issuance
	// executes and none is ever refused
	if e.calls[`{"path":"a"}`] != 4 || e.calls[`{"path":"b"}`] != 4 {
		t.Fatalf("drifting args each get a fresh streak, got %+v", e.calls)
	}
	if e.total != 8 {
		t.Fatalf("total executions %d, want 8 (drifting args never refuse)", e.total)
	}
	if strings.Contains(last, "stop reissuing") {
		t.Fatalf("drifting args must not trip the bound, got %q", last)
	}
}

// The corrected call always executes: identical retries cap at the limit
// and the next identical issuance refuses, then a call with differing args
// resets the count and runs, and its own identical retries cap in turn. The
// "change the call" teaching is never followed by blocking the changed call.
func TestChangedCallResetsTheCount(t *testing.T) {
	e := &failingExec{calls: map[string]int{}}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	}
	exec = guard.Bound(3).Wrap(exec)

	a := core.ToolCall{ID: "c1", Name: "edit", Args: json.RawMessage(`{"path":"a"}`)}
	b := core.ToolCall{ID: "c2", Name: "edit", Args: json.RawMessage(`{"path":"b"}`)}

	var last string
	for i := 0; i < 4; i++ { // three identical failures, then a refusal
		last, _ = exec(context.Background(), a)
	}
	if !strings.Contains(last, "stop reissuing") {
		t.Fatalf("the 4th identical retry must refuse (identical retries cap survives), got %q", last)
	}
	if e.calls[`{"path":"a"}`] != 3 {
		t.Fatalf("identical retries must cap at %d executions, got %d", 3, e.calls[`{"path":"a"}`])
	}

	// the corrected call resets the count: it executes instead of being
	// refused (the teaching never blocks the changed call).
	last, _ = exec(context.Background(), b)
	if strings.Contains(last, "stop reissuing") {
		t.Fatalf("the changed call must reset the count and execute, got %q", last)
	}
	if e.calls[`{"path":"b"}`] != 1 {
		t.Fatalf("the changed call must execute exactly once, got %d", e.calls[`{"path":"b"}`])
	}

	for i := 0; i < 3; i++ { // b now repeats identically: it caps at its own 3
		last, _ = exec(context.Background(), b)
	}
	if !strings.Contains(last, "stop reissuing") {
		t.Fatalf("identical retries of the corrected call must cap too, got %q", last)
	}
	if e.calls[`{"path":"b"}`] != 3 {
		t.Fatalf("the corrected call's identical retries must cap at %d, got %d", 3, e.calls[`{"path":"b"}`])
	}
	if e.total != 6 {
		t.Fatalf("total executions %d, want 6 (3 per identical streak; refusals never execute)", e.total)
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

// The limit-th failure of a tool in a turn carries the note, appended to
// the fed-back content.
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
