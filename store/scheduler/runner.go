package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/decision"
	"github.com/mrsirg97-rgb/rig/v3/models"
	"github.com/mrsirg97-rgb/rig/v3/pathguard"
	"github.com/mrsirg97-rgb/rig/v3/room"
	"github.com/mrsirg97-rgb/rig/v3/store"
	scheddomain "github.com/mrsirg97-rgb/rig/v3/store/scheduler/domain"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Fetch func(url string) (json.RawMessage, error)

type SpawnResult struct {
	Exit     int
	Stdout   string
	Stderr   string
	TimedOut bool
	Signal   syscall.Signal
}

type Spawn func(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (SpawnResult, error)

type RunOpts struct {
	Home         string
	Crontab      Crontab
	Fetch        Fetch
	Spawn        Spawn
	WorkerCmd    []string
	SwapURL      string
	Timeout      time.Duration
	Stall        time.Duration
	Now          func() time.Time
	Sandbox      string
	SandboxBinds []string
	RigHome      string
	StateDir     string
	LandlockABI  func() (int, error)
	Models       func() models.Table
	DefaultModel string
	Decisions    decision.Recorder
	Tell         func(room.JobStatus)
}

const DefaultRunTimeout = 30 * time.Minute

const spawnCaptureCap = 256 * 1024

const defaultSwapURL = "http://127.0.0.1:8090"

var Transport http.RoundTripper

func ReportBack(scope string) string {
	return "\n\nReport back: when you finish, persist durable findings with the rem tool (scope: " + scope + ") and end your reply with a short summary of what you found and did."
}

func runSummary(res SpawnResult, failed bool) string {
	source := res.Stdout
	if failed {
		source = res.Stderr
	}
	return tailLine(source)
}

func tailLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "[spawn:") || strings.HasPrefix(line, "[runner:") {
			continue
		}
		return line
	}
	return ""
}

func (opts RunOpts) modelRow(model string) (models.Model, bool) {
	if opts.Models == nil {
		return models.Model{}, false
	}
	row, ok := opts.Models().Get(model)
	return row, ok
}

