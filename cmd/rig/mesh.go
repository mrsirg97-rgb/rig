package main

import (
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/config"
	"github.com/mrsirg97-rgb/rig/v3/models"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
)

func meshKeys(rows models.Table) (remote []int64, home []int64) {
	for _, id := range rows.Known() {
		m, _ := rows.Get(id)
		key := machineroom.KeyOf(id)
		if m.Port > 0 {
			home = append(home, key)
			continue
		}
		if m.BaseURL != "" {
			remote = append(remote, key)
		}
	}
	return remote, home
}

func meshConfig(setting *config.Mesh) broadcast.MeshConfig {
	period := time.Duration(setting.Period) * time.Millisecond
	return broadcast.MeshConfig{
		Machine: setting.Listen,
		Addr:    setting.Listen,
		Peers:   setting.Peers,
		Clock:   broadcast.NewTickClock(period, setting.Window, machineroom.PriorityFleet),
		Window:  setting.Window,
	}
}
