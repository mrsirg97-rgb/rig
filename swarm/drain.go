package swarm

import (
	"fmt"
	"os"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

const noTimeout = time.Duration(-1)

func (c *Controller) run(w *worker) {
	defer c.wg.Done()
	for {
		select {
		case <-w.ctx.Done():
			c.set(w, func() { w.state = StateExited })
			return
		case id := <-w.tasks:
			if w.ctx.Err() != nil {
				c.set(w, func() { w.state = StateExited })
				return
			}
			res := c.work(w, id)
			c.settle(w, id, res)
			c.emit(true)
			c.Wake()
		}
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
	res, err := c.delegate(sched.DelegateInput{
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
		Timeout:       noTimeout,
		Sandbox:       c.opts.Sandbox,
		SandboxBinds:  c.opts.SandboxBinds,
		RigHome:       c.opts.RigHome,
		StateDir:      c.opts.StateDir,
		Allow:         c.opts.Allow,
		SpawnCtx:      w.ctx,
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

func (c *Controller) delegate(in sched.DelegateInput) (sched.DelegateResult, error) {
	if c.opts.Delegate != nil {
		return c.opts.Delegate(in)
	}
	return sched.Delegate(in)
}

func (c *Controller) settle(w *worker, id string, res workResult) {
	if res.ok {
		c.set(w, func() { w.done++; w.task = "" })
		return
	}
	c.bump(w, "retries", id)
	if c.countOf("retries", id) == 1 {
		c.notice(fmt.Sprintf("swarm: w%d died — %s restarted", w.id, id))
		c.release(w, id)
		c.set(w, func() { w.task = "" })
		return
	}
	c.notice(fmt.Sprintf("swarm: w%d died — %s exited", w.id, id))
	c.failTask(w, id, res.noVerdict)
	c.set(w, func() { w.failed++; w.task = "" })
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

func (c *Controller) loud(w *worker, format string, args ...any) {
	if w.ctx.Err() != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "swarm: "+format, args...)
}

func (c *Controller) set(w *worker, fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn()
}
