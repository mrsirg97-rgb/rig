// Package perm is the deny-by-default permission middleware: a static
// allow-list of tool names. A denied call is fed back to the model as a
// refusal naming the tool and the list, attributed so downstream guards can
// bound the repetition.
package perm

import (
	"context"
	"errors"
	"fmt"

	"github.com/mrsirg97-rgb/looper/core"
)

type allowlist struct {
	allowed map[string]bool
}

// Allowlist permits exactly the listed tool names; everything else is
// denied. A wrap-only participant: it adapts through ToolMiddlewareFunc
// and carries neither optional capability.
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
