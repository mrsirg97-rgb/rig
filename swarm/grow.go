package swarm

import (
	"context"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

func (c *Controller) freeSlots() int {
	resident, err := sched.ResidentModel(c.opts.Fetch, c.opts.SwapURL)
	if err != nil || resident == "" {
		return 1
	}
	model := c.model
	if model == "" {
		model = resident
	}
	free, _, err := sched.FreeSlots(c.opts.Fetch, c.opts.SwapURL, model)
	if err != nil || free < 1 {
		return 1
	}
	return free
}

func (c *Controller) growLoop() {
	defer c.wg.Done()
	poll := c.opts.Poll
	if poll <= 0 {
		poll = defaultPoll
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.grow()
		}
	}
}

func (c *Controller) grow() {
	c.mu.Lock()
	if c.ctx == nil || len(c.workers) == 0 {
		c.mu.Unlock()
		return
	}
	live := 0
	for _, w := range c.workers {
		if w.state == StateRunning {
			live++
		}
	}
	if live >= MaxWorkers {
		c.mu.Unlock()
		return
	}
	role, model, proj, architect := c.role, c.model, c.proj, c.architect
	c.mu.Unlock()

	if !tasksRemain(c.opts.TodoDB, proj, role) {
		return
	}
	if free := c.freeSlots(); free <= live {
		return
	}

	c.mu.Lock()
	w := &worker{
		id: len(c.workers) + 1, role: role, model: model,
		identity:  core.NewSession().ID,
		ctx:       c.ctx,
		proj:      proj,
		architect: architect,
		state:     StateRunning,
	}
	c.workers = append(c.workers, w)
	c.wg.Add(1)
	go c.run(w)
	c.mu.Unlock()
}

func tasksRemain(db store.DB, proj todostore.Project, role string) bool {
	counts, err := todostore.Counts(context.Background(), db, proj)
	if err != nil {
		return false
	}
	if role == RoleReviewer {
		return counts.Review > 0
	}
	return counts.Pending > 0
}
