package operator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

var operatorPairs = [][2]string{
	{"todo", "prune"}, {"todo", "accept"}, {"todo", "reject"}, {"todo", "move"},
	{"scheduler", "remove"}, {"plugin", "delete"},
}

var workerVerbs = [][2]string{
	{"todo", "create"}, {"todo", "claim"}, {"todo", "start"}, {"todo", "complete"},
	{"todo", "fail"}, {"todo", "release"}, {"todo", "retry"}, {"todo", "read"},
	{"todo", "note"}, {"todo", "notes"}, {"todo", "finished"},
	{"scheduler", "create"}, {"scheduler", "update"}, {"scheduler", "list"},
	{"scheduler", "show"}, {"scheduler", "pause"}, {"scheduler", "resume"},
	{"scheduler", "runs"}, {"scheduler", "repair"},
	{"plugin", "run"}, {"plugin", "schema"}, {"plugin", "list"},
	{"plugin", "create"}, {"plugin", "reload"},
	{"rem", "prune"}, {"rem", "learn"}, {"rem", "recall"}, {"rem", "pack"},
}

func refusingExec(t *testing.T) (core.ToolExec, *int) {
	t.Helper()
	calls := 0
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		calls++
		return "ran", nil
	}, &calls
}

func callArgs(t *testing.T, action string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"action": action})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTheMiddlewareRefusesEachOperatorPairByName(t *testing.T) {
	exec, calls := refusingExec(t)
	exec = Middleware().Wrap(exec)
	for _, pair := range operatorPairs {
		content, err := exec(context.Background(), core.ToolCall{
			ID: "c1", Name: pair[0], Args: callArgs(t, pair[1]),
		})
		if err != nil {
			t.Fatalf("%s %s: the refusal is a teaching result, not a fault: %v", pair[0], pair[1], err)
		}
		want := pair[0] + " " + pair[1] + ": the session's verb — a worker does not judge or delete the board; leave it for the session"
		if content != want {
			t.Fatalf("%s %s: refusal = %q, want %q", pair[0], pair[1], content, want)
		}
	}
	if *calls != 0 {
		t.Fatalf("a refused call reached the tool %d times", *calls)
	}
}

func TestTheMiddlewarePassesEveryOtherVerb(t *testing.T) {
	exec, calls := refusingExec(t)
	exec = Middleware().Wrap(exec)
	for _, pair := range workerVerbs {
		content, err := exec(context.Background(), core.ToolCall{
			ID: "c1", Name: pair[0], Args: callArgs(t, pair[1]),
		})
		if err != nil {
			t.Fatalf("%s %s: a verb the session does not keep must pass: %v", pair[0], pair[1], err)
		}
		if content != "ran" {
			t.Fatalf("%s %s: content = %q, want the tool's own", pair[0], pair[1], content)
		}
	}
	if *calls != len(workerVerbs) {
		t.Fatalf("inner calls = %d, want %d", *calls, len(workerVerbs))
	}
}

func TestTheMiddlewarePassesArgsItCannotRead(t *testing.T) {
	exec, calls := refusingExec(t)
	exec = Middleware().Wrap(exec)
	for _, args := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"action": 3}`),
		json.RawMessage(`not json`),
		json.RawMessage(nil),
	} {
		if _, err := exec(context.Background(), core.ToolCall{ID: "c1", Name: "todo", Args: args}); err != nil {
			t.Fatalf("args %s: unreadable args are the tool's refusal, not the middleware's: %v", args, err)
		}
	}
	if *calls != 4 {
		t.Fatalf("inner calls = %d, want 4", *calls)
	}
}

func TestTheMiddlewareNamesAToolWithoutOperatorVerbsThrough(t *testing.T) {
	exec, calls := refusingExec(t)
	exec = Middleware().Wrap(exec)
	if _, err := exec(context.Background(), core.ToolCall{
		ID: "c1", Name: "rem", Args: callArgs(t, "prune"),
	}); err != nil {
		t.Fatalf("rem prune must pass: %v", err)
	}
	if _, err := exec(context.Background(), core.ToolCall{
		ID: "c2", Name: "nosuchtool", Args: callArgs(t, "prune"),
	}); err != nil {
		t.Fatalf("an unknown tool is not the middleware's to refuse: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("inner calls = %d, want 2", *calls)
	}
}
