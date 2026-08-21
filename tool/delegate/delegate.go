// Package delegate is the one-shot worker tool (SPEC_DELEGATE): spawn a
// headless worker on a task now, in a cwd, wait, and feed back its last
// message. It is one tool over the existing runner (the jail, the socket
// proxy, the busy rule, the worker command), a recorded run in the
// cwd-scope scheduler store under a minted ad-hoc key, and a resumable
// transcript in the state store. One in flight per session; a worker
// cannot delegate (the RIG_DELEGATE marker).
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

// Opts is the tool's wiring (the root's composition): the session's
// cwd-scope scheduler store and its home (the log dirs), the operator's
// rig home and state-store directory (the jail's resumable-transcript
// bind), the worker command (the root's self), the default worker model,
// the sandbox, and the operator's allow-list (the worker's omits
// delegate). Fetch and Spawn are the runner's seams, injected for tests.
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
	return "spawn a headless worker on a task now, in a cwd, wait, and return its last message. " +
		"Use it for a bounded sub-task whose result is a message, not a conversation: a long compute, " +
		"a sweep, a review — run on a separate worker row without threading the whole turn through it. " +
		"cwd defaults to the session's cwd and must be under it or the rig home; model defaults to " +
		"(default: " + a.DefaultModel + "); timeoutMs defaults to 10 minutes (ceiling the runner's 30). " +
		"A held GPU is a loud refusal naming the holder (busy:skip); one delegation in flight per session; " +
		"the worker's transcript is resumable with sessions resume <id>."
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

// canonicalCwd canonicalizes the cwd (absolute and clean, the file tools'
// shape) and refuses anything outside the session's cwd or the rig home,
// by name.
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

// findSession names the worker's session in the state store: the worker
// is a fresh -p one-shot, so its row is the newest started after the
// spawn (the operator's own session predates it).
func findSession(ctx context.Context, rigHome, cwd, started string) (string, error) {
	db, _, err := store.Open(state.StorePath(rigHome, cwd), state.Statements(), state.SchemaVersion)
	if err != nil {
		return "", fmt.Errorf("delegate: session store: %w", err)
	}
	defer db.DB.Close()
	after, err := time.Parse(time.RFC3339, started)
	if err != nil {
		return "", fmt.Errorf("delegate: spawn start: %w", err)
	}
	var id string
	var at time.Time
	err = db.DB.QueryRowContext(ctx,
		`SELECT "id", "started_at" FROM "sessions" WHERE "cwd" = $1 AND "started_at" >= $2 ORDER BY "started_at" DESC LIMIT 1`,
		cwd, after).Scan(&id, &at)
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
