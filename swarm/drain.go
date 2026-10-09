package swarm

import (
	"errors"
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
			c.call(func() { w.state = StateExited; c.refresh() })
			return
		case id := <-w.tasks:
			if w.ctx.Err() != nil {
				c.call(func() { w.state = StateExited; c.refresh() })
				return
			}
			res := c.work(w, id)
			c.call(func() {
				c.settle(w, id, res)
				if res.holder != nil {
					w.state = StateExited
					w.task = ""
				}
				c.refresh()
				c.emit()
			})
			c.Wake()
			if res.holder != nil {
				return
			}
		}
	}
}

type workResult struct {
	ok        bool
	noVerdict bool
	holder    error
}

func (c *Controller) work(w *worker, id string) workResult {
	c.call(func() { w.heartbeat, w.verdict = time.Time{}, nil; c.refresh() })
	task, err := todostore.Task(w.ctx, c.opts.TodoDB, w.proj, id, w.identity)
	if err != nil {
		c.loud(w, "w%d: task %s: %v", w.id, id, err)
		return workResult{}
	}
	if err := c.opts.Cap.Hold(w.ctx); err != nil {
		return workResult{holder: err}
	}
	defer c.opts.Cap.Free()
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
		Allow:         c.allow(w),
		SpawnCtx:      w.ctx,
		Member:        w.member,
	})
	if res.Cost > 0 {
		c.call(func() { c.spent += res.Cost })
	}
	if err != nil {
		c.loud(w, "w%d: task %s: %v", w.id, id, err)
		if errors.Is(err, sched.ErrNotResident) {
			return workResult{holder: err}
		}
		return workResult{}
	}
	if res.Exit != 0 || res.TimedOut {
		return workResult{}
	}
	if w.role == RoleReviewer {
		var v *core.Verdict
		c.call(func() { v, w.verdict = w.verdict, nil })
		switch {
		case v == nil || (!v.Accept && v.Reason == ""):
			return workResult{noVerdict: true}
		case v.Accept:
			_, err := todostore.Accept(w.ctx, c.opts.TodoDB, w.proj, id, w.identity)
			if err != nil {
				c.loud(w, "w%d: accept %s: %v", w.id, id, err)
			}
			return workResult{ok: err == nil}
		}
		reason := v.Reason
		if len(reason) > todostore.MaxNoteLen {
			reason = reason[:todostore.MaxNoteLen]
		}
		var ok bool
		c.call(func() { ok = c.rejectTask(w, id, reason) == nil })
		return workResult{ok: ok}
	}
	_, err = todostore.Complete(w.ctx, c.opts.TodoDB, w.proj, id, w.identity, true)
	if err != nil {
		c.loud(w, "w%d: complete %s: %v", w.id, id, err)
	}
	return workResult{ok: err == nil}
}

func (c *Controller) allow(w *worker) []string {
	if w.role != RoleReviewer {
		return c.opts.Allow
	}
	return append(append([]string{}, c.opts.Allow...), "verdict")
}

func (c *Controller) delegate(in sched.DelegateInput) (sched.DelegateResult, error) {
	if c.opts.Delegate != nil {
		return c.opts.Delegate(in)
	}
	return sched.Delegate(in)
}

func (c *Controller) settle(w *worker, id string, res workResult) {
	if res.holder != nil {
		c.say(core.LevelError, "w%d stopped — %v", w.id, res.holder)
		c.release(w, id)
		return
	}
	if res.ok {
		w.done++
		w.task = ""
		return
	}
	c.retries[id]++
	if c.retries[id] == 1 {
		c.say(core.LevelError, "w%d died — %s restarted", w.id, id)
		c.release(w, id)
		w.task = ""
		return
	}
	c.say(core.LevelError, "w%d died — %s exited", w.id, id)
	c.failTask(w, id, res.noVerdict)
	w.failed++
	w.task = ""
}

func (c *Controller) release(w *worker, id string) {
	if _, err := todostore.Reap(w.ctx, c.opts.TodoDB, w.proj, []string{w.identity}, w.architect); err != nil {
		c.loud(w, "w%d: release %s: %v", w.id, id, err)
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
		c.loud(w, "w%d: fail %s: %v", w.id, id, err)
		return
	}
	c.say(core.LevelError, "%s failed — the worker died twice", id)
}

func (c *Controller) rejectTask(w *worker, id, reason string) error {
	c.rejects[id]++
	if c.rejects[id] > 2 {
		c.note(w, id, "the reviewer rejected this twice; the swarm failed it")
		if _, err := todostore.Fail(w.ctx, c.opts.TodoDB, w.proj, id, w.identity, false); err != nil {
			c.loud(w, "w%d: fail %s: %v", w.id, id, err)
			return err
		}
		c.say(core.LevelError, "%s failed — the reviewer rejected this twice; the swarm failed it", id)
		return nil
	}
	if _, err := todostore.Reject(w.ctx, c.opts.TodoDB, w.proj, id, reason, w.identity); err != nil {
		c.loud(w, "w%d: reject %s: %v", w.id, id, err)
		return err
	}
	c.say(core.LevelError, "%s rejected — %s", id, reason)
	return nil
}

func (c *Controller) note(w *worker, id, text string) {
	if _, err := todostore.Note(w.ctx, c.opts.TodoDB, w.proj, id, text, w.architect); err != nil {
		c.loud(w, "w%d: note %s: %v", w.id, id, err)
	}
}
