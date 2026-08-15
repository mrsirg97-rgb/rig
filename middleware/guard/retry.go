// Package guard is the retry-guard middleware: bounds the repetition of
// fed-back failures per call. The bound is a hard attempt ceiling; the final
// attempt's result and error surface either way.
package guard

import (
	"context"

	"github.com/mrsirg97-rgb/looper/core"
)

// Retry permits at most limit executions per call (clamped to >= 1).
func Retry(limit int) core.ToolMiddleware {
	if limit < 1 {
		limit = 1
	}
	return func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			for attempts := 0; ; attempts++ {
				content, err := next(ctx, call)
				if err == nil || attempts+1 >= limit || ctx.Err() != nil {
					return content, err
				}
			}
		}
	}
}
