package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mrsirg97-rgb/looper/store"
	scheddomain "github.com/mrsirg97-rgb/looper/store/scheduler/domain"
)

// --- run-job: the fire engine cron invokes ---
//
// Pane's runner.mjs, ported. Every outcome is recorded; only unexpected
// errors come back loud (the verb maps that to exit non-zero). The job
// runtime is looper itself in -p mode (roadmap deliverable 2, borrowed
// minimally by this PR); the report-back instruction is pane's standing
// directive, verbatim.

// Fetch is the busy-check transport seam.
type Fetch func(url string) (json.RawMessage, error)

// SpawnResult is one worker run's outcome.
type SpawnResult struct {
	Exit     int
	Stdout   string
	Stderr   string
	TimedOut bool
}

// Spawn is the worker-spawn seam.
type Spawn func(ctx context.Context, argv []string, cwd string) (SpawnResult, error)

// RunOpts is RunJob's seam bundle: production wires the real crontab,
// fetch, and spawn once at the root; tests inject fakes.
type RunOpts struct {
	Home      string
	Crontab   Crontab
	Fetch     Fetch
	Spawn     Spawn
	WorkerCmd []string // nil: the process's own executable
	SwapURL   string   // empty: pane's default swap endpoint
	Timeout   time.Duration
	Now       func() time.Time
}

// DefaultRunTimeout bounds one worker run (pane's default).
const DefaultRunTimeout = 30 * time.Minute

// defaultSwapURL is pane's llama-swap default endpoint.
const defaultSwapURL = "http://127.0.0.1:8090"

// ReportBack is pane's standing report-back directive, appended to every
// job prompt: findings persist through rem, scoped to the job's cwd.
const ReportBack = "\n\nReport back: when you finish, persist durable findings with the rem tool (project scope: this job's cwd) and end your reply with a short summary of what you found and did."

