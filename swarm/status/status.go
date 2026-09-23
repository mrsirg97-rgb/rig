package status

import (
	"sync"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

const every = 250 * time.Millisecond

type Emitter struct {
	mu     sync.Mutex
	last   time.Time
	notify func(core.Event)
}

func New(notify func(core.Event)) *Emitter {
	if notify == nil {
		return nil
	}
	return &Emitter{notify: notify}
}

func (e *Emitter) Emit(st core.SwarmStatus) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if time.Since(e.last) < every {
		e.mu.Unlock()
		return
	}
	e.last = time.Now()
	e.mu.Unlock()
	e.notify(st)
}

func (e *Emitter) Force(st core.SwarmStatus) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.last = time.Now()
	e.mu.Unlock()
	e.notify(st)
}
