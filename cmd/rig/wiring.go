package main

import (
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/store"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

// workerWiring is the fleet's shared wiring: the delegate and the swarm are
// children of the same session and take the same stores, homes, sandbox,
// spawn seam, and model table. It is built once at the root; the two Opts
// are filled from it.
type workerWiring struct {
	DB           store.DB
	Home         string
	RigHome      string
	StateDir     string
	SwapURL      string
	WorkerCmd    []string
	DefaultModel string
	Sandbox      string
	SandboxBinds []string
	Allow        []string
	Fetch        sched.Fetch
	Spawn        sched.Spawn
	Models       func() models.Table
	Room         broadcast.Room
}
