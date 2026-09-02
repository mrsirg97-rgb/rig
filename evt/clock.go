package evt

import (
	"sync/atomic"
	"time"
)

type Clock interface {
	Step() uint64
}

type counter struct{ n atomic.Uint64 }

func Counter() Clock { return &counter{} }

func (c *counter) Step() uint64 { return c.n.Add(1) }


type monotonic struct{ last atomic.Uint64 }

func Monotonic() Clock { return &monotonic{} }

func (m *monotonic) Step() uint64 {
	for {
		now := uint64(time.Now().UnixNano())
		last := m.last.Load()
		id := now
		if id <= last {
			id = last + 1
		}
		if m.last.CompareAndSwap(last, id) {
			return id
		}
	}
}
