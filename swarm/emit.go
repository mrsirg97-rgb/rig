package swarm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

func (c *Controller) emit(force bool) {
	if c.emitter == nil {
		return
	}
	if force {
		c.emitter.Force(c.status)
	} else {
		c.emitter.Emit(c.status)
	}
}

func (c *Controller) status() core.SwarmStatus {
	c.mu.Lock()
	proj := c.proj
	c.mu.Unlock()
	rows := c.List()
	workers := make([]core.SwarmWorker, len(rows))
	for i, w := range rows {
		workers[i] = core.SwarmWorker{
			ID: w.ID, Role: w.Role, Task: w.Task,
			Heartbeat: w.Heartbeat, Done: w.Done, Failed: w.Failed, State: w.State,
		}
	}
	counts, err := todostore.Counts(context.Background(), c.opts.TodoDB, proj)
	if err != nil {
		return core.SwarmStatus{Workers: workers}
	}
	return core.SwarmStatus{Workers: workers, Pending: counts.Pending, Review: counts.Review}
}

func (c *Controller) notice(text string) {
	if c.emitter == nil {
		return
	}
	c.safeNotify(core.SwarmNotice{Text: text})
}

func (c *Controller) stream(w *worker, p []byte) {
	if bytes.Contains(p, []byte(heartbeatLine)) {
		c.set(w, func() { w.heartbeat = time.Now() })
	}
	c.emit(false)
	dir := filepath.Join(c.opts.Home, "swarm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("w%d.stream", w.id)), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(p)
}

func (c *Controller) brief(w *worker, task todostore.TaskInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "swarm task %s · %s (queue %s):\n%s\n", task.ID, w.role, c.proj.Label, task.Text)
	if len(task.Notes) > 0 {
		b.WriteString("\nNotes:\n")
		for _, n := range task.Notes {
			fmt.Fprintf(&b, "- %s (by %s)\n", n.Text, n.Session)
		}
	}
	b.WriteString("\nThe supervisor owns this board entry: the claim is the supervisor's, so findings go in the task's note (todo note) and in rem; do not create tasks, and do not start, complete, or fail the board's tasks.\n")
	if w.role == RoleReviewer {
		b.WriteString("\nReview the work now: read the diff and the task's notes; then decide. End your reply with exactly one verdict line as the last line: 'verdict: accept' or 'verdict: reject <reason>'.\n")
	} else {
		b.WriteString("\nDo the task now in this cwd. Report back: when you finish, persist durable findings with the rem tool (project scope: this cwd) and end your reply with a short summary of what you did.\n")
	}
	return b.String()
}
