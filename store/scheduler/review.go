package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/models"
	scheddomain "github.com/mrsirg97-rgb/rig/v2/store/scheduler/domain"
)

const ReviewPrompt = "review"

func runReviewJob(db DB, opts RunOpts, job *scheddomain.Job, text string, timeout time.Duration) error {
	if opts.Reviews == nil {
		return recordSkip(db, opts, job.Id, "no reviews seam (the runner was wired without the decision store)", job.Cwd)
	}
	table := models.Table{}
	if opts.Models != nil {
		table = opts.Models()
	}
	model, gateModel := job.Model, job.Model
	if model == "" {
		fireModel, canonical, err := resolveFireModel(opts.Fetch, opts.SwapURL, table, opts.DefaultModel)
		if err != nil {
			return recordSkip(db, opts, job.Id, err.Error(), job.Cwd)
		}
		model, gateModel = fireModel, canonical
	}
	row, err := models.Resolve(table, model, os.LookupEnv)
	if err != nil {
		return recordSkip(db, opts, job.Id, fmt.Sprintf("no model row for %q: %v", model, err), job.Cwd)
	}
	if !row.Remote {
		if err := gateOnce(opts.Fetch, opts.SwapURL, gateModel); err != nil {
			return recordSkip(db, opts, job.Id, err.Error(), job.Cwd)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	startedTime := opts.Now().UTC()
	started := startedTime.Format(time.RFC3339)

	var fired DelegateResult
	fire := func(ctx context.Context, prompt string) (string, string, error) {
		res, err := Delegate(DelegateInput{
			DB: db, Home: opts.Home, Session: core.NewSession().ID,
			Cwd: job.Cwd, Task: prompt, Model: model,
			WorkerSession: core.NewSession().ID,
			DefaultModel:  opts.DefaultModel, Models: opts.Models,
			Fetch: opts.Fetch, Spawn: opts.Spawn, WorkerCmd: opts.WorkerCmd,
			SwapURL: opts.SwapURL, Timeout: -1,
			RigHome: opts.RigHome, StateDir: opts.StateDir,
			Sandbox: opts.Sandbox, SandboxBinds: opts.SandboxBinds,
			NoTools: true, SpawnCtx: ctx,
		})
		if err == nil {
			fired = res
		}
		if err != nil {
			return "", "", err
		}
		if ferr := res.FireError(opts.Home); ferr != nil {
			return "", res.Model, ferr
		}
		return res.Stdout, res.Model, nil
	}
	loud := func(m string) { fmt.Fprintln(os.Stderr, "run-job: decision:", m) }
	rev := decision.NewReviewer(opts.Reviews, fire, row.Window-row.Reserve, row.MaxTokens, loud)
	summary, drainErr := rev.Drain(ctx)
	if drainErr == nil {
		loud(summary)
	}

	endedTime := opts.Now().UTC()
	ended := endedTime.Format(time.RFC3339)
	duration := endedTime.Sub(startedTime).Milliseconds()
	status, exit := "ok", int64(0)
	reason := ""
	if drainErr != nil {
		status, exit, reason = "fail", 1, drainErr.Error()
	}
	modelLine := ""
	if model != "" {
		modelLine = "model=" + model + "\n"
	}
	logName := strings.NewReplacer(":", "-", ".", "-").Replace(startedTime.Format("2006-01-02T15:04:05.000Z")) + ".log"
	logRel := filepath.Join("runs", job.Id, logName)
	dir := filepath.Join(opts.Home, "runs", job.Id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("run-job: log dir: %w", err)
	}
	outcome := summary
	if drainErr != nil {
		outcome = drainErr.Error()
	}
	content := fmt.Sprintf(
		"# rig-scheduler run\nkey=%s\n%sstarted=%s\nexit=%d\nduration_ms=%d\n\n== summary ==\n%s\n\n== stdout ==\n%s\n\n== stderr ==\n%s\n",
		job.Id, modelLine, started, exit, duration, outcome, fired.Stdout, fired.Stderr)
	if err := os.WriteFile(filepath.Join(dir, logName), []byte(content), 0o644); err != nil {
		return fmt.Errorf("run-job: log write: %w", err)
	}
	if err := pruneLogs(dir, 20); err != nil {
		return fmt.Errorf("run-job: log prune: %w", err)
	}
	if _, err := RecordRun(context.Background(), db, RunRecordInput{
		ID: job.Id, Status: status, Exit: &exit, Duration: &duration,
		Log: logRel, Started: started, Ended: ended, Done: job.At != nil,
		Reason: reason, Model: model,
	}); err != nil {
		return fmt.Errorf("run-job: record: %w", err)
	}
	if job.At != nil {
		if err := installRemoved(opts.Crontab, text, job.Id, opts.RigHome); err != nil {
			return err
		}
	}
	return nil
}
