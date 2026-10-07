package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/models"
)

const DelegateEnv = "RIG_DELEGATE"

type DelegateInput struct {
	DB            DB
	Home          string
	Session       string
	Cwd           string
	Task          string
	Model         string
	WorkerSession string
	Fetch         Fetch
	Spawn         Spawn
	WorkerCmd     []string
	SwapURL       string
	Timeout       time.Duration
	Sandbox       string
	SandboxBinds  []string
	RigHome       string
	StateDir      string
	Allow         []string
	Bare          bool
	LandlockABI   func() (int, error)
	Now           func() time.Time
	DefaultModel  string
	Models        func() models.Table
	Member        broadcast.Member
	SpawnCtx      context.Context
}

const NoToolsAllow = "none"

type DelegateResult struct {
	Model     string
	Exit      int
	Stdout    string
	Stderr    string
	TimedOut  bool
	Signal    syscall.Signal
	Duration  time.Duration
	ID        string
	LogRel    string
	Started   string
	SessionID string
	Note      string
	Reason    string
	Cost      float64
}

func (res DelegateResult) FireError(home string) error {
	if res.Exit == 0 && !res.TimedOut {
		return nil
	}
	msg := fmt.Sprintf("the review fire ended exit %d (timed out %v)", res.Exit, res.TimedOut)
	if res.Reason != "" {
		msg += ": " + res.Reason
	}
	if res.LogRel != "" {
		msg += "; the run log: " + filepath.Join(home, res.LogRel)
	}
	return errors.New(msg)
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

const maxDelegateTimeout = 24 * time.Hour

func resolveWorkerModel(in DelegateInput) (string, string, error) {
	if in.Model != "" {
		return in.Model, in.Model, nil
	}
	table := models.Table{}
	if in.Models != nil {
		table = in.Models()
	}
	row, canonical, err := resolveResidentModel(in.Fetch, in.SwapURL, table)
	if err != nil {
		var noRow noRowError
		if !errors.As(err, &noRow) {
			row, canonical = "", ""
		} else {
			return "", "", err
		}
	}
	if row != "" {
		return row, canonical, nil
	}
	if in.DefaultModel == "" {
		return "", "", fmt.Errorf("no model: the swap has nothing resident and no default is wired (the session's model is the default)")
	}
	return in.DefaultModel, in.DefaultModel, nil
}

func isRemoteRow(in DelegateInput) bool {
	if in.Models == nil {
		return false
	}
	row, ok := in.Models().Get(in.Model)
	return ok && row.Remote
}

func delegateTimeout(t time.Duration) time.Duration {
	if t < 0 {
		return 0
	}
	if t == 0 {
		return DefaultRunTimeout
	}
	if t > maxDelegateTimeout {
		return maxDelegateTimeout
	}
	return t
}

// Delegation is a worker that has been handed off: everything that could refuse
// it (the seams, the recursion guard, the residency gate, the record, the jail)
// already passed, and the process is running. Wait collects the outcome; it is
// the only wait, and the delegate tool — which answers its turn immediately —
// is simply the caller that does not call it until the worker is done.
type Delegation struct {
	ID      string
	Session string
	Log     string
	Model   string
	Note    string
	done    chan delegateOutcome
}

type delegateOutcome struct {
	res DelegateResult
	err error
}

// Wait blocks until the worker exits and its run log and scheduler record are
// on disk. Delegate is Start and Wait; there is no second path.
func (d Delegation) Wait() (DelegateResult, error) {
	if d.done == nil {
		return DelegateResult{}, fmt.Errorf("delegate: nothing was delegated (wait on a delegation only after DelegateStart accepted it)")
	}
	out, ok := <-d.done
	if !ok {
		return DelegateResult{}, fmt.Errorf("delegate: the delegation was abandoned")
	}
	return out.res, out.err
}

func Delegate(in DelegateInput) (DelegateResult, error) {
	d, err := DelegateStart(in)
	if err != nil {
		return DelegateResult{}, err
	}
	return d.Wait()
}

func DelegateStart(in DelegateInput) (Delegation, error) {
	in = delegateInput(in)
	if in.Fetch == nil || in.Spawn == nil {
		return Delegation{}, fmt.Errorf("delegate: fetch and spawn seams are required")
	}
	if in.WorkerSession == "" {
		return Delegation{}, fmt.Errorf("delegate: worker session is required")
	}

	if os.Getenv(DelegateEnv) != "" {
		return Delegation{}, fmt.Errorf("delegate: a worker cannot delegate (RIG_DELEGATE is set — no recursion)")
	}

	model, gateModel, err := resolveWorkerModel(in)
	if err != nil {
		return Delegation{}, fmt.Errorf("delegate: %w", err)
	}
	in.Model = model
	if !isRemoteRow(in) {
		if err := gateOnce(in.Fetch, in.SwapURL, gateModel); err != nil {
			return Delegation{}, fmt.Errorf("delegate: %w", err)
		}
	}

	id, err := adHocCreate(context.Background(), in.DB, in)
	if err != nil {
		return Delegation{}, fmt.Errorf("delegate: record: %w", err)
	}

	workerCmd := in.WorkerCmd
	if len(workerCmd) == 0 {
		exe, err := os.Executable()
		if err != nil {
			return Delegation{}, fmt.Errorf("delegate: worker command: %w", err)
		}
		workerCmd = []string{exe}
	}
	prompt := in.Task + ReportBack(in.Cwd)
	allow := joinAllow(in.Allow)
	if in.Bare {
		prompt = in.Task
		if allow == "" {
			allow = NoToolsAllow
		}
	}

	profile, err := SandboxProfile(in.Sandbox)
	if err != nil {
		return Delegation{}, fmt.Errorf("delegate: sandbox: %w", err)
	}
	var (
		argv     []string
		proxy    *SocketProxy
		pipe     *fleetPipe
		refuse   string
		note     string
		spawnEnv []string
	)
	release := func() {
		if pipe != nil {
			pipe.close()
		}
		if proxy != nil {
			proxy.Close()
		}
	}
	if profile == "off" {
		note = "sandbox off: the worker ran unjailed (the operator's choice)"
		argv = append(append([]string{}, workerCmd...),
			"-p", PromptStdin,
			"-session-id", in.WorkerSession,
			"-base-url", in.SwapURL+"/v1",
			"-model", in.Model)
		if allow != "" {
			argv = append(argv, "-allow", allow)
		}
		spawnEnv = append(os.Environ(), DelegateEnv+"=1")
	} else {
		argv, proxy, spawnEnv, refuse, err = spawnJailed(in.toRunOpts(), profile, in.Cwd, workerCmd, in.Model, prompt, allow, in.WorkerSession, DelegateEnv+"=1")
		if err != nil {
			release()
			return Delegation{}, fmt.Errorf("delegate: jail: %w", err)
		}
		if refuse != "" {
			release()
			return Delegation{}, fmt.Errorf("delegate: %s", refuse)
		}
	}

	base := in.SpawnCtx
	if base == nil {
		base = context.Background()
	}
	started := in.Now().UTC()
	startedStr := started.Format(time.RFC3339)
	logName := strings.NewReplacer(":", "-", ".", "-").Replace(started.Format("2006-01-02T15:04:05.000Z")) + ".log"
	logRel := filepath.Join("runs", id, logName)

	if in.Member != nil {
		pipe, err = openFleet(base, in.Member.Id(), func(messages ...broadcast.Message) {
			in.Member.Publish(base, func(error) {}, messages...)
		})
		if err != nil {
			release()
			return Delegation{}, fmt.Errorf("delegate: fleet: %w", err)
		}
		spawnEnv = append(spawnEnv, pipe.env())
	}

	done := make(chan delegateOutcome, 1)
	go func() {
		ctx := base
		var cancel context.CancelFunc
		if limit := delegateTimeout(in.Timeout); limit > 0 {
			ctx, cancel = context.WithTimeout(base, limit)
		}
		if pipe != nil {
			ctx = pipe.into(ctx)
		}
		res, err := in.Spawn(WithPrompt(ctx, prompt), argv, in.Cwd, spawnEnv, nil)
		var outcome delegateOutcome
		if err != nil {
			outcome.err = fmt.Errorf("delegate: spawn: %w", err)
		} else {
			ended := in.Now().UTC()
			outcome.res, outcome.err = finishDelegate(in, id, started, startedStr, ended, logRel, model, note, res, ctx)
		}
		if cancel != nil {
			cancel()
		}
		release()
		done <- outcome
	}()

	return Delegation{
		ID: id, Session: in.WorkerSession, Log: logRel, Model: model, Note: note,
		done: done,
	}, nil
}

func finishDelegate(in DelegateInput, id string, started time.Time, startedStr string, ended time.Time, logRel, model, note string, res SpawnResult, ctx context.Context) (DelegateResult, error) {
	durationMs := ended.Sub(started).Milliseconds()
	logName := filepath.Base(logRel)
	dir := filepath.Join(in.Home, "runs", id)
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

	reason := spawnReason(ctx, res, false)
	status := "ok"
	if res.Exit != 0 {
		status = "fail"
	}
	exit := int64(res.Exit)
	duration := durationMs
	cost := workerSessionCost(in.RigHome, in.Cwd, in.WorkerSession)
	var costPtr *float64
	if cost > 0 {
		costPtr = &cost
	}
	if _, err := RecordRun(context.Background(), in.DB, RunRecordInput{
		ID: id, Status: status, Exit: &exit, Duration: &duration,
		Log: logRel, Started: startedStr, Ended: ended.Format(time.RFC3339), Cost: costPtr,
		Reason: reason,
	}); err != nil {
		return DelegateResult{}, fmt.Errorf("delegate: record: %w", err)
	}

	return DelegateResult{
		Model: model,
		Exit:  res.Exit, Stdout: res.Stdout, Stderr: res.Stderr,
		TimedOut: res.TimedOut, Signal: res.Signal, Duration: ended.Sub(started),
		ID: id, LogRel: logRel, Started: startedStr, SessionID: in.WorkerSession, Note: note,
		Reason: reason, Cost: cost,
	}, nil
}

func (in DelegateInput) toRunOpts() RunOpts {
	return RunOpts{
		RigHome:      in.RigHome,
		Sandbox:      in.Sandbox,
		SandboxBinds: in.SandboxBinds,
		StateDir:     in.StateDir,
		LandlockABI:  in.LandlockABI,
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
