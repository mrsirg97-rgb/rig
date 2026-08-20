package perm

import (
	"context"
	"errors"
	"fmt"

	"github.com/mrsirg97-rgb/rig/core"
)

type allowlist struct {
	allowed map[string]bool
}

func Allowlist(names ...string) core.ToolMiddleware {
	allowed := make(map[string]bool, len(names))
	for _, n := range names {
		allowed[n] = true
	}
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			if !allowed[call.Name] {
				msg := fmt.Sprintf("permission denied: %s is not in the allow-list", call.Name)
				return msg, errors.New(msg)
			}
			return next(ctx, call)
		}
	})
}
