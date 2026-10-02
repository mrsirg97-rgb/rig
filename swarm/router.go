package swarm

import (
	"context"
	"fmt"

	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

func (c *Controller) Wake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Controller) route(ctx context.Context) {
	defer c.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
			c.dispatch(ctx)
		}
	}
}

func (c *Controller) dispatch(ctx context.Context) {
	tried := map[int]bool{}
	for {
		c.mu.Lock()
		if c.ctx == nil || len(c.workers) == 0 {
			c.mu.Unlock()
			return
		}
		proj := c.proj
		if c.budget > 0 && c.spent >= c.budget {
			said := c.budgetSaid
			c.budgetSaid = true
			spent, budget := c.spent, c.budget
			c.mu.Unlock()
			if !said {
				c.notice(fmt.Sprintf("swarm: budget reached — $%.2f / $%.2f — the swarm stops claiming", spent, budget))
			}
			return
		}
		w := c.idleWorker(tried)
		if w == nil {
			c.mu.Unlock()
			return
		}
		tried[w.id] = true
		c.mu.Unlock()
		claimStatus := ""
		if w.role == RoleReviewer {
			claimStatus = "review"
		}
		reply, err := todostore.Claim(ctx, c.opts.TodoDB, proj, w.identity, claimStatus)
		if err != nil {
			c.loud(w, "w%d: claim: %v\n", w.id, err)
			return
		}
		if reply == "nothing to do" {
			continue
		}
		id := claimID(reply)
		if id == "" {
			c.loud(w, "w%d: claim reply unreadable: %q\n", w.id, reply)
			return
		}
		c.set(w, func() { w.task = id })
		select {
		case w.tasks <- id:
		case <-w.ctx.Done():
		}
		c.emit(false)
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
