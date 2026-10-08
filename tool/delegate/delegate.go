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

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/pathguard"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

const (
	outputCap = 256 * 1024

	noTimeout = time.Duration(-1)
)

type Opts struct {
	DB           sched.DB
	Home         string
	RigHome      string
	StateDir     string
	SwapURL      string
	WorkerCmd    []string
	DefaultModel string
	Sandbox      string
	SandboxBinds []string
	Allow        []string
	Fetch        sched.Fetch
	Spawn        sched.Spawn
	Models       func() models.Table
	Room         broadcast.Room
	Ctx          context.Context
	Await        bool
}

type workerState struct {
	n         int
	task      string
	heartbeat time.Time
	state     string
	tool      string
	toolAt    time.Time
}

type Delegate interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Run(ctx context.Context, task, workspace, model string) (string, error)

	StopAll()
}

func New(o Opts) Delegate {
	a := &adapter{Definition: tool.Def("delegate"), Opts: o, workers: map[int64]workerState{}, stops: map[int]context.CancelFunc{}}
	if o.Room != nil {
		a.member = o.Room.Add(rig.MemberDelegate)
		a.member.Subscribe(context.Background(), a.receive)
	}
	return a
}

type adapter struct {
	tool.Definition
	Opts
	mu      sync.Mutex
	seq     int64
	workers map[int64]workerState
	stops   map[int]context.CancelFunc
	member  broadcast.Member
}

type args struct {
	Task      string `json:"task"`
	Workspace string `json:"workspace,omitempty"`
	Model     string `json:"model,omitempty"`
}

func (a *adapter) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var g args
	if err := strictDecode(data, &g); err != nil {
		return "", fmt.Errorf("delegate: args: %w", err)
	}
	return a.Run(ctx, g.Task, g.Workspace, g.Model)
}

// Run hands the work off. Everything that could refuse it — an empty task, a
// workspace outside the guard, a model that is not resident, a jail that will
// not start — is still an error this turn; what moves to the next turn is the
// answer. The worker runs under the session's context, not the turn's, so the
// turn ending is not what kills it.
func (a *adapter) Run(ctx context.Context, task, workspace, model string) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", errors.New("delegate: task is required")
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
	if workspace != "" {
		cwd, err = pathguard.Within(workspace, sessionCwd, a.RigHome)
		if err != nil {
			return "", fmt.Errorf("delegate: %w", err)
		}
	}

	workerCtx, stop := context.WithCancel(a.session())
	member, n := a.begin(task, stop)
	del, err := sched.DelegateStart(sched.DelegateInput{
		DB:            a.DB,
		Home:          a.Home,
		Session:       session,
		Cwd:           cwd,
		Task:          task,
		Model:         model,
		WorkerSession: core.NewSession().ID,
		DefaultModel:  a.DefaultModel,
		Models:        a.Models,
		Fetch:         a.Fetch,
		Spawn:         a.Spawn,
		WorkerCmd:     a.WorkerCmd,
		SwapURL:       a.SwapURL,
		Timeout:       noTimeout,
		Sandbox:       a.Sandbox,
		SandboxBinds:  a.SandboxBinds,
		RigHome:       a.RigHome,
		StateDir:      a.StateDir,
		Allow:         doingAllow(a.Allow),
		Member:        member,
		SpawnCtx:      workerCtx,
	})
	if err != nil {
		stop()
		a.end(member, n)
		return "", err
	}

	if a.Await {
		res, err := a.settle(member, n, task, del)
		if err != nil {
			return "", err
		}
		if res.Exit != 0 {
			return res.Content, fmt.Errorf("delegate: the worker failed (exit %d)", res.Exit)
		}
		return res.Content, nil
	}

	go func() { _, _ = a.settle(member, n, task, del) }()

	line := fmt.Sprintf("delegate: worker #%d started · session %s · log %s", n, del.Session, del.Log)
	if del.Note != "" {
		line += " · " + del.Note
	}
	return line, nil
}

func (a *adapter) session() context.Context {
	if a.Ctx == nil {
		return context.Background()
	}
	return a.Ctx
}

type settled struct {
	Content  string
	Exit     int
	Duration time.Duration
	Session  string
	Log      string
}

