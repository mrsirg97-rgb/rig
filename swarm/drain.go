package swarm

import (
	"fmt"
	"os"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

func (c *Controller) run(w *worker) {
	defer c.wg.Done()
	empties := 0
	for {
		if w.ctx.Err() != nil {
			return
		}
		if c.atBudget() {
			spent, budget := c.budgetState()
			c.notice(fmt.Sprintf("swarm: budget reached — $%.2f / $%.2f — the swarm stops claiming", spent, budget))
			c.finish(w, true)
			return
		}
		status := ""
		if w.role == RoleReviewer {
			status = "review"
		}
		reply, err := todostore.Claim(w.ctx, c.opts.TodoDB, w.proj, w.identity, status)
		if err != nil {
			c.loud(w, "w%d: claim: %v\n", w.id, err)
			c.finish(w, false)
			return
		}
		if reply == "nothing to do" {
			if c.anyBusy() {
				c.idle(w)
				continue
			}
			empties++
			if empties >= emptyClaimsBeforeExit {
				c.finish(w, true)
				return
			}
			c.idle(w)
			continue
		}
		empties = 0
		id := claimID(reply)
		if id == "" {
			c.loud(w, "w%d: claim reply unreadable: %q\n", w.id, reply)
			c.finish(w, false)
			return
		}
		c.set(w, func() { w.task = id })
		c.emit(false)
		res := c.work(w, id)
		if res.ok {
			c.set(w, func() { w.done++ })
		} else {
			c.bump(w, "retries", id)
			if c.countOf("retries", id) == 1 {
				c.notice(fmt.Sprintf("swarm: w%d died — %s restarted", w.id, id))
				c.release(w, id)
				c.idle(w)
			} else {
				c.notice(fmt.Sprintf("swarm: w%d died — %s exited", w.id, id))
				c.failTask(w, id, res.noVerdict)
				c.set(w, func() { w.failed++ })
			}
		}
		c.emit(false)
		c.set(w, func() { w.task = "" })
	}
}

type workResult struct {
	ok        bool
	noVerdict bool
}

func (c *Controller) work(w *worker, id string) workResult {
	c.set(w, func() { w.heartbeat = time.Time{} })
	task, err := todostore.Task(w.ctx, c.opts.TodoDB, w.proj, id, w.identity)
	if err != nil {
		c.loud(w, "w%d: task %s: %v\n", w.id, id, err)
		return workResult{}
	}
	c.stream(w, []byte(fmt.Sprintf("## swarm task %s (%s)\n", id, w.role)))
	res, err := sched.Delegate(sched.DelegateInput{
		DB:            c.opts.SchedDB,
		Home:          c.opts.Home,
		Session:       w.identity,
		Cwd:           c.opts.Cwd,
		Task:          c.brief(w, task),
		Model:         w.model,
		WorkerSession: core.NewSession().ID,
		DefaultModel:  c.opts.DefaultModel,
		Models:        c.opts.Models,
		Fetch:         c.opts.Fetch,
		Spawn:         c.opts.Spawn,
		WorkerCmd:     c.opts.WorkerCmd,
		SwapURL:       c.opts.SwapURL,
		Timeout:       workerTimeout,
		Stall:         workerStall,
		Sandbox:       c.opts.Sandbox,
		SandboxBinds:  c.opts.SandboxBinds,
		RigHome:       c.opts.RigHome,
		StateDir:      c.opts.StateDir,
		Allow:         c.opts.Allow,
		Context:       w.ctx,
		SpawnCtx:      w.ctx,
		WaitBusy:      true,
		Observe:       func(p []byte) { c.stream(w, p) },
	})
	c.addSpent(res.Cost)
	if err != nil {
		c.loud(w, "w%d: task %s: %v\n", w.id, id, err)
		return workResult{}
	}
	if res.Exit != 0 || res.TimedOut {
		return workResult{}
	}
	if w.role == RoleReviewer {
		v := parseVerdict(res.Stdout)
		switch v.kind {
		case "accept":
			_, err := todostore.Accept(w.ctx, c.opts.TodoDB, w.proj, id, w.identity)
			if err != nil {
				c.loud(w, "w%d: accept %s: %v\n", w.id, id, err)
			} else {
				c.emit(false)
			}
			return workResult{ok: err == nil}
		case "reject":
			ok := c.rejectTask(w, id, v.reason) == nil
			if ok {
				c.emit(false)
			}
			return workResult{ok: ok}
		}
		return workResult{noVerdict: true}
	}
	_, err = todostore.Complete(w.ctx, c.opts.TodoDB, w.proj, id, w.identity, true)
	if err != nil {
		c.loud(w, "w%d: complete %s: %v\n", w.id, id, err)
	}
	return workResult{ok: err == nil}
}

func (c *Controller) release(w *worker, id string) {
	if _, err := todostore.Reap(w.ctx, c.opts.TodoDB, w.proj, []string{w.identity}, w.architect); err != nil {
		c.loud(w, "w%d: release %s: %v\n", w.id, id, err)
	}
}

func (c *Controller) failTask(w *worker, id string, noVerdict bool) {
	if w.role == RoleReviewer {
		reason := "the reviewer died twice"
		if noVerdict {
			reason = "the reviewer gave no verdict"
		}
		c.rejectTask(w, id, reason)
		return
	}
	c.note(w, id, "the worker died twice")
	if _, err := todostore.Fail(w.ctx, c.opts.TodoDB, w.proj, id, w.identity, false); err != nil {
		c.loud(w, "w%d: fail %s: %v\n", w.id, id, err)
		return
	}
	c.notice(fmt.Sprintf("swarm: %s failed — the worker died twice", id))
}

func (c *Controller) rejectTask(w *worker, id, reason string) error {
	c.bump(w, "rejects", id)
	if c.countOf("rejects", id) > 2 {
		c.note(w, id, "the reviewer rejected this twice; the swarm failed it")
		if _, err := todostore.Fail(w.ctx, c.opts.TodoDB, w.proj, id, w.identity, false); err != nil {
			c.loud(w, "w%d: fail %s: %v\n", w.id, id, err)
			return err
		}
		c.notice(fmt.Sprintf("swarm: %s failed — the reviewer rejected this twice; the swarm failed it", id))
		return nil
	}
	if _, err := todostore.Reject(w.ctx, c.opts.TodoDB, w.proj, id, reason, w.identity); err != nil {
		c.loud(w, "w%d: reject %s: %v\n", w.id, id, err)
		return err
	}
	c.notice(fmt.Sprintf("swarm: %s rejected — %s", id, reason))
	return nil
}

func (c *Controller) note(w *worker, id, text string) {
	if _, err := todostore.Note(w.ctx, c.opts.TodoDB, w.proj, id, text, w.architect); err != nil {
		c.loud(w, "w%d: note %s: %v\n", w.id, id, err)
	}
}

func (c *Controller) countOf(key, id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key == "rejects" {
		return c.rejects[id]
	}
	return c.retries[id]
}

func (c *Controller) bump(w *worker, key, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key == "rejects" {
		c.rejects[id]++
	} else {
		c.retries[id]++
	}
}

func (c *Controller) idle(w *worker) {
	poll := c.opts.Poll
	if poll <= 0 {
		poll = defaultPoll
	}
	select {
	case <-w.ctx.Done():
	case <-time.After(poll):
	}
}

func (c *Controller) loud(w *worker, format string, args ...any) {
	if w.ctx.Err() != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "swarm: "+format, args...)
}

func (c *Controller) anyBusy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.workers {
		if w.task != "" {
			return true
		}
	}
	return false
}

func (c *Controller) allExited() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.workers {
		if w.state != StateExited {
			return false
		}
	}
	return true
}

func (c *Controller) set(w *worker, fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn()
}

func (c *Controller) finish(w *worker, natural bool) {
	c.set(w, func() { w.state = StateExited })
	if natural && c.allExited() {
		c.notice("swarm: the board emptied — all workers exited")
	}
	c.emit(true)
}
