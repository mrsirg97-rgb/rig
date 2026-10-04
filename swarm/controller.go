package swarm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/evt"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

const (
	RoleWorker   = "worker"
	RoleReviewer = "reviewer"

	StateRunning = "running"
	StateExited  = "exited"

	MaxWorkers = 16

	SupervisorID int64 = 0

	briefReview = "\nReview the work now: read the diff and the task's notes; then decide, and deliver it with the verdict tool: accept, or reject with the reason the author needs.\n"
)

func boardBrief(scope string) string {
	return "\nThe supervisor owns this board entry: the claim is the supervisor's, so findings go in the task's note (todo note) and in rem; do not create tasks, and do not start, complete, or fail the board's tasks. Every todo and rem call names scope: " + scope + ".\n"
}

func workBrief(scope string) string {
	return "\nDo the task now in this cwd. Report back: when you finish, persist durable findings with the rem tool (scope: " + scope + ") and end your reply with a short summary of what you did.\n"
}

type StartOpts struct {
	Count  int
	Role   string
	Model  string
	Budget float64
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
	TodoDB       store.DB
	SchedDB      sched.DB
	Home         string
	Project      func(ctx context.Context, session string) (todostore.Project, error)
	Cwd          string
	WorkerCmd    []string
	Fetch        sched.Fetch
	Spawn        sched.Spawn
	SwapURL      string
	Sandbox      string
	SandboxBinds []string
	RigHome      string
	StateDir     string
	Allow        []string
	DefaultModel string
	Models       func() models.Table
	Delegate     func(sched.DelegateInput) (sched.DelegateResult, error)
	Engine       evt.Engine
	Room         broadcast.Room
}

type Controller struct {
	opts Opts
	self broadcast.Member

	ctx        context.Context
	cancel     context.CancelFunc
	workers    []*worker
	wg         sync.WaitGroup
	architect  string
	proj       todostore.Project
	retries    map[string]int
	rejects    map[string]int
	budget     float64
	spent      float64
	budgetSaid bool
	role       string
	model      string

	view        atomic.Pointer[[]Worker]
	dispatching atomic.Bool
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
	tasks     chan string
	member    broadcast.Member
	verdict   *core.Verdict
}

func New(o Opts) *Controller {
	if o.Engine == nil || o.Room == nil {
		panic("swarm: the controller posts on the loop and speaks in a room; both are constructor arguments")
	}
	c := &Controller{opts: o, retries: map[string]int{}, rejects: map[string]int{}}
	c.view.Store(&[]Worker{})
	c.self = o.Room.Add(SupervisorID)
	c.self.Subscribe(context.Background(), c.receive)
	return c
}

func (c *Controller) post(fn func()) {
	c.opts.Engine.Add(evt.Func(func(context.Context) { fn() }), rig.PriorityFleet)
}

func (c *Controller) call(fn func()) {
	done := make(chan struct{})
	if c.opts.Engine.Add(evt.Func(func(context.Context) { fn(); close(done) }), rig.PriorityFleet) == 0 {
		fn()
		return
	}
	<-done
}

func (c *Controller) receive(err error, messages ...broadcast.Message) {
	if err != nil {
		return
	}
	for _, m := range messages {
		for _, w := range c.workers {
			if int64(w.id) != m.Origin() {
				continue
			}
			switch ev := m.Event().(type) {
			case nil:
				w.heartbeat = time.Now()
			case core.Verdict:
				w.verdict = &ev
			}
		}
	}
	c.refresh()
	c.emit()
}

func (c *Controller) refresh() {
	out := make([]Worker, len(c.workers))
	for i, w := range c.workers {
		out[i] = Worker{
			ID: w.id, Role: w.role, Model: modelName(w.model), Task: w.task,
			Heartbeat: w.heartbeat, Done: w.done, Failed: w.failed, State: w.state,
		}
	}
	c.view.Store(&out)
}

