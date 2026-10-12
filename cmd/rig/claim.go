package main

import (
	"context"
	"path/filepath"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/room"
)

type claiming struct {
	inner    core.Provider
	machine  room.Member
	key      int64
	priority int
	talk     int64
	row      models.Model
	logs     string
}

func (c claiming) Stream(ctx context.Context, req core.Request) (<-chan evt.Signal, error) {
	if c.machine == nil {
		return c.inner.Stream(ctx, req)
	}
	claimed := make(chan struct{})
	var mint broadcast.Mint
	c.machine.Claim(c.key, c.priority, c.talk, func(m broadcast.Mint, ok bool) {
		mint = m
		close(claimed)
	})
	select {
	case <-claimed:
	case <-ctx.Done():
		go func() {
			<-claimed
			c.machine.Release(mint, c.priority)
		}()
		return nil, ctx.Err()
	}
	inner, err := c.inner.Stream(ctx, req)
	if err != nil {
		c.machine.Release(mint, c.priority)
		c.machine.Act(room.StartChild{
			Model:   c.row.ID,
			Port:    c.row.Port,
			Command: c.row.Command,
			Env:     c.row.Env,
			Log:     filepath.Join(c.logs, c.row.ID+".log"),
		}, c.key, room.PriorityChild, func(bool) {})
		return nil, err
	}
	out := make(chan evt.Signal, 8)
	go func() {
		defer c.machine.Release(mint, c.priority)
		for ev := range inner {
			out <- ev
		}
		close(out)
	}()
	return out, nil
}
