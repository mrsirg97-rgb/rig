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
	total, err := sched.FleetCapacity(fetch, swapURL)
	if err != nil {
		return false, "swarm: the fleet needs more than one slot (the swap is unreadable at start)"
	}
	if total == 1 {
		return false, "swarm: the fleet needs more than one slot (the resident server runs one, and a turn holds it)"
	}
	return true, ""
}
