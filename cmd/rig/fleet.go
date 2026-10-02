package main

import (
	"github.com/mrsirg97-rgb/rig/v2/models"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

func fleetWiring(fetch sched.Fetch, swapURL, modelID string, tbl models.Table, workersOn bool) (bool, string) {
	if !workersOn {
		return false, `swarm: the worker tools are off (settings.json "workers": false)`
	}
	if row, ok := tbl.Get(modelID); ok && row.Remote {
		return true, ""
	}
	if _, err := sched.ResidentModel(fetch, swapURL); err != nil {
		return false, "swarm: the swap is unreadable at start (the pair fails closed)"
	}
	return true, ""
}
