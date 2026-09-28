package main

import (
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/middleware/approve"
	"github.com/mrsirg97-rgb/rig/v2/middleware/cutoff"
	"github.com/mrsirg97-rgb/rig/v2/middleware/guard"
	"github.com/mrsirg97-rgb/rig/v2/middleware/paths"
	"github.com/mrsirg97-rgb/rig/v2/middleware/perm"
	"github.com/mrsirg97-rgb/rig/v2/middleware/toolset"
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

func guidelinesOf(ms []core.ToolMiddleware) string {
	var b strings.Builder
	for _, mw := range ms {
		if gc, ok := mw.(core.GuidelineContributor); ok {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(gc.Guidelines())
		}
	}
	return b.String()
}
