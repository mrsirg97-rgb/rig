package main

import (
	"github.com/mrsirg97-rgb/rig/v2/models"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

// fleetWiring decides the drain pair's registration from one live read:
// the session's model row is remote, or the resident server runs more
// than one slot (SPEC_WORKERS 5). The string is /swarm's refusal while
// the pair stays off.
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
	if total == 0 {
		return false, "swarm: the fleet needs more than one slot (nothing resident at start)"
	}
	if total == 1 {
		return false, "swarm: the fleet needs more than one slot (the resident server runs one, and a turn holds it)"
	}
	return true, ""
}
