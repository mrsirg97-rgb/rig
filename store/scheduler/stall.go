package scheduler

import (
	"sync"
	"time"
)

type stallWatch struct {
	mu    sync.Mutex
	stall time.Duration
	timer *time.Timer
	last  time.Time
	fired bool
	done  bool
}

func newStallWatch(stall time.Duration, idle func()) *stallWatch {
	w := &stallWatch{stall: stall, last: time.Now()}
	w.timer = time.AfterFunc(stall, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.done {
			return
		}
		if time.Since(w.last) >= w.stall {
			w.fired = true
			idle()
		} else {
			w.timer.Reset(w.stall)
		}
	})
	return w
}

func (w *stallWatch) touch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	w.last = time.Now()
	w.timer.Reset(w.stall)
}

func (w *stallWatch) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.done = true
	w.timer.Stop()
}

func (w *stallWatch) hasFired() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fired
}
