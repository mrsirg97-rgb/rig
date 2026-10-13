package room

import (
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
)

func MeshConfig(addr string, peers []string, period time.Duration, window int64) broadcast.MeshConfig {
	return broadcast.MeshConfig{
		Machine: addr,
		Addr:    addr,
		Peers:   peers,
		Clock:   broadcast.NewTickClock(period, window, PriorityFleet),
		Window:  window,
	}
}
