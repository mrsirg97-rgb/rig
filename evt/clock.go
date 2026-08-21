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

type monotonic struct{}

func Monotonic() Clock { return monotonic{} }

func (monotonic) Step() uint64 { return uint64(time.Now().UnixNano()) }
