package main

import (
	"context"
	"errors"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/swarm"
)

type swarmAdapter struct{ c *swarm.Controller }

func (a swarmAdapter) Start(ctx context.Context, in command.SwarmStart) (string, error) {
	if a.c == nil {
		return "", errors.New("swarm: no workers configured (workers.json names the model)")
	}
	return a.c.Start(ctx, swarm.StartOpts{Count: in.Count, Role: in.Role, Model: in.Model})
}

func (a swarmAdapter) List() []command.SwarmWorker {
	if a.c == nil {
		return nil
	}
	rows := a.c.List()
	out := make([]command.SwarmWorker, len(rows))
	for i, w := range rows {
		out[i] = command.SwarmWorker{
			ID: w.ID, Role: w.Role, Model: w.Model, Task: w.Task,
			Heartbeat: w.Heartbeat, Done: w.Done, Failed: w.Failed, State: w.State,
		}
	}
	return out
}

func (a swarmAdapter) Stop() (string, error) {
	if a.c == nil {
		return "", errors.New("swarm: no swarm running")
	}
	return a.c.Stop()
}
