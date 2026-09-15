package main

import (
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/approve"
	"github.com/mrsirg97-rgb/rig/middleware/cutoff"
	"github.com/mrsirg97-rgb/rig/middleware/guard"
	"github.com/mrsirg97-rgb/rig/middleware/paths"
	"github.com/mrsirg97-rgb/rig/middleware/perm"
	"github.com/mrsirg97-rgb/rig/middleware/toolset"
)

func (r *root) canonicalMiddleware() []core.ToolMiddleware {
	resultCap := r.resultCap
	if resultCap == 0 {
		resultCap = defaultResultCap
	}
	return []core.ToolMiddleware{
		toolset.Resolve(r.live),
		approve.Gate(func() string { return r.approve }, r.askDoor, r.isMutating),
		cutoff.Middleware(),
		perm.Plugins(r.pluginsDir),
		perm.AllowlistWithDoor(r.allow, r.pluginDoor()),
		guard.Bound(r.retries),
		guard.Rounds(r.rounds),
		guard.Cap(resultCap),
		paths.Middleware(),
	}
}
