package cutoff

import (
	"context"
	"fmt"

	"github.com/mrsirg97-rgb/rig/core"
)

func Middleware() core.ToolMiddleware {
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			if call.Cut == "" {
				return next(ctx, call)
			}
			return "", refusal(call)
		}
	})
}

func refusal(call core.ToolCall) error {
	if call.Cut == "length" {
		return fmt.Errorf("%s: the tool call was cut off by the output token limit while its arguments were still being generated; re-issue the call more tersely, or split it into several calls", call.Name)
	}
	return fmt.Errorf("%s: the tool call's arguments were not valid JSON when the stream ended (finish_reason %q); re-issue the call", call.Name, call.Cut)
}
