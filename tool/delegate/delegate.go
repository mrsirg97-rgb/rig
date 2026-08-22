package delegate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/store/state"
)

const outputCap = 256 * 1024

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
}

func New(o Opts) core.Tool { return adapter{o} }

type adapter struct{ Opts }

func (a adapter) Name() string { return "delegate" }

func (a adapter) Description() string {
	return "spawn a headless worker on a task now, wait, and return its last message. Guidelines: a bounded " +
		"sub-task whose result is a message — a long compute, a sweep, a review — never a conversation; one " +
		"in flight per session. Reply: the worker's message plus a trailer (exit, duration, session id, log); " +
		"a held GPU refuses naming the holder. cwd must be under the session's cwd or the rig home; the model " +
		"defaults to " + a.DefaultModel + "; the timeout to 10 minutes (ceiling 30)."
}

func (a adapter) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task":      {"type": "string", "description": "the prompt the worker runs (required)"},
			"cwd":       {"type": "string", "description": "working directory (default the session's cwd; must be under it or the rig home)"},
			"model":     {"type": "string", "description": "worker model id (default ` + a.DefaultModel + `)"},
			"timeoutMs": {"type": "integer", "minimum": 1, "description": "timeout in ms (default 600000, ceiling 1800000)"}
		},
		"required": ["task"]
	}`)
}

type args struct {
	Task      string `json:"task"`
	Cwd       string `json:"cwd,omitempty"`
	Model     string `json:"model,omitempty"`
	TimeoutMs int64  `json:"timeoutMs,omitempty"`
}

func (a adapter) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var g args
	if err := strictDecode(data, &g); err != nil {
		return "", fmt.Errorf("delegate: args: %w", err)
	}
	if strings.TrimSpace(g.Task) == "" {
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
	if g.Cwd != "" {
		cwd, err = canonicalCwd(g.Cwd, sessionCwd, a.RigHome)
		if err != nil {
			return "", err
		}
	}
	model := a.DefaultModel
	if g.Model != "" {
		model = g.Model
	}
	timeout := 10 * time.Minute
	if g.TimeoutMs > 0 {
		timeout = time.Duration(g.TimeoutMs) * time.Millisecond
	}

	res, err := sched.Delegate(sched.DelegateInput{
		DB:           a.DB,
		Home:         a.Home,
		Session:      session,
		StoreCwd:     sessionCwd,
		Cwd:          cwd,
		Task:         g.Task,
		Model:        model,
		Fetch:        a.Fetch,
		Spawn:        a.Spawn,
		WorkerCmd:    a.WorkerCmd,
		SwapURL:      a.SwapURL,
		Timeout:      timeout,
		Sandbox:      a.Sandbox,
		SandboxBinds: a.SandboxBinds,
		RigHome:      a.RigHome,
		StateDir:     a.StateDir,
		Allow:        a.Allow,
	})
	if err != nil {
		return "", err
	}

	sessionID, err := findSession(ctx, a.RigHome, cwd, res.Started)
	if err != nil {
		return "", err
	}
	content := capOutput(res.Stdout)
	trailer := fmt.Sprintf("delegate: exit %d · %dms · session %s · log %s",
		res.Exit, res.Duration.Milliseconds(), sessionID, res.LogRel)
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

func canonicalCwd(path, sessionCwd, rigHome string) (string, error) {
	cwd := path
	if abs, err := filepath.Abs(path); err == nil {
		cwd = abs
	}
	cwd = filepath.Clean(cwd)
	under := func(root string) bool {
		if root == "" {
			return false
		}
		return cwd == root || strings.HasPrefix(cwd, root+string(filepath.Separator))
	}
	if under(sessionCwd) || under(rigHome) {
		return cwd, nil
	}
	return "", fmt.Errorf("delegate: cwd %q is outside the session's cwd (%s) and the rig home (%s)", cwd, sessionCwd, rigHome)
}

func findSession(ctx context.Context, rigHome, cwd, started string) (string, error) {
	db, _, _, err := store.Open(state.StorePath(rigHome, cwd), state.Statements(), state.SchemaVersion)
	if err != nil {
		return "", fmt.Errorf("delegate: session store: %w", err)
	}
	defer db.DB.Close()
	after, err := time.Parse(time.RFC3339, started)
	if err != nil {
		return "", fmt.Errorf("delegate: spawn start: %w", err)
	}
	id, err := state.NewestSince(ctx, db, cwd, after)
	if err != nil {
		return "", fmt.Errorf("delegate: no worker session recorded (%v)", err)
	}
	return id, nil
}

func strictDecode(data json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
