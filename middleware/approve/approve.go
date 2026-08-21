// Package approve is the manual tool-approval gate (SPEC_MODES 4): a
// ToolMiddleware wired at the root with three closures — the dial, the
// ask door, and the mutating set — so the frozen core and the Frontend
// seam are untouched (extension without modification, the perm chain's
// precedent). In manual mode a mutating call pauses for the operator's
// answer; a denial is a teaching refusal the model reads, never a dead
// turn. The gate sits inside the allow-list and the provenance rule
// (executed after both), so the operator is only ever asked about a
// call that would actually run.
package approve

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

// Auto and Manual are the dial's vocabulary (SPEC_MODES 4): auto is
// today's behavior; manual asks at every mutating call.
const (
	Auto   = "auto"
	Manual = "manual"
)

// Mode validates the dial's vocabulary: empty descends to auto;
// anything outside the two is the caller's refusal to name.
func Mode(s string) (string, bool) {
	if s == "" {
		return Auto, true
	}
	if s == Auto || s == Manual {
		return s, true
	}
	return "", false
}

// Gate is the approval gate (SPEC_MODES 4). mode is the session's dial
// (read at call time — a flip applies to the very next call); ask is
// the frontend's door (blocks for the operator's answer; false
// declines); mutating names the calls that pause (the read set passes
// silently, or manual is death by a thousand confirms). A nil ask door
// never reaches the gate in production — the root wires the gate only
// when the frontend can ask — but the gate still refuses safely,
// declining with the named reason rather than executing unasked.
func Gate(mode func() string, ask func(ctx context.Context, prompt string) bool, mutating func(name string) bool) core.ToolMiddleware {
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			if mode() != Manual || !mutating(call.Name) {
				return next(ctx, call)
			}
			if ask == nil {
				return "approve: manual mode with no ask door (this frontend cannot ask) — the call was not run", nil
			}
			if !ask(ctx, Prompt(call)) {
				return "approve: the operator declined " + call.Name + " — do not retry the same call; adjust, or ask what they want", nil
			}
			return next(ctx, call)
		}
	})
}

// Prompt is the ask row's text: the tool's name and a one-line preview
// of its arguments, truncated — the operator glances what is about to
// run, the transcript keeps the full call as ever. A delegate call
// (SPEC_DELEGATE 7) shows the task's first line, not the wire: the
// operator glances the work, not the args JSON.
func Prompt(call core.ToolCall) string {
	if call.Name == "delegate" {
		return delegatePrompt(call)
	}
	return PromptGeneric(call)
}

// delegatePrompt renders "delegate · <first line>", falling back to the
// generic shape when the args do not parse as {task}.
func delegatePrompt(call core.ToolCall) string {
	var a struct {
		Task string `json:"task"`
	}
	if json.Unmarshal(call.Args, &a) != nil {
		return PromptGeneric(call)
	}
	line := strings.SplitN(strings.TrimSpace(a.Task), "\n", 2)[0]
	line = strings.TrimSpace(line)
	if line == "" {
		return "delegate"
	}
	const cap = 120
	if len(line) > cap {
		line = line[:cap] + "…"
	}
	return "delegate · " + line
}

// PromptGeneric is the non-delegate shape (tool name + flattened args),
// kept exported for the fallback.
func PromptGeneric(call core.ToolCall) string {
	args := strings.Join(strings.Fields(string(call.Args)), " ")
	const cap = 120
	if len(args) > cap {
		args = args[:cap] + "…"
	}
	if args == "" || args == "{}" {
		return call.Name
	}
	return call.Name + " " + args
}
