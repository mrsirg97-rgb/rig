package swarm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
	"github.com/mrsirg97-rgb/rig/v2/swarm/status"
)

const (
	RoleWorker   = "worker"
	RoleReviewer = "reviewer"

	StateRunning = "running"
	StateExited  = "exited"

	MaxWorkers = 16

	emptyClaimsBeforeExit = 3

	defaultPoll = 2 * time.Second

	maxVerdictReason = todostore.MaxNoteLen

	workerTimeout = 2 * time.Hour
	workerStall   = 10 * time.Minute

	heartbeatLine = "rig: heartbeat"
)

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
	Frontend      func() core.Frontend
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
	budget    float64
	spent     float64
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
	c := &Controller{opts: o, retries: map[string]int{}, rejects: map[string]int{}}
	if o.Frontend != nil {
		c.emitter = status.New(c.safeNotify)
	}
	return c
}

func (c *Controller) safeNotify(ev core.Event) {
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(os.Stderr, "swarm: notify: recovered from panic: %v\n", p)
		}
	}()
	resolve := c.opts.Frontend
	if resolve == nil {
		return
	}
	if fe := resolve(); fe != nil {
		fe.Notify(ev)
	}
}

func (c *Controller) addSpent(v float64) {
	if v <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spent += v
}

func (c *Controller) atBudget() bool {
	spent, budget := c.budgetState()
	return budget > 0 && spent >= budget
}

func (c *Controller) budgetState() (float64, float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.spent, c.budget
}

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
		c.ctx, c.cancel = context.WithCancel(ctx)
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
		}
		c.workers = append(c.workers, w)
		c.wg.Add(1)
		go c.run(w)
	}
	return fmt.Sprintf("swarm: added %d %s (role %s · model %s)", in.Count, plural(in.Count, "agent"), role, model), nil
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
	return fmt.Sprintf("swarm: stopped %d %s", count, plural(count, "agent")), nil
}
