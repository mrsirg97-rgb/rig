package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/pathguard"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v3/tool"
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
	Cap          evt.Cap
}

type holding struct {
	stop func()
}

type Delegate interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Run(ctx context.Context, task, workspace, model string) (string, error)

	Command(ctx context.Context, command, workspace string, bound time.Duration) (string, error)

	Submit(ctx context.Context, kind, name string, bound time.Duration, work func(ctx context.Context) (string, int)) (string, error)

	StopAll()
}

func New(o Opts) Delegate {
	if o.Cap == nil {
		o.Cap = evt.NewCap(nil, 0)
	}
	a := &adapter{Definition: tool.Def("delegate"), Opts: o, workers: map[int64]workerState{}, stops: map[int]holding{}, subs: map[int]*hand{}}
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
	stops   map[int]holding
	subs    map[int]*hand
	member  broadcast.Member
}

type hand struct {
	sub  evt.Submission
	wait chan struct{}
}

func newHand() *hand {
	return &hand{wait: make(chan struct{})}
}

func (h *hand) offer(sub evt.Submission) {
	h.sub = sub
	close(h.wait)
}

func (h *hand) claim() evt.Submission {
	<-h.wait
	return h.sub
}

func (h *hand) drop() evt.Submission {
	select {
	case <-h.wait:
		return h.sub
	default:
		return nil
	}
}

type args struct {
	Task      string `json:"task"`
	Command   string `json:"command"`
	Workspace string `json:"workspace,omitempty"`
	Model     string `json:"model,omitempty"`
	TimeoutMs *int64 `json:"timeoutMs"`
}

type delegateCall struct {
	task      string
	command   string
	workspace string
	model     string
	bound     time.Duration
}

func (a *adapter) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var g args
	if err := tool.StrictDecode(data, &g); err != nil {
		return "", fmt.Errorf("delegate: args: %w", err)
	}
	bound, err := tool.BoundMs("delegate", g.TimeoutMs)
	if err != nil {
		return "", err
	}
	if g.Command != "" {
		if strings.TrimSpace(g.Task) != "" {
			return "", errors.New("delegate: take task or command (one worker per call)")
		}
		if g.Model != "" {
			return "", errors.New("delegate: a command worker takes no model")
		}
		return a.runCommand(ctx, g.Command, g.Workspace, bound)
	}
	if g.TimeoutMs != nil {
		return "", errors.New("delegate: a model worker has no clock (2.12.7) — command workers take timeoutMs")
	}
	return a.Run(ctx, g.Task, g.Workspace, g.Model)
}

func (a *adapter) Run(ctx context.Context, task, workspace, model string) (string, error) {
	return a.run(ctx, delegateCall{task: task, workspace: workspace, model: model})
}

func (a *adapter) Command(ctx context.Context, command, workspace string, bound time.Duration) (string, error) {
	return a.runCommand(ctx, command, workspace, bound)
}

func (a *adapter) Submit(ctx context.Context, kind, name string, bound time.Duration, work func(ctx context.Context) (string, int)) (string, error) {
	if kind != core.KindKernel && kind != core.KindFetch {
		return "", fmt.Errorf("delegate: unknown worker kind %q", kind)
	}
	session := "anon"
	if s, ok := core.SessionFrom(ctx); ok && s != nil {
		session = s.ID
	}
	a.mu.Lock()
	a.seq++
	n := int(a.seq)
	a.mu.Unlock()
	in := sched.DelegateInput{
		DB: a.DB, Home: a.Home, Session: session, Task: name, Work: work,
		SpawnCtx: a.session(),
	}
	if bound > 0 {
		in.Timeout = bound
	}
	if cwd, err := os.Getwd(); err == nil {
		in.Cwd = cwd
	}
	del, err := sched.DelegateStart(in)
	if err != nil {
		return "", fmt.Errorf("delegate: %w", err)
	}
	if a.Await {
		res, err := del.Wait()
		if err != nil {
			return "", err
		}
		content := capOutput(res.Stdout) + "\n" + tool.ExitTrailer(kind, res.Exit, res.Duration, del.ID, res.LogRel, res.TimedOut, del.Bound)
		if res.Exit != 0 {
			return content, fmt.Errorf("delegate: the worker failed (exit %d)", res.Exit)
		}
		return content, nil
	}
	go func() {
		res, err := del.Wait()
		content := capOutput(res.Stdout) + "\n" + tool.ExitTrailer(kind, res.Exit, res.Duration, del.ID, res.LogRel, res.TimedOut, del.Bound)
		if err != nil {
			content = err.Error()
		}
		if a.member != nil {
			a.member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(rig.MemberDelegate, true, core.WorkerDone{
				N: n, Task: tool.FirstLine(name), Content: content, Exit: res.Exit, Duration: res.Duration,
				Log: res.LogRel, Kind: kind, RunID: del.ID,
			}))
		}
	}()
	return fmt.Sprintf("delegate: worker #%d started · run %s · log %s", n, del.ID, del.Log), nil
}

