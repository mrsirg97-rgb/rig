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
	var record decision.Recorder
	if len(rec) > 0 {
		record = rec[0]
	}
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
			if record != nil {
				record.Record(ctx, decision.Final{
					Site:     decision.SitePerm,
					State:    string(call.Args),
					Question: decision.YesNo("allow", "allow "+call.Name+"?"),
					Answer:   "no",
					Decider:  decision.SitePerm,
				})
			}
			msg := fmt.Sprintf("permission denied: %s is not in the allow-list", call.Name)
			return msg, errors.New(msg)
		}
	})
}
