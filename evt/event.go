package evt

import "context"

type Event interface {
	ID() uint64
	Priority() int
	Context() Context
	UpdatePriority(priority int)
}

type event struct {
	id       uint64
	priority int
	ctx      Context
	index    int
}

func NewEvent(id uint64, priority int, ctx Context) Event {
	return &event{id: id, priority: priority, ctx: ctx, index: -1}
}

func (e *event) ID() uint64                  { return e.id }
func (e *event) Priority() int               { return e.priority }
func (e *event) Context() Context            { return e.ctx }
func (e *event) UpdatePriority(priority int) { e.priority = priority }

func Execute(e Event, ctx context.Context) {
	if e == nil || e.Context() == nil {
		return
	}
	e.Context().Resolve(ctx)
}
