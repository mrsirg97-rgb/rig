package evt

import "context"

type Event interface {
	ID() uint64
	Priority() int
	Closure() Closure
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

func (evt *event) ID() uint64       { return evt.id }
func (evt *event) Priority() int    { return evt.priority }
func (evt *event) Closure() Closure { return evt.closure }

func Execute(ctx context.Context, evt Event) {
	if evt == nil || evt.Closure() == nil {
		return
	}
	evt.Closure().Resolve(ctx)
}
