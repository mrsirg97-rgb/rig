package perm

import (
	"context"
	"errors"
	"fmt"

	"github.com/mrsirg97-rgb/rig/core"
)

func Allowlist(names ...string) core.ToolMiddleware {
	return allowlist(names, nil)
}

// AllowlistWithDoor is the allow-list's second door (SPEC_PLUGINS 7,
// the presence reversal): a name the live plugin table carries passes
// though it is absent from the static list. The door speaks for
// plugins only — a native is never admitted by it (the collision rule
// keeps the sets disjoint). A nil door is today: the static list alone
// decides, the same as Allowlist.
func AllowlistWithDoor(names []string, door func(string) bool) core.ToolMiddleware {
	return allowlist(names, door)
}

func allowlist(names []string, door func(string) bool) core.ToolMiddleware {
	allowed := make(map[string]bool, len(names))
	for _, n := range names {
		allowed[n] = true
	}
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			if allowed[call.Name] {
				return next(ctx, call)
			}
			if door != nil && door(call.Name) {
				return next(ctx, call)
			}
			msg := fmt.Sprintf("permission denied: %s is not in the allow-list", call.Name)
			return msg, errors.New(msg)
		}
	})
}
