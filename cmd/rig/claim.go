package main

import (
	"context"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/room"
)

func mintContext(ctx context.Context, mint broadcast.Mint) context.Context {
	return core.MintContext(ctx, mint)
}

type claiming struct {
	inner    core.Provider
	machine  room.Member
	kids     *children
	key      int64
	priority int
	talk     int64
	row      models.Model
	mesh     bool
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
	case <-c.machine.Gone():
		return nil, errLeft
	case <-ctx.Done():
		go func() {
			select {
			case <-claimed:
				c.machine.Release(mint, c.priority)
			case <-c.machine.Gone():
			}
		}()
		return nil, ctx.Err()
	}
	if c.kids != nil {
		if err := c.kids.ready(ctx, c.machine, c.row); err != nil {
			c.machine.Release(mint, c.priority)
			return nil, err
		}
	}
	if c.mesh && c.row.Remote {
		ctx = mintContext(ctx, mint)
	}
	inner, err := c.inner.Stream(ctx, req)
	if err != nil {
		c.machine.Release(mint, c.priority)
		return nil, err
	}
	out := make(chan evt.Signal, 8)
	go func() {
		defer c.machine.Release(mint, c.priority)
		for ev := range inner {
			if done, ok := ev.(core.Done); ok && c.kids != nil {
				_ = room.Record(c.kids.home, c.row.ID, room.Served{
					At:              time.Now().UTC(),
					Priority:        c.priority,
					Prompt:          done.Usage.Prompt,
					Cached:          done.Usage.CacheRead,
					Completion:      done.Usage.Completion,
					PromptPerSecond: done.Usage.PromptPerSecond,
					DecodePerSecond: done.Usage.DecodePerSecond,
				})
			}
			out <- ev
		}
		close(out)
	}()
	return out, nil
}
