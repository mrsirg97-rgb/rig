package evt

import (
	"context"
	"sync"
)

type Engine interface {
	Start(ctx context.Context)
	Add(c Closure, priority int) uint64
	Update(id uint64, priority int) bool
	Stop()
	Pending() []Event
}

type Option func(*engine)

func WithClock(c Clock) Option { return func(e *engine) { e.clock = c } }

func WithTick(tick func(ctx context.Context)) Option {
	return func(e *engine) { e.tick = tick }
}

func WithCapacity(n int) Option { return func(e *engine) { e.q = NewQueue(n) } }

type engine struct {
	mu      sync.Mutex
	cond    *sync.Cond
	q       Queue
	clock   Clock
	tick    func(ctx context.Context)
	running bool
}

func NewEngine(opts ...Option) Engine {
	e := &engine{q: NewQueue(64), clock: Counter()}
	e.cond = sync.NewCond(&e.mu)
	for _, o := range opts {
		o(e)
	}
	return e
}

func (e *engine) Start(ctx context.Context) {
	e.mu.Lock()
	e.running = true
	e.mu.Unlock()

	watch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			e.Stop()
		case <-watch:
		}
	}()
	defer close(watch)

	e.mu.Lock()
	defer e.mu.Unlock()
	for e.running {
		if e.q.Len() == 0 {
			if e.tick != nil {
				e.mu.Unlock()
				e.tick(ctx)
				e.mu.Lock()
				continue
			}
			e.cond.Wait()
			continue
		}
		evt, _ := e.q.Pop()
		e.mu.Unlock()
		Execute(ctx, evt)
		e.mu.Lock()
	}
}

func (e *engine) Add(c Closure, priority int) uint64 {
	if c == nil {
		return 0
	}
	if priority < 0 {
		priority = 0
	}
	e.mu.Lock()
	id := e.clock.Step()
	e.q.Push(NewEvent(id, priority, c))
	e.cond.Signal()
	e.mu.Unlock()
	return id
}

func (e *engine) Update(id uint64, priority int) bool {
	if priority < 0 {
		priority = 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.q.Update(id, priority)
}

func (e *engine) Stop() {
	e.mu.Lock()
	e.running = false
	e.cond.Broadcast()
	e.mu.Unlock()
}

func (e *engine) Pending() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.q.View()
}