func (a *adapter) runCommand(ctx context.Context, command, workspace string, bound time.Duration) (string, error) {
	return a.run(ctx, delegateCall{command: command, workspace: workspace, bound: bound})
}

func (a *adapter) run(ctx context.Context, c delegateCall) (string, error) {
	if c.command != "" {
		if strings.TrimSpace(c.command) == "" {
			return "", errors.New("delegate: command is required")
		}
	} else if strings.TrimSpace(c.task) == "" {
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
	if c.workspace != "" {
		cwd, err = pathguard.Within(c.workspace, sessionCwd, a.RigHome)
		if err != nil {
			return "", fmt.Errorf("delegate: %w", err)
		}
	}

	workerCtx, stop := context.WithCancel(a.session())
	name := c.task
	if c.command != "" {
		name = c.command
	}
	member, n := a.mint(stop)
	queued := newHand()
	a.mu.Lock()
	a.subs[n] = queued
	a.stops[n] = holding{stop: func() {
		stop()
		if sub := queued.drop(); sub != nil && !sub.Started() {
			sub.Stop()
		}
	}}
	a.mu.Unlock()
	in := sched.DelegateInput{
		DB:            a.DB,
		Home:          a.Home,
		Session:       session,
		Cwd:           cwd,
		Task:          c.task,
		Command:       c.command,
		Model:         c.model,
		WorkerSession: workerSessionID(c),
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
	}
	if c.bound > 0 {
		in.Timeout = c.bound
	}

	var line string
	var runErr error
	var del sched.Delegation
	var ran atomic.Bool
	work := evt.Func(func(gate context.Context) {
		if !ran.CompareAndSwap(false, true) {
			return
		}
		if gate.Err() != nil {
			a.fail(member, n, workerName(in), fmt.Errorf("delegate: worker #%d stopped while queued", n))
			runErr = fmt.Errorf("delegate: %w", gate.Err())
			return
		}
		a.running(member, n, name)
		var err error
		del, err = sched.DelegateStart(in)
		if err != nil {
			if !a.Await {
				a.release(n)
				a.fail(member, n, workerName(in), err)
			} else {
				a.end(member, n)
			}
			runErr = err
			return
		}
		if a.Await {
			watch := a.claim(n)
			res, serr := a.settle(member, n, in, del)
			line = res.Content
			runErr = serr
			if serr == nil && res.Exit != 0 {
				runErr = fmt.Errorf("delegate: the worker failed (exit %d)", res.Exit)
			}
			watch.Release()
			return
		}
		line = fmt.Sprintf("delegate: worker #%d started · session %s · log %s", n, del.Session, del.Log)
		if c.command != "" {
			line = fmt.Sprintf("delegate: worker #%d started · run %s · log %s", n, del.ID, del.Log)
		}
		if del.Note != "" {
			line += " · " + del.Note
		}
		watch := a.claim(n)
		go func() {
			defer watch.Release()
			_, _ = a.settle(member, n, in, del)
		}()
	})
	gate := workerCtx
	if a.Await {
		gate = ctx
	}
	sub := a.Cap.Submit(gate, work, rig.PriorityFleet)
	queued.offer(sub)
	state := "running"
	if !sub.Started() {
		state = "queued"
	}
	a.track(member, n, name, state)
	if !sub.Started() {
		if a.Await {
			<-sub.Settled()
			return line, runErr
		}
		return fmt.Sprintf("delegate: worker #%d queued · %d workers run at once (settings maxWorkers); it starts when one returns", n, a.Cap.Size()), nil
	}
	select {
	case <-sub.Settled():
		work.Resolve(ctx)
	default:
		<-sub.Settled()
	}
	return line, runErr
}

func workerSessionID(c delegateCall) string {
	if c.command != "" {
		return ""
	}
	return core.NewSession().ID
}

func (a *adapter) fail(member broadcast.Member, n int, task string, err error) {
	a.end(member, n)
	if a.member != nil {
		done := core.WorkerDone{N: n, Task: tool.FirstLine(task), Content: err.Error(), Exit: -1}
		a.member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(rig.MemberDelegate, true, done))
	}
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

func (a *adapter) settle(member broadcast.Member, n int, in sched.DelegateInput, del sched.Delegation) (settled, error) {
	res, err := del.Wait()
	kind := ""
	name := workerName(in)
	if in.Command != "" {
		kind = core.KindCommand
	}
	out := settled{
		Exit: res.Exit, Duration: res.Duration,
		Session: firstNonEmpty(res.SessionID, del.Session), Log: firstNonEmpty(res.LogRel, del.Log),
	}
	if err == nil {
		content := capOutput(res.Stdout)
		if kind == "" {
			content += "\n" + fmt.Sprintf("delegate: exit %d · %dms · session %s · log %s",
				res.Exit, res.Duration.Milliseconds(), out.Session, out.Log)
		} else {
			content += "\n" + tool.ExitTrailer("delegate", res.Exit, res.Duration, del.ID, out.Log, res.TimedOut, del.Bound)
		}
		if res.Note != "" {
			content += " · " + res.Note
		}
		out.Content = content
	} else {
		out.Exit = -1
		out.Content = err.Error()
	}
	a.end(member, n)
	if a.member != nil {
		done := core.WorkerDone{
			N: n, Task: tool.FirstLine(name), Content: out.Content, Exit: out.Exit,
			Duration: out.Duration, Session: out.Session, Log: out.Log,
			Kind: kind, RunID: del.ID,
		}
		a.member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(rig.MemberDelegate, true, done))
	}
	return out, err
}

