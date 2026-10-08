package main

import (
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/middleware/approve"
	"github.com/mrsirg97-rgb/rig/v2/middleware/cutoff"
	"github.com/mrsirg97-rgb/rig/v2/middleware/guard"
	"github.com/mrsirg97-rgb/rig/v2/middleware/index"
	"github.com/mrsirg97-rgb/rig/v2/middleware/paths"
	"github.com/mrsirg97-rgb/rig/v2/middleware/perm"
	"github.com/mrsirg97-rgb/rig/v2/middleware/toolset"
	"github.com/mrsirg97-rgb/rig/v2/policy/operator"
	remapi "github.com/mrsirg97-rgb/rig/v2/tool/rem"
)

func (r *root) canonicalMiddleware() []core.ToolMiddleware {
	resultCap := r.resultCap
	if resultCap == 0 {
		resultCap = defaultResultCap
	}
	door := r.pluginDoor()
	if r.allow == nil {
		door = nil // no tools: the fire's worker executes nothing, plugins included
	}
	var mw []core.ToolMiddleware
	if r.graph != nil {
		mw = append(mw, index.Middleware(r.graph))
	}
	mw = append(mw,
		toolset.Resolve(r.live),
		approve.Gate(func() string { return r.approve }, r.askDoor, r.isMutating, r.drec),
		cutoff.Middleware(),
		perm.Plugins(r.pluginsDir, r.drec),
		perm.AllowlistWithDoor(r.allow, door, r.drec),
	)
	if r.delegated() {
		mw = append(mw, operator.Middleware())
	}
	mw = append(mw,
		guard.Bound(r.retries, r.drec),
		guard.Rounds(r.rounds, r.drec),
		guard.Cap(resultCap),
		paths.Middleware(r.drec),
	)
	if r.proposals != nil {
		mw = append(mw, decision.Site(r.proposals, r.cwd))
	}
	if r.decide != nil {
		mw = append(mw, remapi.Guide())
	}
	return mw
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