// settle waits the worker out, clears its band row, and publishes the return as
// core.WorkerDone. The content is the same text the synchronous tool result
// always was: the worker's stdout, capped, with the trailer naming its death.
// The error is the runner's, the one the blocking shape used to return; a
// handed-off worker's failure is its return's exit code, not an error here —
// and a worker that never ran carries the fault as its content, since there is
// no stdout to cap.
func (a *adapter) settle(member broadcast.Member, n int, task string, del sched.Delegation) (settled, error) {
	res, err := del.Wait()
	out := settled{
		Exit: res.Exit, Duration: res.Duration,
		Session: firstNonEmpty(res.SessionID, del.Session), Log: firstNonEmpty(res.LogRel, del.Log),
	}
	if err == nil {
		out.Content = capOutput(res.Stdout) + "\n" + fmt.Sprintf("delegate: exit %d · %dms · session %s · log %s",
			res.Exit, res.Duration.Milliseconds(), out.Session, out.Log)
		if res.Note != "" {
			out.Content += " · " + res.Note
		}
	} else {
		out.Exit = -1
		// A worker that never ran has no stdout to cap: the fault itself is
		// its return's content, because a late failure is still an answer.
		out.Content = err.Error()
	}
	a.end(member, n)
	if a.member != nil {
		done := core.WorkerDone{
			N: n, Task: firstLine(task), Content: out.Content, Exit: out.Exit,
			Duration: out.Duration, Session: out.Session, Log: out.Log,
		}
		a.member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(rig.MemberDelegate, true, done))
	}
	return out, err
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
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

func (a *adapter) begin(task string, stop context.CancelFunc) (broadcast.Member, int) {
	a.mu.Lock()
	a.seq++
	n := int(a.seq)
	a.stops[n] = stop
	a.mu.Unlock()
	if a.member == nil {
		return nil, n
	}
	member := a.Room.Mint()
	a.mu.Lock()
	a.workers[member.Id()] = workerState{n: n, task: firstLine(task), heartbeat: time.Now(), state: "running"}
	a.mu.Unlock()
	a.emit()
	return member, n
}

func (a *adapter) end(member broadcast.Member, n int) {
	a.mu.Lock()
	stop := a.stops[n]
	delete(a.stops, n)
	a.mu.Unlock()
	// The worker's life is over — end is reached only after Wait returned or
	// before the spawn began — so cancelling releases the context the session
	// would otherwise carry until it ends. Cancel is idempotent, and StopAll
	// races nothing by reading a map the entry has already left.
	if stop != nil {
		stop()
	}
	if member == nil {
		return
	}
	member.Leave()
	a.mu.Lock()
	delete(a.workers, member.Id())
	a.mu.Unlock()
	a.emit()
}

// StopAll ends every running worker. It is the idle interrupt: the gesture the
// operator makes when there is no turn to interrupt and the workers are the
// only thing still running.
func (a *adapter) StopAll() {
	a.mu.Lock()
	stops := make([]context.CancelFunc, 0, len(a.stops))
	for _, stop := range a.stops {
		stops = append(stops, stop)
	}
	a.mu.Unlock()
	for _, stop := range stops {
		if stop != nil {
			stop()
		}
	}
}

func (a *adapter) receive(err error, messages ...broadcast.Message) {
	if err != nil {
		return
	}
	changed := false
	a.mu.Lock()
	for _, m := range messages {
		w, ok := a.workers[m.Origin()]
		if !ok {
			continue
		}
		switch ev := m.Event().(type) {
		case nil:
			w.heartbeat = time.Now()
		case core.ToolStart:
			w.heartbeat, w.tool, w.toolAt = time.Now(), ev.BoundedCall(), time.Now()
		default:
			continue
		}
		a.workers[m.Origin()] = w
		changed = true
	}
	a.mu.Unlock()
	if changed {
		a.emit()
	}
}

func (a *adapter) emit() {
	a.member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(rig.MemberDelegate, true, a.snapshot()))
}

func (a *adapter) snapshot() core.SwarmStatus {
	a.mu.Lock()
	ws := make([]core.SwarmWorker, 0, len(a.workers))
	for _, w := range a.workers {
		ws = append(ws, core.SwarmWorker{
			ID: w.n, Role: "delegate", Task: w.task, Heartbeat: w.heartbeat,
			State: w.state, Tool: w.tool, ToolAt: w.toolAt,
		})
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
