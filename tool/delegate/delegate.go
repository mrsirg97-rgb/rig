package delegate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/pathguard"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/swarm/status"
)

const (
	outputCap          = 256 * 1024
	defaultTimeout     = 10 * time.Minute
	delegateTimeoutCap = 30 * time.Minute
)

type Opts struct {
	DB           sched.DB
	Home         string
	RigHome      string
	StateDir     string
	SwapURL      string
	WorkerCmd    []string
	DefaultModel string
	Slots        int
	Sandbox      string
	SandboxBinds []string
	Allow        []string
	Fetch        sched.Fetch
	Spawn        sched.Spawn
	Models       func() models.Table
	Notify       func(core.Event)
}

type workerState struct {
	task      string
	heartbeat time.Time
	state     string
}

func New(o Opts) core.Tool {
	a := &adapter{Opts: o, workers: map[int64]workerState{}}
	if o.Notify != nil {
		a.emitter = status.New(o.Notify)
	}
	return a
}

type adapter struct {
	Opts
	mu      sync.Mutex
	seq     int64
	workers map[int64]workerState
	emitter *status.Emitter
}

func (a *adapter) Name() string { return "delegate" }

func (a *adapter) Description() string {
	stance := "one in flight per session"
	if a.Slots > 1 {
		stance = fmt.Sprintf("up to %d in flight per session", a.Slots)
	}
	return "spawn a headless worker on a task now, wait, and return its last message. Guidelines: a bounded " +
		"sub-task whose result is a message — a long compute, a sweep, a review — never a conversation; " +
		"fan out: several delegate calls in one turn, " + stance + " and extras wait for a slot. Reply: the " +
		"worker's message plus a trailer (exit, duration, session id, log); a held GPU refuses naming the " +
		"holder. cwd must be under the session's cwd or the rig home; the model defaults to " + a.DefaultModel +
		"; the timeout to 10 minutes (ceiling 30)."
}