func (c *Controller) Start(ctx context.Context, in StartOpts) (string, error) {
	if in.Count < 1 {
		return "", fmt.Errorf("swarm: count %d is below one (swarm start <count> [role=…] [model=…] [budget=…])", in.Count)
	}
	if in.Count > MaxWorkers {
		return "", fmt.Errorf("swarm: count %d is past the %d cap", in.Count, MaxWorkers)
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
	c.call(func() {
		if c.ctx == nil {
			c.ctx, c.cancel = context.WithCancel(ctx)
		}
		if c.role == "" {
			c.role, c.model = role, model
		}
		c.proj = proj
		c.architect = session
		c.budget = in.Budget
		base := len(c.workers)
		for i := 1; i <= in.Count; i++ {
			w := &worker{
				id: base + i, role: role, model: model,
				identity:  core.NewSession().ID,
				ctx:       c.ctx,
				proj:      proj,
				architect: session,
				state:     StateRunning,
				tasks:     make(chan string, 1),
			}
			w.member = c.opts.Room.Add(int64(w.id))
			c.workers = append(c.workers, w)
			c.wg.Add(1)
			go c.run(w)
		}
		c.refresh()
	})
	c.Wake()
	return fmt.Sprintf("swarm: added %d %s (role %s · model %s)", in.Count, plural(in.Count, "agent"), role, modelName(model)), nil
}

func modelName(m string) string {
	if m == "" {
		return "resident"
	}
	return m
}

func (c *Controller) modelFor(role, override string) (string, error) {
	if override != "" {
		t := c.opts.Models()
		if _, ok := t.Get(override); !ok {
			return "", fmt.Errorf("swarm: no row for %q (known: %s)", override, strings.Join(t.Known(), ", "))
		}
		return override, nil
	}
	return "", nil
}

func (c *Controller) List() []Worker {
	return append([]Worker(nil), (*c.view.Load())...)
}

func (c *Controller) Stop() (string, error) {
	var cancel context.CancelFunc
	c.call(func() { cancel = c.cancel })
	if cancel == nil {
		return "", errors.New("swarm: no swarm running")
	}
	cancel()
	c.wg.Wait()
	var count int
	var ended []string
	var proj todostore.Project
	var architect string
	c.call(func() {
		count = len(c.workers)
		proj, architect = c.proj, c.architect
		for _, w := range c.workers {
			ended = append(ended, w.identity)
			w.member.Leave()
		}
		c.workers = nil
		c.ctx = nil
		c.cancel = nil
		c.role = ""
		c.model = ""
		c.refresh()
	})
	if _, err := todostore.Reap(context.Background(), c.opts.TodoDB, proj, ended, architect); err != nil {
		return "", fmt.Errorf("swarm: stop: release: %w", err)
	}
	c.call(func() {
		c.emit()
		c.say(core.LevelSuccess, "/swarm exited — %d %s stopped", count, plural(count, "worker"))
	})
	return fmt.Sprintf("swarm: stopped %d %s", count, plural(count, "agent")), nil
}

func (c *Controller) emit() {
	c.self.Publish(context.Background(), func(error) {}, broadcast.NewMessage(SupervisorID, true, c.status()))
}

func (c *Controller) status() core.SwarmStatus {
	rows := c.List()
	workers := make([]core.SwarmWorker, len(rows))
	for i, w := range rows {
		workers[i] = core.SwarmWorker{
			ID: w.ID, Role: w.Role, Task: w.Task,
			Heartbeat: w.Heartbeat, Done: w.Done, Failed: w.Failed, State: w.State,
		}
	}
	counts, err := todostore.Counts(context.Background(), c.opts.TodoDB, c.proj)
	if err != nil {
		return core.SwarmStatus{Workers: workers}
	}
	return core.SwarmStatus{Workers: workers, Pending: counts.Pending, Review: counts.Review}
}

func (c *Controller) say(level core.Level, format string, args ...any) {
	broadcast.Say(c.self, "swarm", strings.TrimRight(fmt.Sprintf(format, args...), "\n"), level)
}

func (c *Controller) loud(w *worker, format string, args ...any) {
	if w.ctx.Err() != nil {
		return
	}
	c.say(core.LevelError, format, args...)
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
	b.WriteString(boardBrief(c.proj.Dir))
	if w.role == RoleReviewer {
		b.WriteString(briefReview)
	} else {
		b.WriteString(workBrief(c.proj.Dir))
	}
	return b.String()
}