// RunJob fires one job from its key: lock, drift/zombie self-heal, busy
// policy, worker spawn under a bounded context, record, log, once-
// consumption. Nil error means a recorded outcome (the verb exits 0); a
// loud error means nothing ran (the verb exits non-zero).
func RunJob(key string, opts RunOpts) error {
	if opts.Crontab == nil || opts.Fetch == nil || opts.Spawn == nil {
		return fmt.Errorf("run-job: crontab, fetch, and spawn seams are required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.SwapURL == "" {
		opts.SwapURL = defaultSwapURL
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}

	parsed, err := ParseKey(key)
	if err != nil {
		return err
	}

	// the store for this key, opened cold: run-job is its own process
	db, quarantined, err := store.Open(StorePathFor(opts.Home, parsed), Statements(), SchemaVersion)
	if err != nil {
		return fmt.Errorf("run-job: store: %w", err)
	}
	if quarantined != "" {
		fmt.Fprintf(os.Stderr, "run-job: quarantined corrupt store file: %s\n", quarantined)
	}
	defer db.DB.Close()

	// one fire per key at a time: the flock mechanism, attempted non-
	// blocking. Held by a previous fire -> recorded skip, benign exit.
	lockFD, held, err := acquireLock(opts.Home, key)
	if err != nil {
		return fmt.Errorf("run-job: lock: %w", err)
	}
	if held {
		defer releaseLock(lockFD)
	} else {
		if e := recordSkip(db, parsed.ID, "lock held (previous run still active)"); e != nil {
			return e
		}
		return nil
	}

	// drift first: cron fires from the line, so an absent line is drift —
	// record loudly, touch nothing
	text, err := opts.Crontab.List()
	if err != nil {
		return err
	}
	if !hasLine(text, key) {
		if e := recordSkip(db, parsed.ID, "no crontab line (drift)"); e != nil {
			return e
		}
		return nil
	}

	// the job row (the projection): zombie, done, and paused gates
	bound, tx, err := db.TxReadOnly(context.Background())
	if err != nil {
		return fmt.Errorf("run-job: job row: %w", err)
	}
	job, err := scheddomain.NewJobDomain().GetJob(bound, parsed.ID).Row()
	tx.Rollback()
	if err != nil {
		return fmt.Errorf("run-job: job row: %w", err)
	}
	if job == nil {
		if e := recordSkip(db, parsed.ID, "no job row (zombie line)"); e != nil {
			return e
		}
		return installRemoved(opts.Crontab, text, key)
	}
	switch job.State {
	case "done":
		if e := recordSkip(db, parsed.ID, "job already done (crash between run and line delete)"); e != nil {
			return e
		}
		return installRemoved(opts.Crontab, text, key)
	case "removed":
		if e := recordSkip(db, parsed.ID, "job removed (stale line)"); e != nil {
			return e
		}
		return installRemoved(opts.Crontab, text, key)
	case "paused":
		if e := recordSkip(db, parsed.ID, "store says paused (line drifted active)"); e != nil {
			return e
		}
		return nil
	}

	// busy policy: llama-swap is the GPU truth; own-slot-loaded short-
	// circuits (concurrent slots are safe, no eviction); unreachable
	// fails closed
	st := busyState(opts.Fetch, opts.SwapURL, job.Model)
	switch st.kind {
	case "error":
		if e := recordSkip(db, parsed.ID, st.reason); e != nil {
			return e
		}
		return nil
	case "busy":
		if job.Busy != "force" {
			if e := recordSkip(db, parsed.ID, "busy: "+st.names+" resident (policy skip)"); e != nil {
				return e
			}
			return nil
		}
	}

	// the worker run, bounded: context timeout with process-group kill
	workerCmd := opts.WorkerCmd
	if len(workerCmd) == 0 {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("run-job: worker command: %w", err)
		}
		workerCmd = []string{exe}
	}
	argv := append(append([]string{}, workerCmd...), "-p", job.Prompt+ReportBack, "-model", job.Model)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	startedTime := opts.Now().UTC()
	started := startedTime.Format(time.RFC3339)
	res, err := opts.Spawn(ctx, argv, job.Cwd)
	if err != nil {
		return fmt.Errorf("run-job: spawn: %w", err)
	}
	ended := opts.Now().UTC().Format(time.RFC3339)
	durationMs := opts.Now().UTC().Sub(startedTime).Milliseconds()

	// the log: runs/<scope>/<id>/<ts>.log, newest twenty kept
	logName := strings.NewReplacer(":", "-", ".", "-").Replace(opts.Now().UTC().Format("2006-01-02T15:04:05.000Z")) + ".log"
	logRel := filepath.Join("runs", ScopeDir(parsed), parsed.ID, logName)
	dir := filepath.Join(opts.Home, "runs", ScopeDir(parsed), parsed.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("run-job: log dir: %w", err)
	}
	content := fmt.Sprintf(
		"# looper-scheduler run\nkey=%s\nstarted=%s\nexit=%d\nduration_ms=%d\n\n== stdout ==\n%s\n\n== stderr ==\n%s\n",
		key, started, res.Exit, durationMs, res.Stdout, res.Stderr)
	if err := os.WriteFile(filepath.Join(dir, logName), []byte(content), 0o644); err != nil {
		return fmt.Errorf("run-job: log write: %w", err)
	}
	if err := pruneLogs(dir, 20); err != nil {
		return fmt.Errorf("run-job: log prune: %w", err)
	}

	status := "ok"
	if res.Exit != 0 {
		status = "fail"
	}
	exit := int64(res.Exit)
	duration := durationMs
	seq, err := RecordRun(context.Background(), db, RunRecordInput{
		ID: parsed.ID, Status: status, Exit: &exit, Duration: &duration,
		Log: logRel, Started: started, Ended: ended,
	})
	if err != nil {
		return fmt.Errorf("run-job: record: %w", err)
	}

	// once job: mark done (store first), then consume the line. A crash in
	// between leaves a live line on a done row; the next fire heals it.
	if job.At != nil {
		job.State = "done"
		job.UpdatedSeq = seq
		ob, otx, err := db.Tx(context.Background())
		if err != nil {
			return fmt.Errorf("run-job: once-state: %w", err)
		}
		if _, err := scheddomain.NewJobDomain().UpdateJob(ob, *job); err != nil {
			otx.Rollback()
			return fmt.Errorf("run-job: once-state: %w", err)
		}
		if err := otx.Commit(); err != nil {
			return fmt.Errorf("run-job: once-state: %w", err)
		}
		if err := installRemoved(opts.Crontab, text, key); err != nil {
			return err
		}
	}
	return nil
}

// recordSkip lands one skip record without touching the crontab.
func recordSkip(db DB, id, reason string) error {

	if _, err := RecordRun(context.Background(), db, RunRecordInput{
		ID: id, Status: "skip", Reason: reason,
	}); err != nil {
		return fmt.Errorf("run-job: skip record: %w", err)
	}
	return nil
}

// installRemoved consumes a healed line: surgery plus install, skipped
// when the line is absent or already clean.
func installRemoved(ct Crontab, text, key string) error {
	next, found := RemoveLine(text, key)
	if !found || next == text {
		return nil
	}
	return ct.Install(next)
}

