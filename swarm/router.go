package swarm

import (
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

func (c *Controller) Wake() {
	if c.dispatching.Swap(true) {
		return
	}
	c.post(func() {
		c.dispatching.Store(false)
		c.dispatch()
	})
}

func (c *Controller) dispatch() {
	if c.ctx == nil || len(c.workers) == 0 {
		return
	}
	ctx := c.ctx
	tried := map[int]bool{}
	for {
		if c.budget > 0 && c.spent >= c.budget {
			if !c.budgetSaid {
				c.budgetSaid = true
				c.say("budget reached — $%.2f / $%.2f — the swarm stops claiming", c.spent, c.budget)
			}
			return
		}
		w := c.idleWorker(tried)
		if w == nil {
			return
		}
		tried[w.id] = true
		claimStatus := ""
		if w.role == RoleReviewer {
			claimStatus = "review"
		}
		reply, err := todostore.Claim(ctx, c.opts.TodoDB, c.proj, w.identity, claimStatus)
		if err != nil {
			c.loud(w, "w%d: claim: %v", w.id, err)
			return
		}
		if reply == "nothing to do" {
			continue
		}
		id := claimID(reply)
		if id == "" {
			c.loud(w, "w%d: claim reply unreadable: %q", w.id, reply)
			return
		}
		w.task = id
		select {
		case w.tasks <- id:
		case <-w.ctx.Done():
		}
		c.refresh()
		c.emit()
	}
}

func (c *Controller) idleWorker(tried map[int]bool) *worker {
	for _, w := range c.workers {
		if w.state == StateRunning && w.task == "" && !tried[w.id] {
			return w
		}
	}
	return nil
}
