package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DelegateEnv = "RIG_DELEGATE"

type DelegateInput struct {
	DB           DB
	Home         string
	Session      string
	StoreCwd     string
	Cwd          string
	Task         string
	Model        string
	Fetch        Fetch
	Spawn        Spawn
	WorkerCmd    []string
	SwapURL      string
	Timeout      time.Duration
	Sandbox      string
	SandboxBinds []string
	RigHome      string
	StateDir     string
	Allow        []string
	Now          func() time.Time
}

type DelegateResult struct {
	Exit     int
	Stdout   string
	Stderr   string
	TimedOut bool
	Duration time.Duration
	ID       string
	LogRel   string
	Started  string
	Note     string
}

func delegateInput(in DelegateInput) DelegateInput {
	if in.Now == nil {
		in.Now = time.Now
	}
	if in.SwapURL == "" {
		in.SwapURL = defaultSwapURL
	}
	return in
}

func delegateTimeout(t time.Duration) time.Duration {
	if t <= 0 {
		return DefaultRunTimeout
	}
	if t > DefaultRunTimeout {
		return DefaultRunTimeout
	}
	return t
}

func Delegate(in DelegateInput) (DelegateResult, error) {
	in = delegateInput(in)
	if in.Fetch == nil || in.Spawn == nil {
		return DelegateResult{}, fmt.Errorf("delegate: fetch and spawn seams are required")
	}

	lockFD, held, err := acquireLock(in.Home, "delegate:"+in.Session)
	if err != nil {
		return DelegateResult{}, fmt.Errorf("delegate: lock: %w", err)
	}
	if !held {
		return DelegateResult{}, fmt.Errorf("delegate: a delegation is already in flight (this session)")
	}
	defer releaseLock(lockFD)

	if os.Getenv(DelegateEnv) != "" {
		return DelegateResult{}, fmt.Errorf("delegate: a worker cannot delegate (RIG_DELEGATE is set — no recursion)")
	}

	st := busyState(in.Fetch, in.SwapURL, in.Model)
	switch st.kind {
	case "error":
		return DelegateResult{}, fmt.Errorf("delegate: busy check failed: %s", st.reason)
	case "busy":
		return DelegateResult{}, fmt.Errorf("delegate: the GPU is held by %s (busy:skip — no eviction from inside a turn)", st.names)
	}

	id, err := adHocCreate(context.Background(), in.DB, in)
	if err != nil {
		return DelegateResult{}, fmt.Errorf("delegate: record: %w", err)
	}

	workerCmd := in.WorkerCmd
	if len(workerCmd) == 0 {
		exe, err := os.Executable()
		if err != nil {
			return DelegateResult{}, fmt.Errorf("delegate: worker command: %w", err)
		}
		workerCmd = []string{exe}
	}
	prompt := in.Task + ReportBack
	allow := joinAllow(in.Allow)

	profile, err := SandboxProfile(in.Sandbox)
	if err != nil {
		return DelegateResult{}, fmt.Errorf("delegate: sandbox: %w", err)
	}
	var (
		argv    []string
		proxy   *SocketProxy
		homeEnv string
		refuse  string
		note    string
	)
	if profile == "off" {
		note = "sandbox off: the worker ran unjailed (the operator's choice)"
		argv = append(append([]string{}, workerCmd...),
			"-p", prompt,
			"-base-url", in.SwapURL+"/v1",
			"-model", in.Model)
		if allow != "" {
			argv = append(argv, "-allow", allow)
		}
	} else {
		argv, proxy, homeEnv, refuse, err = jailSpawn(in.toRunOpts(), in.Cwd, workerCmd, in.Model, prompt, allow)
		if err != nil {
			return DelegateResult{}, fmt.Errorf("delegate: jail: %w", err)
		}
		if refuse != "" {
			return DelegateResult{}, fmt.Errorf("delegate: %s", refuse)
		}
		defer proxy.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), delegateTimeout(in.Timeout))
	defer cancel()
	started := in.Now().UTC()
	startedStr := started.Format(time.RFC3339)

	prevHome, hadHome := os.LookupEnv("RIG_HOME")
	if homeEnv != "" {
		os.Setenv("RIG_HOME", homeEnv)
	}
	prevDel, hadDel := os.LookupEnv(DelegateEnv)
	os.Setenv(DelegateEnv, "1")
	defer func() {
		if homeEnv != "" {
			if hadHome {
				os.Setenv("RIG_HOME", prevHome)
			} else {
				os.Unsetenv("RIG_HOME")
			}
		}
		if hadDel {
			os.Setenv(DelegateEnv, prevDel)
		} else {
			os.Unsetenv(DelegateEnv)
		}
	}()

	res, err := in.Spawn(ctx, argv, in.Cwd)
	if err != nil {
		return DelegateResult{}, fmt.Errorf("delegate: spawn: %w", err)
	}
	ended := in.Now().UTC()
	durationMs := ended.Sub(started).Milliseconds()

	logName := strings.NewReplacer(":", "-", ".", "-").Replace(in.Now().UTC().Format("2006-01-02T15:04:05.000Z")) + ".log"
	logRel := filepath.Join("runs", ScopeDir(JobKey{Scope: "cwd", Hash: CwdHash(in.StoreCwd), ID: id}), id, logName)
	dir := filepath.Join(in.Home, "runs", ScopeDir(JobKey{Scope: "cwd", Hash: CwdHash(in.StoreCwd), ID: id}), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return DelegateResult{}, fmt.Errorf("delegate: log dir: %w", err)
	}
	content := fmt.Sprintf(
		"# rig-delegate run\nkey=%s\nstarted=%s\nexit=%d\nduration_ms=%d\n\n== stdout ==\n%s\n\n== stderr ==\n%s\n",
		id, startedStr, res.Exit, durationMs, res.Stdout, res.Stderr)
	if err := os.WriteFile(filepath.Join(dir, logName), []byte(content), 0o644); err != nil {
		return DelegateResult{}, fmt.Errorf("delegate: log write: %w", err)
	}
	if err := pruneLogs(dir, 20); err != nil {
		return DelegateResult{}, fmt.Errorf("delegate: log prune: %w", err)
	}

	status := "ok"
	if res.Exit != 0 {
		status = "fail"
	}
	exit := int64(res.Exit)
	duration := durationMs
	if _, err := RecordRun(context.Background(), in.DB, RunRecordInput{
		ID: id, Status: status, Exit: &exit, Duration: &duration,
		Log: logRel, Started: startedStr, Ended: ended.Format(time.RFC3339),
	}); err != nil {
		return DelegateResult{}, fmt.Errorf("delegate: record: %w", err)
	}

	return DelegateResult{
		Exit: res.Exit, Stdout: res.Stdout, Stderr: res.Stderr,
		TimedOut: res.TimedOut, Duration: ended.Sub(started),
		ID: id, LogRel: logRel, Started: startedStr, Note: note,
	}, nil
}

