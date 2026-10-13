package room

import "github.com/mrsirg97-rgb/evt/broadcast"

func MeshConfig(room, addr string, peers []string) broadcast.MeshConfig {
	return broadcast.MeshConfig{
		Room:       room,
		Machine:    addr,
		Addr:       addr,
		Peers:      peers,
		Prime:      broadcast.RFC3526Prime1536(),
		Iterations: 40000,
		Window:     4,
	}
}
