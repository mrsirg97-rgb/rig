package swarm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
	"github.com/mrsirg97-rgb/rig/swarm/status"
)

const (
	RoleWorker   = "worker"
	RoleReviewer = "reviewer"

	StateRunning = "running"
	StateExited  = "exited"

	// MaxWorkers bounds one swarm: the induced work cap, the same shape as
	// the fleet's slots bounding the delegates.
	MaxWorkers = 16

	// emptyClaimsBeforeExit is how many consecutive "nothing to do" claims
	// end a drain worker.
	emptyClaimsBeforeExit = 3

	// defaultPoll is the idle wait between empty claims when Opts.Poll is
	// unset: the workers do not wake together and do not spin on an empty
	// queue.
	defaultPoll = 2 * time.Second

	// maxVerdictReason caps the reviewer's reject reason at the store's
	// note bound, so a long verdict cannot fail the review protocol.
	maxVerdictReason = todostore.MaxNoteLen

	workerTimeout = 2 * time.Hour
	workerStall   = 10 * time.Minute

	heartbeatLine = "rig: heartbeat"
)

type StartOpts struct {
	Count int
	Role  string
	Model string
}

type Worker struct {
	ID        int
	Role      string
	Model     string
	Task      string
	Heartbeat time.Time
	Done      int
	Failed    int
	State     string
}

type Opts struct {
	TodoDB        store.DB
	SchedDB       sched.DB
	Home          string
	Project       func(ctx context.Context, session string) (todostore.Project, error)
	Cwd           string
	WorkerCmd     []string
	Fetch         sched.Fetch
	Spawn         sched.Spawn
	SwapURL       string
	Sandbox       string
	SandboxBinds  []string
	RigHome       string
	StateDir      string
	Allow         []string
	FleetModel    string
	ReviewerModel string
	Models        func() models.Table
	Poll          time.Duration
	Frontend      core.Frontend
}

type Controller struct {
	opts Opts

	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	workers   []*worker
	wg        sync.WaitGroup
	architect string
	proj      todostore.Project
	retries   map[string]int
	rejects   map[string]int
	fe        core.Frontend
	emitter   *status.Emitter
}

type worker struct {
	id        int
	role      string
	model     string
	identity  string
	ctx       context.Context
	proj      todostore.Project
	architect string
	task      string
	heartbeat time.Time
	done      int
	failed    int
	state     string
}

func New(o Opts) *Controller {
	c := &Controller{opts: o, retries: map[string]int{}, rejects: map[string]int{}, fe: o.Frontend}
	if o.Frontend != nil {
		c.emitter = status.New(o.Frontend.Notify)
	}
	return c
}

// Start begins n drain workers. The architect is the session the command
// threads: the swarm works its bound queue, and the supervisor's doors
// attribute to it. A running swarm refuses; a new start clears the stale
// rows a finished drain left behind.
func (c *Controller) Start(ctx context.Context, in StartOpts) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if in.Count < 1 || in.Count > MaxWorkers {
		return "", fmt.Errorf("swarm: worker count must be 1..%d (got %d)", MaxWorkers, in.Count)
	}
	role := in.Role
	if role == "" {
		role = RoleWorker
	}
	if role != RoleWorker && role != RoleReviewer {
		return "", fmt.Errorf("swarm: unknown role %q (worker, reviewer)", role)
	}
	model, err := c.modelFor(role, in.Model)
	if err != nil {
		return "", err
	}
	session := ""
	if s, ok := core.SessionFrom(ctx); ok && s != nil {
		session = s.ID
	}
	proj, err := c.opts.Project(ctx, session)
	if err != nil {
		return "", fmt.Errorf("swarm: queue: %w", err)
	}
	if c.ctx == nil {
		c.ctx, c.cancel = context.WithCancel(context.Background())
	}
	c.proj = proj
	c.architect = session
	base := len(c.workers)
	added := "started"
	if base > 0 {
		added = "added"
	}
	for i := 1; i <= in.Count; i++ {
		w := &worker{
			id: base + i, role: role, model: model,
			identity:  core.NewSession().ID,
			ctx:       c.ctx,
			proj:      proj,
			architect: session,
			state:     StateRunning,
		}
		c.workers = append(c.workers, w)
		c.wg.Add(1)
		go c.run(w)
	}
	return fmt.Sprintf("swarm: %s %d %s (role %s · model %s)", added, in.Count, plural(in.Count, "worker"), role, model), nil
}

func (c *Controller) modelFor(role, override string) (string, error) {
	if override != "" {
		t := c.opts.Models()
		if _, ok := t.Get(override); !ok {
			return "", fmt.Errorf("swarm: no row for %q (known: %s)", override, strings.Join(t.Known(), ", "))
		}
		return override, nil
	}
	if role == RoleReviewer && c.opts.ReviewerModel != "" {
		return c.opts.ReviewerModel, nil
	}
	return c.opts.FleetModel, nil
}

// List is the supervisor's read: one row per drain worker, in start order.
func (c *Controller) List() []Worker {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Worker, len(c.workers))
	for i, w := range c.workers {
		out[i] = Worker{
			ID: w.id, Role: w.role, Model: w.model, Task: w.task,
			Heartbeat: w.heartbeat, Done: w.done, Failed: w.failed, State: w.state,
		}
	}
	return out
}

// Stop ends the swarm: the in-flight workers die with the context, the
// drain workers finish, their claims are released, and the rows clear.
func (c *Controller) Stop() (string, error) {
	c.mu.Lock()
	if len(c.workers) == 0 {
		c.mu.Unlock()
		return "", errors.New("swarm: no swarm running")
	}
	cancel := c.cancel
	count := len(c.workers)
	proj := c.proj
	architect := c.architect
	c.mu.Unlock()
	cancel()
	c.wg.Wait()
	c.mu.Lock()
	ended := make([]string, 0, len(c.workers))
	for _, w := range c.workers {
		ended = append(ended, w.identity)
	}
	c.workers = nil
	c.ctx = nil
	c.cancel = nil
	c.mu.Unlock()
	if _, err := todostore.Reap(context.Background(), c.opts.TodoDB, proj, ended, architect); err != nil {
		return "", fmt.Errorf("swarm: stop: release: %w", err)
	}
	c.emit(true)
	c.notice(fmt.Sprintf("swarm: /swarm exited — %d %s stopped", count, plural(count, "worker")))
	return fmt.Sprintf("swarm: stopped %d %s", count, plural(count, "worker")), nil
}

func (c *Controller) run(w *worker) {
	defer c.wg.Done()
	empties := 0
	for {
		if w.ctx.Err() != nil {
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
		Slots:         1,
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
	if c.fe == nil {
		return
	}
	c.fe.Notify(core.SwarmNotice{Text: text})
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

func claimID(reply string) string {
	start := strings.Index(reply, "'")
	if start < 0 {
		return ""
	}
	rest := reply[start+1:]
	end := strings.Index(rest, "'")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

type verdict struct {
	kind   string
	reason string
}

func parseVerdict(stdout string) verdict {
	lines := strings.Split(stdout, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "verdict:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "verdict:"))
		if rest == "accept" {
			return verdict{kind: "accept"}
		}
		if strings.HasPrefix(rest, "reject") {
			reason := strings.TrimSpace(strings.TrimPrefix(rest, "reject"))
			if reason == "" {
				return verdict{}
			}
			if len(reason) > maxVerdictReason {
				reason = reason[:maxVerdictReason]
			}
			return verdict{kind: "reject", reason: reason}
		}
		return verdict{}
	}
	return verdict{}
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
