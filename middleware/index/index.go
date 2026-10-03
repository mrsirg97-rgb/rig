package index

import (
	"context"
	"encoding/json"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

type Indexer interface {
	Touch(path string)
}

var mapped = map[string]bool{"read": true, "write": true, "edit": true}

func Middleware(ix Indexer) core.ToolMiddleware {
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			out, err := next(ctx, call)
			if err == nil && ix != nil && mapped[call.Name] {
				if p := pathOf(call.Args); p != "" {
					ix.Touch(p)
				}
			}
			return out, err
		}
	})
}

func pathOf(args json.RawMessage) string {
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &a) != nil {
		return ""
	}
	return a.Path
}
