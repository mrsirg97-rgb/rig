package evt

import "context"

type Event interface {
	ID() uint64
	Priority() int
	Closure() Closure
	UpdatePriority(priority int)
}

type event struct {
	id       uint64
	priority int
	closure  Closure
	index    int
}

func NewEvent(id uint64, priority int, closure Closure) Event {
	return &event{id: id, priority: priority, closure: closure, index: -1}
}

func (e *event) ID() uint64                  { return e.id }
func (e *event) Priority() int               { return e.priority }
func (e *event) Closure() Closure            { return e.closure }
func (e *event) UpdatePriority(priority int) { e.priority = priority }

func Execute(e Event, ctx context.Context) {
	if e == nil || e.Closure() == nil {
		return
	}
	e.Closure().Resolve(ctx)
}
