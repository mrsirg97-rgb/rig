package approve

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

const (
	Auto   = "auto"
	Manual = "manual"
)

func Mode(s string) (string, bool) {
	if s == "" {
		return Auto, true
	}
	if s == Auto || s == Manual {
		return s, true
	}
	return "", false
}

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

func Prompt(call core.ToolCall) string {
	if call.Name == "delegate" {
		return delegatePrompt(call)
	}
	return PromptGeneric(call)
}

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
