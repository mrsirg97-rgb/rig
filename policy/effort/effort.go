package effort

import (
	"context"

	"github.com/mrsirg97-rgb/rig/core"
)

// decorator wraps the provider: Stream stamps the session's effort onto
// a request that has none. The compaction summary call sets its own
// (the row's, SPEC_CONFIG 4) and is untouched. The root's dial is read
// at call time, so next-turn is by construction — zero loop lines, the
// models-switch machinery.
type decorator struct {
	inner  core.Provider
	effort func() string
}

func Decorator(inner core.Provider, effort func() string) core.Provider {
	return &decorator{inner: inner, effort: effort}
}

func (d *decorator) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	if req.ReasoningEffort == "" {
		req.ReasoningEffort = d.effort()
	}
	return d.inner.Stream(ctx, req)
}