func (in DelegateInput) toRunOpts() RunOpts {
	return RunOpts{
		RigHome:      in.RigHome,
		Sandbox:      in.Sandbox,
		SandboxBinds: in.SandboxBinds,
		StateDir:     in.StateDir,
	}
}

func joinAllow(allow []string) string {
	var b strings.Builder
	for _, n := range allow {
		if n == "delegate" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(",")
		}
		b.WriteString(n)
	}
	return b.String()
}

func firstLine(s string) string {
	l := strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]
	l = strings.TrimSpace(l)
	if len(l) > 60 {
		l = l[:60] + "…"
	}
	return l
}

func adHocCreate(ctx context.Context, db DB, in DelegateInput) (string, error) {
	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx)
	if err != nil {
		return "", err
	}
	if err := maybeCompact(bound, tx, f, in.Session); err != nil {
		return "", err
	}
	id := f.mintID()
	at := in.Now().UTC().Format(time.RFC3339)
	argsJSON, _ := json.Marshal(map[string]any{
		"id": id, "name": "delegate:" + firstLine(in.Task), "prompt": in.Task,
		"cron": "once", "at": at, "cwd": in.Cwd, "model": in.Model, "busy": "skip",
	})
	seq, err := appendEvent(bound, f.maxSeq+1, "create", string(argsJSON), in.Session)
	if err != nil {
		return "", err
	}
	f.apply(eventRow{seq: seq, ts: nowRFC3339(), op: "create", args: string(argsJSON)})
	if err := rewrite(tx, f); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}
