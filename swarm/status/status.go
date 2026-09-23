package status

import (
	"sync"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

const every = 250 * time.Millisecond

type Emitter struct {
	notify func(core.Event)
	mu     sync.Mutex
	last   time.Time
}

func New(notify func(core.Event)) *Emitter {
	if notify == nil {
		return nil
	}
	return &Emitter{notify: notify}
}

// Emit delivers at most one frame per window. The snapshot is built lazily,
// only when the frame is due: the caller passes the builder, so a streaming
// worker's per-chunk emits never fold the store.
func (e *Emitter) Emit(st func() core.SwarmStatus) {
	if e == nil {
		return
	}
	if !e.due() {
		return
	}
	e.notify(st())
}

// Force always delivers: the exit's last frame must land.
func (e *Emitter) Force(st func() core.SwarmStatus) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.last = time.Now()
	e.mu.Unlock()
	e.notify(st())
}

func (e *Emitter) due() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if now.Sub(e.last) < every {
		return false
	}
	e.last = now
	return true
}
