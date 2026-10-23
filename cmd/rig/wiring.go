package main

import (
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/store"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

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
	Python       string
	FFmpeg       string
	Browser      string
	Allow        []string
	Fetch        sched.Fetch
	Spawn        sched.Spawn
	Models       func() models.Table
	Room         broadcast.Room
}