// acquireLock attempts the key's non-blocking flock. Held true means the
// lock is acquired by this process (release later); held false with a nil
// error means contention (the caller records the skip). Other errors
// surface (loud).
func acquireLock(home, key string) (*os.File, bool, error) {
	lockDir := filepath.Join(home, "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, false, err
	}
	lockPath := filepath.Join(lockDir, strings.ReplaceAll(key, ":", "_")+".lock")
	fd, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fd.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil // held by another fire
		}
		return nil, false, fmt.Errorf("flock: %w", err)
	}
	return fd, true, nil
}

func releaseLock(fd *os.File) {
	syscall.Flock(int(fd.Fd()), syscall.LOCK_UN)
	fd.Close()
}

// hasLine: is this key's tagged line present (active or paused)?
func hasLine(text, key string) bool {
	for _, l := range Scan(text) {
		if l.Key == key {
			return true
		}
	}
	return false
}

// pruneLogs keeps the newest keep names in dir (pane's prune).
func pruneLogs(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) <= keep {
		return nil
	}
	for _, name := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// --- busy policy ---

// busyResult is one busy-state verdict (pane's busyState shape).
type busyResult struct {
	kind   string // run | busy | error
	names  string
	reason string
}

// busyState is pane's busy check, verbatim in semantics: normalize names
// through the swap's alias map, short-circuit on own-slot loaded (concurrent
// slots are safe, no eviction), then resident-size decides. Fetch failure
// fails closed.
func busyState(fetch Fetch, swapURL, jobModel string) busyResult {
	modelsRaw, err := fetch(swapURL + "/v1/models")
	if err != nil {
		return busyResult{kind: "error", reason: "busy check failed: " + err.Error()}
	}
	runningRaw, err := fetch(swapURL + "/running")
	if err != nil {
		return busyResult{kind: "error", reason: "busy check failed: " + err.Error()}
	}

	var models struct {
		Data []struct {
			ID   string `json:"id"`
			Meta struct {
				LLamaSwap struct {
					Aliases []string `json:"aliases"`
				} `json:"llamaswap"`
			} `json:"meta"`
			Status struct {
				Value string `json:"value"`
			} `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(modelsRaw, &models); err != nil {
		return busyResult{kind: "error", reason: "busy check failed: models: " + err.Error()}
	}
	var running struct {
		Running []struct {
			Model string `json:"model"`
		} `json:"running"`
	}
	if err := json.Unmarshal(runningRaw, &running); err != nil {
		return busyResult{kind: "error", reason: "busy check failed: running: " + err.Error()}
	}

	// normalize names: the store keeps pi catalog ids while /running
	// reports canonical llama-swap names
	canon := map[string]string{}
	for _, m := range models.Data {
		for _, n := range append([]string{m.ID}, m.Meta.LLamaSwap.Aliases...) {
			canon[n] = m.ID
		}
	}
	norm := func(n string) string {
		if c, ok := canon[n]; ok {
			return c
		}
		return n
	}
	own := norm(jobModel)
	for _, m := range models.Data {
		if norm(m.ID) == own && m.Status.Value == "loaded" {
			return busyResult{kind: "run"} // own slot already allocated
		}
	}
	resident := map[string]bool{}
	for _, r := range running.Running {
		resident[norm(r.Model)] = true
	}
	if resident[own] {
		return busyResult{kind: "run"}
	}
	if len(resident) == 0 {
		return busyResult{kind: "run"} // nothing to evict: a plain load
	}
	var names []string
	for n := range resident {
		names = append(names, n)
	}
	sort.Strings(names)
	return busyResult{kind: "busy", names: strings.Join(names, ", ")}
}

// --- production seams ---

// RealFetch is the production busy-check transport.
func RealFetch(timeout time.Duration) Fetch {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	return func(url string) (json.RawMessage, error) {
		resp, err := client.Get(url)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var raw json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
}

// RealSpawn is the production worker spawn: context-bounded, process-
// group kill on cancellation (tool/bash's discipline), captured output.
func RealSpawn(ctx context.Context, argv []string, cwd string) (SpawnResult, error) {
	if len(argv) == 0 {
		return SpawnResult{}, errors.New("spawn: empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	res := SpawnResult{Stdout: out.String(), Stderr: errBuf.String()}
	switch {
	case runErr == nil:
		res.Exit = 0
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.TimedOut = true
		res.Exit = 1
		res.Stderr += "\n[runner: killed after timeout]\n"
	default:
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			res.Exit = exit.ExitCode()
			return res, nil
		}
		// the worker never resolved: loud, the verb's exit-1 path
		return SpawnResult{}, fmt.Errorf("spawn: %w", runErr)
	}
	return res, nil
}
