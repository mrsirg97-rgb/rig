package loop_test

// End-to-end pairing, per the spec: a denied call and a failing tool call are
// both fed back to the model and bounded by the guard; the loop itself never
// retries.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/looper"
	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/loop"
	"github.com/mrsirg97-rgb/looper/middleware/guard"
	"github.com/mrsirg97-rgb/looper/middleware/perm"
)

// countingTool fails for its first calls, counting every execution.
type countingTool struct {
	name  string
	fail  int
	calls int
}

func (t *countingTool) Name() string { return t.name }

func (t *countingTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (t *countingTool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	t.calls++
	if t.calls <= t.fail {
		return "tool failed", errors.New("tool failed")
	}
	return "recovered", nil
}

func TestDenialIsFedBackAndBounded(t *testing.T) {
	bash := &countingTool{name: "bash"}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{
			callEv(core.ToolCall{ID: "c1", Name: "bash"}),
			doneEv(),
		}},
		{events: []core.Event{textEv("understood"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := looper.New(
		looper.WithProvider(p),
		looper.WithFrontend(f),
		looper.WithPolicy(&transcriptPolicy{}),
		looper.WithTools(bash),
		looper.WithMiddleware(
			perm.Allowlist("read"), // bash is not listed: denied
			guard.Retry(3),
		),
	)
	k.Session = session

	f.inputs <- "run bash"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}

	if bash.calls != 0 {
		t.Fatalf("denied call executed %d times, want 0", bash.calls)
	}
	var refusal string
	for _, m := range session.Messages {
		if m.Role == core.RoleTool {
			refusal = m.Content
		}
	}
	if !strings.Contains(refusal, "bash") || !strings.Contains(refusal, "allow") {
		t.Fatalf("denial must be fed back to the model naming the tool and the allow-list, got %q", refusal)
	}
	if len(session.Messages) != 4 || session.Messages[3].Role != core.RoleAssistant ||
		!strings.Contains(session.Messages[3].Content, "understood") {
		t.Fatalf("the model must get to recover after the fed-back denial:\n%s", dump(session))
	}
}

func TestToolFailureIsBoundedAndRecoverable(t *testing.T) {
	bash := &countingTool{name: "bash", fail: 999}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{callEv(core.ToolCall{ID: "c1", Name: "bash"}), doneEv()}},
		{events: []core.Event{callEv(core.ToolCall{ID: "c1", Name: "bash"}), doneEv()}},
		{events: []core.Event{callEv(core.ToolCall{ID: "c1", Name: "bash"}), doneEv()}},
		{events: []core.Event{callEv(core.ToolCall{ID: "c1", Name: "bash"}), doneEv()}},
		{events: []core.Event{textEv("recovered"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := looper.New(
		looper.WithProvider(p),
		looper.WithFrontend(f),
		looper.WithPolicy(&transcriptPolicy{}),
		looper.WithTools(bash),
		looper.WithMiddleware(
			perm.Allowlist("bash"),
			guard.Retry(3),
		),
	)
	k.Session = session

	f.inputs <- "go"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}

	// each of the model's identical failing issuances executes exactly once;
	// the fourth is refused without executing.
	if bash.calls != 3 {
		t.Fatalf("failing call executed %d times across re-issuance, want exactly the bound of 3", bash.calls)
	}
	var sawExhaustion bool
	for _, m := range session.Messages {
		if m.Role == core.RoleTool && strings.Contains(m.Content, "stop reissuing") {
			sawExhaustion = true
		}
	}
	if !sawExhaustion {
		t.Fatalf("exhaustion must refuse the over-bound issuance, naming the bound:\n%s", dump(session))
	}
	if len(session.Messages) != 10 || !strings.Contains(session.Messages[9].Content, "recovered") {
		t.Fatalf("the model must get to recover after bounded exhaustion:\n%s", dump(session))
	}
}