func resolveFireModel(fetch Fetch, swapURL string, table models.Table, def string) (string, string, error) {
	row, canonical, err := resolveResidentModel(fetch, swapURL, table)
	if err != nil {
		return "", "", err
	}
	if row != "" {
		return row, canonical, nil
	}
	if def == "" {
		return "", "", fmt.Errorf("no model: the swap has nothing resident and no default is wired (the settings' model is the default)")
	}
	return def, def, nil
}

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

	tell := func(st room.JobStatus) {
		if opts.Tell != nil {
			opts.Tell(st)
		}
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
		if e := recordSkip(db, opts, id, "lock held (previous run still active)", ""); e != nil {
			return e
		}
		return nil
	}

	text, err := opts.Crontab.List()
	if err != nil {
		return err
	}
	if !hasLine(text, key, opts.RigHome) {
		if e := recordSkip(db, opts, id, "no crontab line (drift)", ""); e != nil {
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
		if e := recordSkip(db, opts, id, "no job row (zombie line)", ""); e != nil {
			return e
		}
		return installRemoved(opts.Crontab, text, key, opts.RigHome)
	}
	switch job.State {
	case "done":
		if e := recordSkip(db, opts, id, "job already done (crash between run and line delete)", job.Cwd); e != nil {
			return e
		}
		return installRemoved(opts.Crontab, text, key, opts.RigHome)
	case "removed":
		if e := recordSkip(db, opts, id, "job removed (stale line)", job.Cwd); e != nil {
			return e
		}
		return installRemoved(opts.Crontab, text, key, opts.RigHome)
	case "paused":
		if e := recordSkip(db, opts, id, "store says paused (line drifted active)", job.Cwd); e != nil {
			return e
		}
		return nil
	}
	timeout := opts.Timeout
	if job.Timeout != nil && *job.Timeout > 0 {
		timeout = time.Duration(*job.Timeout) * time.Minute
	}
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}

	canonical, err := pathguard.Canonical(job.Cwd)
	if err != nil || canonical != job.Cwd {
		if e := recordSkip(db, opts, id, "job cwd was replaced or moved (refusing the read-write bind); re-create the job", job.Cwd); e != nil {
			return e
		}
		return nil
	}

	command := ""
	if job.Command != nil {
		command = strings.TrimSpace(*job.Command)
	}
	if command == "" && job.Budget != nil && *job.Budget > 0 {
		spent, err := jobSpent(db, id)
		if err != nil {
			return fmt.Errorf("run-job: budget: %w", err)
		}
		if spent >= *job.Budget {
			if e := recordSkip(db, opts, id, fmt.Sprintf("budget reached (%.2f of %.2f spent)", spent, *job.Budget), job.Cwd); e != nil {
				return e
			}
			return nil
		}
	}
	var (
		argv          []string
		prompt        string
		proxy         *SocketProxy
		spawnEnv      []string
		workerSession string
		workerModel   string
		pipe          *fleetPipe
	)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	startedTime := opts.Now().UTC()
	started := startedTime.Format(time.RFC3339)

	stall := opts.Stall
	if job.Stall != nil && *job.Stall > 0 {
		stall = time.Duration(*job.Stall) * time.Minute
	}
	var watch *stallWatch
	if stall > 0 {
		watch = newStallWatch(stall, cancel)
	}
	workers := 0
	if command == "" {
		workers = 1
	}
	runStatus := func(call string, callAt time.Time) room.JobStatus {
		return room.JobStatus{Job: id, Prompt: firstLine(job.Prompt), State: room.JobRunning,
			Started: startedTime, Workers: workers, Call: call, CallAt: callAt}
	}
	pipe, err = nil, nil
	if command == "" {
		pipe, err = openFleet(ctx, runnerOrigin, func(messages ...broadcast.Message) {
			for _, m := range messages {
				if ts, ok := m.Signal().(core.ToolStart); ok {
					tell(runStatus(ts.BoundedCall(), opts.Now().UTC()))
				}
			}
			if watch != nil {
				watch.touch()
			}
		})
		if err != nil {
			if watch != nil {
				watch.stop()
			}
			return fmt.Errorf("run-job: fleet: %w", err)
		}
	}
	abandon := func() {
		if pipe != nil {
			pipe.close()
		}
		if watch != nil {
			watch.stop()
		}
	}
	if command != "" {
		argv = []string{"sh", "-c", command}
		spawnEnv = append(os.Environ(), fleetEnv(pipe)...)
	} else {
		model := job.Model
		gateModel := model
		if model == "" {
			table := models.Table{}
			if opts.Models != nil {
				table = opts.Models()
			}
			fireModel, canonical, err := resolveFireModel(opts.Fetch, opts.SwapURL, table, opts.DefaultModel)
			if err != nil {
				abandon()
				if e := recordSkip(db, opts, id, err.Error(), job.Cwd); e != nil {
					return e
				}
				return nil
			}
			model = fireModel
			gateModel = canonical
		}
		workerModel = model
		row, rowOK := opts.modelRow(model)
		if !(rowOK && row.Remote) {
			if err := gateOnce(opts.Fetch, opts.SwapURL, gateModel); err != nil {
				abandon()
				if e := recordSkip(db, opts, id, err.Error(), job.Cwd); e != nil {
					return e
				}
				return nil
			}
		}

		workerCmd := opts.WorkerCmd
		if len(workerCmd) == 0 {
			exe, err := os.Executable()
			if err != nil {
				abandon()
				return fmt.Errorf("run-job: worker command: %w", err)
			}
			workerCmd = []string{exe}
		}
		prompt = job.Prompt + ReportBack(job.Cwd)
		workerSession = core.NewSession().ID

		profile, err := SandboxProfile(opts.Sandbox)
		if err != nil {
			abandon()
			return fmt.Errorf("run-job: sandbox: %w", err)
		}
		if profile == "off" {

			fmt.Fprintln(os.Stderr, "run-job: sandbox off: the worker runs unjailed (the operator's choice)")

			argv = append(append([]string{}, workerCmd...),
				"-p", PromptStdin,
				"-session-id", workerSession,
				"-base-url", opts.SwapURL+"/v1",
				"-model", model)
			spawnEnv = append(os.Environ(), fleetEnv(pipe)...)
		} else {
			var refuse string
			argv, proxy, spawnEnv, refuse, err = spawnJailed(opts, profile, job.Cwd, id, workerCmd, model, "", "", workerSession, pipe)
			if err != nil {
				abandon()
				return fmt.Errorf("run-job: jail: %w", err)
			}
			if refuse != "" {
				abandon()
				if e := recordSkip(db, opts, id, refuse, job.Cwd); e != nil {
					return e
				}
				return nil
			}
			defer proxy.Close()
		}
	}

	logName := strings.NewReplacer(":", "-", ".", "-").Replace(startedTime.Format("2006-01-02T15:04:05.000Z")) + ".log"
	logRel := filepath.Join("runs", id, logName)
	dir := filepath.Join(opts.Home, "runs", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		abandon()
		return fmt.Errorf("run-job: log dir: %w", err)
	}
	streamPath := filepath.Join(dir, strings.TrimSuffix(logName, ".log")+".stream")
	stream, err := os.Create(streamPath)
	if err != nil {
		abandon()
		return fmt.Errorf("run-job: stream: %w", err)
	}
	observe := func(p []byte) {
		if watch != nil {
			watch.touch()
		}
		stream.Write(p)
	}
	tell(runStatus("", time.Time{}))
	spawnCtx := ctx
	if pipe != nil {
		spawnCtx = pipe.spawnCtx()
	}
	res, err := opts.Spawn(WithPrompt(spawnCtx, prompt), argv, job.Cwd, spawnEnv, observe)
	if pipe != nil {
		pipe.close()
	}
	stream.Close()
	os.Remove(streamPath)
	if watch != nil {
		watch.stop()
	}
	if err != nil {
		end := room.JobStatus{Job: id, Prompt: firstLine(job.Prompt), State: room.JobFailed,
			Started: startedTime, Ended: opts.Now().UTC(), Reason: err.Error(), Workers: workers}
		tell(end)
		return fmt.Errorf("run-job: spawn: %w", err)
	}
	stalled := false
	if watch != nil && watch.hasFired() && ctx.Err() == context.Canceled {
		stalled = true
		res.Stderr += "\n[runner: killed after stall]\n"
		res.Exit = 1
	}
	endedAt := opts.Now().UTC()
	ended := endedAt.Format(time.RFC3339)
	durationMs := endedAt.Sub(startedTime).Milliseconds()
	modelLine := ""
	if workerModel != "" {
		modelLine = "model=" + workerModel + "\n"
	}
	endState, endReason := room.JobDone, ""
	if res.Exit != 0 {
		endState, endReason = room.JobFailed, fmt.Sprintf("exit %d", res.Exit)
	}
	tell(room.JobStatus{Job: id, Prompt: firstLine(job.Prompt), State: endState,
		Started: startedTime, Ended: endedAt, Reason: endReason, Workers: workers,
		Summary: runSummary(res, endState == room.JobFailed)})
	content := fmt.Sprintf(
		"# rig-scheduler run\nkey=%s\n%sstarted=%s\nexit=%d\nduration_ms=%d\n\n== stdout ==\n%s\n\n== stderr ==\n%s\n",
		key, modelLine, started, res.Exit, durationMs, res.Stdout, res.Stderr)
	if err := os.WriteFile(filepath.Join(dir, logName), []byte(content), 0o644); err != nil {
		return fmt.Errorf("run-job: log write: %w", err)
	}
	if err := pruneLogs(dir, logKeep); err != nil {
		return fmt.Errorf("run-job: log prune: %w", err)
	}

	status := "ok"
	if res.Exit != 0 {
		status = "fail"
	}
	exit := int64(res.Exit)
	duration := durationMs
	var cost *float64
	if workerSession != "" {
		if c := workerSessionCost(opts.RigHome, job.Cwd, workerSession); c > 0 {
			cost = &c
		}
	}
	if _, err := RecordRun(context.Background(), db, RunRecordInput{
		ID: id, Status: status, Exit: &exit, Duration: &duration,
		Log: logRel, Started: started, Ended: ended, Cost: cost,
		Reason: spawnReason(ctx, res, stalled), Model: workerModel,
	}); err != nil {
		return fmt.Errorf("run-job: record: %w", err)
	}

	if job.At != nil {
		fresh, ferr := opts.Crontab.List()
		if ferr != nil {
			return fmt.Errorf("run-job: crontab: %w", ferr)
		}
		if err := installRemoved(opts.Crontab, fresh, key, opts.RigHome); err != nil {
			return err
		}
	}
	if err := Prune(context.Background(), db, opts.Home); err != nil {
		return fmt.Errorf("run-job: prune: %w", err)
	}
	return nil
}
