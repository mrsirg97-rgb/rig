package operator

import (
	"context"
	"encoding/json"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

const refusalTail = "the session's verb — a worker does not judge or delete the board; leave it for the session"

func Middleware() core.ToolMiddleware {
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			if verb, ok := operatorVerb(call); ok {
				return call.Name + " " + verb + ": " + refusalTail, nil
			}
			return next(ctx, call)
		}
	})
}

func operatorVerb(call core.ToolCall) (string, bool) {
	verbs := tool.Operator(call.Name)
	if len(verbs) == 0 {
		return "", false
	}
	var args struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(call.Args, &args) != nil || args.Action == "" {
		return "", false
	}
	for _, v := range verbs {
		if v == args.Action {
			return args.Action, true
		}
	}
	return "", false
}
