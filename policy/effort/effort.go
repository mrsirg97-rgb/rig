package effort

import (
	"context"

	"github.com/mrsirg97-rgb/rig/core"
)

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
