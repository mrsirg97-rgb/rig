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
	Sandbox      string
	SandboxBinds []string
	Allow        []string
	Fetch        sched.Fetch
	Spawn        sched.Spawn
	Models       func() models.Table
	Room         broadcast.Room
}

type workerState struct {
	task      string
	heartbeat time.Time
	state     string
}

func New(o Opts) core.Tool {
	a := &adapter{Definition: tool.Fill(tool.Def("delegate"), "{default_model}", o.DefaultModel), Opts: o, workers: map[int64]workerState{}}
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
	member  broadcast.Member
}

type args struct {
	Task      string `json:"task"`
	Workspace string `json:"workspace,omitempty"`
	Model     string `json:"model,omitempty"`
	TimeoutMs int64  `json:"timeoutMs,omitempty"`
}

func (a *adapter) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var g args
	if err := strictDecode(data, &g); err != nil {
		return "", fmt.Errorf("delegate: args: %w", err)
	}
	if strings.TrimSpace(g.Task) == "" {
		return "", errors.New("delegate: task is required")
	}
	member := a.begin(g.Task)
	if member != nil {
		defer a.end(member)
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
	if g.Workspace != "" {
		cwd, err = pathguard.Within(g.Workspace, sessionCwd, a.RigHome)
		if err != nil {
			return "", fmt.Errorf("delegate: %w", err)
		}
	}
	model := g.Model
	timeout := defaultTimeout
	if g.TimeoutMs > 0 {
		timeout = time.Duration(g.TimeoutMs) * time.Millisecond
		if timeout > delegateTimeoutCap {
			timeout = delegateTimeoutCap
		}
	}

	res, err := sched.Delegate(sched.DelegateInput{
		DB:            a.DB,
		Home:          a.Home,
		Session:       session,
		Cwd:           cwd,
		Task:          g.Task,
		Model:         model,
		WorkerSession: core.NewSession().ID,
		DefaultModel:  a.DefaultModel,
		Models:        a.Models,
		Fetch:         a.Fetch,
		Spawn:         a.Spawn,
		WorkerCmd:     a.WorkerCmd,
		SwapURL:       a.SwapURL,
		Timeout:       timeout,
		Sandbox:       a.Sandbox,
		SandboxBinds:  a.SandboxBinds,
		RigHome:       a.RigHome,
		StateDir:      a.StateDir,
		Allow:         a.Allow,
		Member:        member,
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

func (a *adapter) begin(task string) broadcast.Member {
	if a.member == nil {
		return nil
	}
	a.mu.Lock()
	a.seq++
	id := rig.MemberMinted - a.seq
	a.workers[id] = workerState{task: firstLine(task), heartbeat: time.Now(), state: "running"}
	a.mu.Unlock()
	a.emit()
	return a.Room.Add(id)
}

func (a *adapter) end(member broadcast.Member) {
	member.Leave()
	a.mu.Lock()
	delete(a.workers, member.Id())
	a.mu.Unlock()
	a.emit()
}

func (a *adapter) receive(err error, messages ...broadcast.Message) {
	if err != nil {
		return
	}
	beat := false
	a.mu.Lock()
	for _, m := range messages {
		w, ok := a.workers[m.Origin()]
		if m.Event() != nil || !ok {
			continue
		}
		w.heartbeat = time.Now()
		a.workers[m.Origin()] = w
		beat = true
	}
	a.mu.Unlock()
	if beat {
		a.emit()
	}
}

func (a *adapter) emit() {
	a.member.Publish(context.Background(), func(error) {}, broadcast.NewMessage(rig.MemberDelegate, true, a.snapshot()))
}

func (a *adapter) snapshot() core.SwarmStatus {
	a.mu.Lock()
	ws := make([]core.SwarmWorker, 0, len(a.workers))
	for id, w := range a.workers {
		ws = append(ws, core.SwarmWorker{ID: int(rig.MemberMinted - id), Role: "worker", Task: w.task, Heartbeat: w.heartbeat, State: w.state})
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
