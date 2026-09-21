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

// canonicalMiddleware is the one written order (see middleware/PACKAGE.md:
// loop.Run composes exec = mw.Wrap(exec) over this slice, so the
// last-listed link runs first). The order is load-bearing: paths expands
// "~" before any gate validates an argument; cutoff refuses a truncated
// call before approval spends a prompt on it; the permission gates deny
// before approval asks and before the retry guard counts a failure; and
// Cap truncates every reply, refusals included. Reorder only against the
// chain tests in cmd/rig.
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
