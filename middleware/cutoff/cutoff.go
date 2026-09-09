package cutoff

import (
	"github.com/mrsirg97-rgb/rig/core"
)

func Middleware() core.ToolMiddleware {
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return next
	})
}
