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

	"github.com/mrsirg97-rgb/rig/store"
	scheddomain "github.com/mrsirg97-rgb/rig/store/scheduler/domain"
)

type Fetch func(url string) (json.RawMessage, error)

type SpawnResult struct {
	Exit     int
	Stdout   string
	Stderr   string
	TimedOut bool
}

type Spawn func(ctx context.Context, argv []string, cwd string) (SpawnResult, error)

type RunOpts struct {
	Home         string
	Crontab      Crontab
	Fetch        Fetch
	Spawn        Spawn
	WorkerCmd    []string
	SwapURL      string
	Timeout      time.Duration
	Now          func() time.Time
	Sandbox      string
	SandboxBinds []string
	RigHome      string
	StateDir     string
}

const DefaultRunTimeout = 30 * time.Minute

const defaultSwapURL = "http://127.0.0.1:8090"

const ReportBack = "\n\nReport back: when you finish, persist durable findings with the rem tool (project scope: this job's cwd) and end your reply with a short summary of what you found and did."

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

	id, err := ParseKey(key)
	if err != nil {
		if strings.HasPrefix(key, "cwd-") {
			return fmt.Errorf("key: legacy key '%s': the scheduler is one store now; start rig once, in any directory, to fold it and rewrite this line", key)
		}
		return err
	}

	db, quarantined, _, err := store.Open(filepath.Join(opts.Home, "global.sqlite"), Statements(), SchemaVersion)
	if err != nil {
		return fmt.Errorf("run-job: store: %w", err)
	}
	if quarantined != "" {
		fmt.Fprintf(os.Stderr, "run-job: quarantined corrupt store file: %s\n", quarantined)
	}
	defer db.DB.Close()

	lockFD, held, err := acquireLock(opts.Home, key)
	if err != nil {
		return fmt.Errorf("run-job: lock: %w", err)
	}
	if held {
		defer releaseLock(lockFD)
	} else {
		if e := recordSkip(db, id, "lock held (previous run still active)"); e != nil {
			return e
		}
		return nil
	}

	text, err := opts.Crontab.List()
	if err != nil {
		return err
	}
	if !hasLine(text, key) {
		if e := recordSkip(db, id, "no crontab line (drift)"); e != nil {
			return e
		}
		return nil
	}

	bound, tx, err := db.TxReadOnly(context.Background())
	if err != nil {
		return fmt.Errorf("run-job: job row: %w", err)
	}
	job, err := scheddomain.NewJobDomain().GetJob(bound, id).Row()
	tx.Rollback()
	if err != nil {
		return fmt.Errorf("run-job: job row: %w", err)
	}
	if job == nil {
		if e := recordSkip(db, id, "no job row (zombie line)"); e != nil {
			return e
		}
		return installRemoved(opts.Crontab, text, key)
	}
	switch job.State {
	case "done":
		if e := recordSkip(db, id, "job already done (crash between run and line delete)"); e != nil {
			return e
		}
		return installRemoved(opts.Crontab, text, key)
	case "removed":
		if e := recordSkip(db, id, "job removed (stale line)"); e != nil {
			return e
		}
		return installRemoved(opts.Crontab, text, key)
	case "paused":
		if e := recordSkip(db, id, "store says paused (line drifted active)"); e != nil {
			return e
		}
		return nil
	}

	st := busyState(opts.Fetch, opts.SwapURL, job.Model)
	switch st.kind {
	case "error":
		if e := recordSkip(db, id, st.reason); e != nil {
			return e
		}
		return nil
	case "busy":
		if job.Busy != "force" {
			if e := recordSkip(db, id, "busy: "+st.names+" resident (policy skip)"); e != nil {
				return e
			}
			return nil
		}
	}

	workerCmd := opts.WorkerCmd
	if len(workerCmd) == 0 {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("run-job: worker command: %w", err)
		}
		workerCmd = []string{exe}
	}
	prompt := job.Prompt + ReportBack

	profile, err := SandboxProfile(opts.Sandbox)
	if err != nil {
		return fmt.Errorf("run-job: sandbox: %w", err)
	}
	var (
		argv    []string
		proxy   *SocketProxy
		homeEnv string
		refuse  string
	)
	if profile == "off" {

		fmt.Fprintln(os.Stderr, "run-job: sandbox off: the worker runs unjailed (the operator's choice)")

		argv = append(append([]string{}, workerCmd...),
			"-p", prompt,
			"-base-url", opts.SwapURL+"/v1",
			"-model", job.Model)
	} else {
		argv, proxy, homeEnv, refuse, err = jailSpawn(opts, job.Cwd, workerCmd, job.Model, prompt, "")
		if err != nil {
			return fmt.Errorf("run-job: jail: %w", err)
		}
		if refuse != "" {
			if e := recordSkip(db, id, refuse); e != nil {
				return e
			}
			return nil
		}
		defer proxy.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	startedTime := opts.Now().UTC()
	started := startedTime.Format(time.RFC3339)
	if homeEnv != "" {

		prev, had := os.LookupEnv("RIG_HOME")
		os.Setenv("RIG_HOME", homeEnv)
		defer func() {
			if had {
				os.Setenv("RIG_HOME", prev)
			} else {
				os.Unsetenv("RIG_HOME")
			}
		}()
	}
	res, err := opts.Spawn(ctx, argv, job.Cwd)
	if err != nil {
		return fmt.Errorf("run-job: spawn: %w", err)
	}
	ended := opts.Now().UTC().Format(time.RFC3339)
	durationMs := opts.Now().UTC().Sub(startedTime).Milliseconds()

	logName := strings.NewReplacer(":", "-", ".", "-").Replace(opts.Now().UTC().Format("2006-01-02T15:04:05.000Z")) + ".log"
	logRel := filepath.Join("runs", id, logName)
	dir := filepath.Join(opts.Home, "runs", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("run-job: log dir: %w", err)
	}
	content := fmt.Sprintf(
		"# rig-scheduler run\nkey=%s\nstarted=%s\nexit=%d\nduration_ms=%d\n\n== stdout ==\n%s\n\n== stderr ==\n%s\n",
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
	if _, err := RecordRun(context.Background(), db, RunRecordInput{
		ID: id, Status: status, Exit: &exit, Duration: &duration,
		Log: logRel, Started: started, Ended: ended, Done: job.At != nil,
	}); err != nil {
		return fmt.Errorf("run-job: record: %w", err)
	}

	if job.At != nil {
		if err := installRemoved(opts.Crontab, text, key); err != nil {
			return err
		}
	}
	return nil
}

func recordSkip(db DB, id, reason string) error {

	if _, err := RecordRun(context.Background(), db, RunRecordInput{
		ID: id, Status: "skip", Reason: reason,
	}); err != nil {
		return fmt.Errorf("run-job: skip record: %w", err)
	}
	return nil
}

func installRemoved(ct Crontab, text, key string) error {
	next, found := RemoveLine(text, key)
	if !found || next == text {
		return nil
	}
	return ct.Install(next)
}

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
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock: %w", err)
	}
	return fd, true, nil
}

func releaseLock(fd *os.File) {
	syscall.Flock(int(fd.Fd()), syscall.LOCK_UN)
	fd.Close()
}

func hasLine(text, key string) bool {
	for _, l := range Scan(text) {
		if l.Key == key {
			return true
		}
	}
	return false
}

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

type busyResult struct {
	kind   string
	names  string
	reason string
}

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
			return busyResult{kind: "run"}
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
		return busyResult{kind: "run"}
	}
	var names []string
	for n := range resident {
		names = append(names, n)
	}
	sort.Strings(names)
	return busyResult{kind: "busy", names: strings.Join(names, ", ")}
}

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

		return SpawnResult{}, fmt.Errorf("spawn: %w", runErr)
	}
	return res, nil
}