func workerName(in sched.DelegateInput) string {
	return firstNonEmpty(in.Command, in.Task)
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
	cut := outputCap
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n[TRUNCATED: " + fmt.Sprintf("%d", len(s)) + " bytes total]"
}

func (a *adapter) mint(stop context.CancelFunc) (broadcast.Member, int) {
	a.mu.Lock()
	a.seq++
	n := int(a.seq)
	a.stops[n] = holding{stop: stop}
	a.mu.Unlock()
	if a.member == nil {
		return nil, n
	}
	return a.Room.Mint(), n
}

func (a *adapter) end(member broadcast.Member, n int) {
	a.mu.Lock()
	h := a.stops[n]
	delete(a.stops, n)
	delete(a.subs, n)
	a.mu.Unlock()
	if h.stop != nil {
		h.stop()
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

func (a *adapter) claim(n int) evt.Submission {
	a.mu.Lock()
	h := a.subs[n]
	delete(a.subs, n)
	a.mu.Unlock()
	if h == nil {
		return nil
	}
	return h.claim()
}

func (a *adapter) release(n int) {
	sub := a.claim(n)
	if sub != nil {
		sub.Release()
	}
}

func (a *adapter) StopAll() {
	a.mu.Lock()
	stops := make([]func(), 0, len(a.stops))
	for _, h := range a.stops {
		stops = append(stops, h.stop)
	}
	a.mu.Unlock()
	for _, stop := range stops {
		if stop != nil {
			stop()
		}
	}
}
