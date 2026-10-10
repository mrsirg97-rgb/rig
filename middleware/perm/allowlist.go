package perm

import (
	"context"
	"errors"
	"fmt"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
)

func Allowlist(names ...string) core.ToolMiddleware {
	return allowlist(names, nil, nil)
}

func AllowlistWithDoor(names []string, door func(string) bool, rec ...decision.Recorder) core.ToolMiddleware {
	record := decision.FirstRecorder(rec...)
	return allowlist(names, door, record)
}

func allowlist(names []string, door func(string) bool, record decision.Recorder) core.ToolMiddleware {
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
			decision.Deny(ctx, record, decision.SitePerm, string(call.Args),
				decision.Binary("allow", "allow "+call.Name+"?"), "no")
			msg := fmt.Sprintf("permission denied: %s is not in the allow-list", call.Name)
			return msg, errors.New(msg)
		}
	})
}
