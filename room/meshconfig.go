package room

import (
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
)

func MeshConfig(addr string, peers []string, period time.Duration, window int64) broadcast.MeshConfig {
	return broadcast.NewMeshConfig(addr, addr, broadcast.NewTickClock(period, window, PriorityFleet),
		broadcast.WithPeers(peers...),
		broadcast.WithWindow(window),
	)
}
