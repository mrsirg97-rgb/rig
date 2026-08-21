package approve_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/approve"
)

func gateExec(mode string, answer bool, asked *[]string) (core.ToolExec, *int) {
	calls := 0
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		calls++
		return "executed", nil
	}
	mutating := func(name string) bool { return name == "bash" || name == "write" }
	ask := func(ctx context.Context, prompt string) bool {
		if asked != nil {
			*asked = append(*asked, prompt)
		}
		return answer
	}
	return approve.Gate(func() string { return mode }, ask, mutating).Wrap(exec), &calls
}

func call(name, args string) core.ToolCall {
	return core.ToolCall{ID: "c1", Name: name, Args: json.RawMessage(args)}
}

// TestAutoNeverAsks (SPEC_MODES 4, named): auto is today's behavior —
// every call executes, the door never opens.
func TestAutoNeverAsks(t *testing.T) {
	var asked []string
	exec, calls := gateExec("auto", false, &asked)
	out, err := exec(context.Background(), call("bash", `{"cmd":"rm -rf /"}`))
	if err != nil || out != "executed" || *calls != 1 || len(asked) != 0 {
		t.Fatalf("auto must pass through: (%q, %v), calls %d, asked %v", out, err, *calls, asked)
	}
}

// TestManualReadSetPassesSilently (SPEC_MODES 4, named): manual asks
// only for the mutating set — the read set executes without a pause.
func TestManualReadSetPassesSilently(t *testing.T) {
	var asked []string
	exec, calls := gateExec("manual", false, &asked)
	out, err := exec(context.Background(), call("read", `{"path":"a.go"}`))
	if err != nil || out != "executed" || *calls != 1 || len(asked) != 0 {
		t.Fatalf("the read set must pass: (%q, %v), calls %d, asked %v", out, err, *calls, asked)
	}
}

// TestManualMutatingAsksAndRuns (SPEC_MODES 4, named): a mutating call
// in manual mode pauses for the answer; yes runs it, the prompt names
// the tool and previews the arguments.
func TestManualMutatingAsksAndRuns(t *testing.T) {
	var asked []string
	exec, calls := gateExec("manual", true, &asked)
	out, err := exec(context.Background(), call("bash", `{"cmd":"go test"}`))
	if err != nil || out != "executed" || *calls != 1 {
		t.Fatalf("an approved call must run: (%q, %v), calls %d", out, err, *calls)
	}
	if len(asked) != 1 || !strings.HasPrefix(asked[0], "bash ") || !strings.Contains(asked[0], "go test") {
		t.Fatalf("the prompt must name the tool and preview the args: %v", asked)
	}
}

// TestManualDeclineIsATeachingRefusal (SPEC_MODES 4, named): no is a
// model-visible result, not an error — the model reads the named
// refusal and adapts; nothing executes.
func TestManualDeclineIsATeachingRefusal(t *testing.T) {
	exec, calls := gateExec("manual", false, nil)
	out, err := exec(context.Background(), call("write", `{"path":"x"}`))
	if err != nil {
		t.Fatalf("a decline is a result, not an error: %v", err)
	}
	if *calls != 0 {
		t.Fatalf("a declined call must not run: %d calls", *calls)
	}
	if !strings.Contains(out, "the operator declined write") {
		t.Fatalf("the refusal must name the tool: %q", out)
	}
}

// TestNilAskDoorFailsClosed (SPEC_MODES 4, named): manual with no door
// declines with the named reason rather than executing unasked — the
// production root never wires this shape, and the gate still refuses.
func TestNilAskDoorFailsClosed(t *testing.T) {
	calls := 0
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		calls++
		return "executed", nil
	}
	g := approve.Gate(func() string { return "manual" }, nil, func(string) bool { return true }).Wrap(exec)
	out, err := g(context.Background(), call("bash", `{}`))
	if err != nil || calls != 0 || !strings.Contains(out, "no ask door") {
		t.Fatalf("no door must fail closed with the named reason: (%q, %v), calls %d", out, err, calls)
	}
}

// TestDialReadAtCallTime (SPEC_MODES 4, named): the mode closure is
// read per call — a flip applies to the very next call, no rebuild.
func TestDialReadAtCallTime(t *testing.T) {
	mode := "auto"
	calls := 0
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		calls++
		return "executed", nil
	}
	g := approve.Gate(func() string { return mode }, func(context.Context, string) bool { return false },
		func(string) bool { return true }).Wrap(exec)
	if out, _ := g(context.Background(), call("bash", `{}`)); out != "executed" {
		t.Fatalf("auto first: %q", out)
	}
	mode = "manual"
	if out, _ := g(context.Background(), call("bash", `{}`)); !strings.Contains(out, "declined") {
		t.Fatalf("manual next: %q", out)
	}
	if calls != 1 {
		t.Fatalf("only the auto call ran: %d", calls)
	}
}

// TestModeVocabulary (SPEC_MODES 4, named): empty descends to auto;
// auto and manual pass; anything else refuses.
func TestModeVocabulary(t *testing.T) {
	if m, ok := approve.Mode(""); !ok || m != "auto" {
		t.Fatalf("empty must descend to auto: (%q, %v)", m, ok)
	}
	for _, v := range []string{"auto", "manual"} {
		if m, ok := approve.Mode(v); !ok || m != v {
			t.Fatalf("%q must pass: (%q, %v)", v, m, ok)
		}
	}
	if _, ok := approve.Mode("yolo"); ok {
		t.Fatal("an unknown mode must refuse")
	}
}

// TestPromptPreviewTruncates (SPEC_MODES 4, named): the ask row is one
// glance — long arguments truncate, empty ones drop.
func TestPromptPreviewTruncates(t *testing.T) {
	long := `{"cmd":"` + strings.Repeat("a", 300) + `"}`
	p := approve.Prompt(call("bash", long))
	if len(p) > 140 || !strings.HasSuffix(p, "…") {
		t.Fatalf("a long preview must truncate: %d chars", len(p))
	}
	if p := approve.Prompt(call("todo", `{}`)); p != "todo" {
		t.Fatalf("empty args drop the preview: %q", p)
	}
}

// TestDelegatePromptShowsTheTaskFirstLine (SPEC_DELEGATE 7, named): a
// delegate call's ask row renders "delegate · <first line>", not the
// args JSON — the operator glances the work.
func TestDelegatePromptShowsTheTaskFirstLine(t *testing.T) {
	p := approve.Prompt(call("delegate", `{"task":"sweep the /tmp tree\nand report","cwd":"/ws"}`))
	if p != "delegate · sweep the /tmp tree" {
		t.Fatalf("the prompt must be the task's first line: %q", p)
	}
	long := `{"task":"` + strings.Repeat("a", 300) + `"}`
	p = approve.Prompt(call("delegate", long))
	if len(p) > 140 || !strings.HasSuffix(p, "…") {
		t.Fatalf("a long first line must truncate: %d chars", len(p))
	}
	if p := approve.Prompt(call("delegate", `{}`)); p != "delegate" {
		t.Fatalf("no task renders the bare name: %q", p)
	}
	if p := approve.Prompt(call("delegate", `not json`)); p == "delegate · " {
		t.Fatalf("unparseable args must fall back to the generic shape: %q", p)
	}
}