func (a *adapter) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task":      {"type": "string", "description": "the prompt the worker runs (required)"},
			"cwd":       {"type": "string", "description": "working directory (default the session's cwd; must be under it or the rig home)"},
			"model":     {"type": "string", "description": "worker model id (default ` + a.DefaultModel + `)"},
			"timeoutMs": {"type": "integer", "minimum": 1, "description": "timeout in ms (default 600000, ceiling 1800000)"},
			"stallMs":   {"type": "integer", "minimum": 1, "description": "stall window in ms: a worker writing nothing for longer is killed as hung (0 = off; the timeout stays the spend ceiling)"}
		},
		"required": ["task"]
	}`)
}

type args struct {
	Task      string `json:"task"`
	Cwd       string `json:"cwd,omitempty"`
	Model     string `json:"model,omitempty"`
	TimeoutMs int64  `json:"timeoutMs,omitempty"`
	StallMs   int64  `json:"stallMs,omitempty"`
}

func (a *adapter) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var g args
	if err := strictDecode(data, &g); err != nil {
		return "", fmt.Errorf("delegate: args: %w", err)
	}
	if strings.TrimSpace(g.Task) == "" {
		return "", errors.New("delegate: task is required")
	}
	id := a.begin(g.Task)
	if id > 0 {
		defer a.end(id)
	}
	session := "anon"
	if s, ok := core.SessionFrom(ctx); ok && s != nil {
		session = s.ID
	}
	sessionCwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("delegate: %v", err)
	}
	cwd := sessionCwd
	if g.Cwd != "" {
		cwd, err = pathguard.Within(g.Cwd, sessionCwd, a.RigHome)
		if err != nil {
			return "", fmt.Errorf("delegate: %w", err)
		}
	}
	model := a.DefaultModel
	if g.Model != "" {
		model = g.Model
	}
	remote, concurrency := false, 0
	if a.Models != nil {
		if row, ok := a.Models().Get(model); ok {
			remote, concurrency = row.Remote, row.Concurrency
		}
	}
	timeout := defaultTimeout
	if g.TimeoutMs > 0 {
		timeout = time.Duration(g.TimeoutMs) * time.Millisecond
		if timeout > delegateTimeoutCap {
			timeout = delegateTimeoutCap
		}
	}
	stall := time.Duration(0)
	if g.StallMs > 0 {
		stall = time.Duration(g.StallMs) * time.Millisecond
	}

	res, err := sched.Delegate(sched.DelegateInput{
		DB:            a.DB,
		Home:          a.Home,
		Session:       session,
		Cwd:           cwd,
		Context:       ctx,
		Task:          g.Task,
		Model:         model,
		WorkerSession: core.NewSession().ID,
		Slots:         a.Slots,
		Fetch:         a.Fetch,
		Spawn:         a.Spawn,
		Remote:        remote,
		Concurrency:   concurrency,
		WorkerCmd:     a.WorkerCmd,
		SwapURL:       a.SwapURL,
		Timeout:       timeout,
		Stall:         stall,
		Sandbox:       a.Sandbox,
		SandboxBinds:  a.SandboxBinds,
		RigHome:       a.RigHome,
		StateDir:      a.StateDir,
		Allow:         a.Allow,
		Observe:       a.observe(id),
	})
	if err != nil {
		return "", err
	}

	content := capOutput(res.Stdout)
	trailer := fmt.Sprintf("delegate: exit %d · %dms · session %s · log %s",
		res.Exit, res.Duration.Milliseconds(), res.SessionID, res.LogRel)
	if res.Note != "" {
		trailer += " · " + res.Note
	}
	content += "\n" + trailer

	switch {
	case res.Stalled:
		return content, fmt.Errorf("delegate: the worker stalled after %s (process tree killed)", res.Duration.Round(time.Millisecond))
	case res.TimedOut:
		return content, fmt.Errorf("delegate: the worker timed out after %s (process tree killed)", res.Duration.Round(time.Millisecond))
	case res.Exit != 0:
		return content, fmt.Errorf("delegate: the worker failed (exit %d)", res.Exit)
	}
	return content, nil
}

func capOutput(s string) string {
	if len(s) <= outputCap {
		return s
	}
	return s[:outputCap] + "\n[TRUNCATED: " + fmt.Sprintf("%d", len(s)) + " bytes total]"
}

func strictDecode(data json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func (a *adapter) begin(task string) int64 {
	if a.emitter == nil {
		return 0
	}
	a.mu.Lock()
	a.seq++
	id := a.seq
	a.workers[id] = workerState{task: firstLine(task), heartbeat: time.Now(), state: "running"}
	a.mu.Unlock()
	a.emit(false)
	return id
}

func (a *adapter) end(id int64) {
	if a.emitter == nil {
		return
	}
	a.mu.Lock()
	delete(a.workers, id)
	a.mu.Unlock()
	a.emit(true)
}

func (a *adapter) observe(id int64) func([]byte) {
	if a.emitter == nil {
		return nil
	}
	return func(p []byte) {
		a.mu.Lock()
		if w, ok := a.workers[id]; ok {
			w.heartbeat = time.Now()
			a.workers[id] = w
		}
		a.mu.Unlock()
		a.emit(false)
	}
}

func (a *adapter) emit(force bool) {
	if a.emitter == nil {
		return
	}
	if force {
		a.emitter.Force(a.snapshot)
	} else {
		a.emitter.Emit(a.snapshot)
	}
}

func (a *adapter) snapshot() core.SwarmStatus {
	a.mu.Lock()
	ws := make([]core.SwarmWorker, 0, len(a.workers))
	for id, w := range a.workers {
		ws = append(ws, core.SwarmWorker{ID: int(id), Role: "worker", Task: w.task, Heartbeat: w.heartbeat, State: w.state})
	}
	a.mu.Unlock()
	sort.Slice(ws, func(i, j int) bool { return ws[i].ID < ws[j].ID })
	return core.SwarmStatus{Workers: ws}
}

func firstLine(s string) string {
	l := strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]
	l = strings.TrimSpace(l)
	if len(l) > 60 {
		l = l[:60] + "…"
	}
	return l
}
