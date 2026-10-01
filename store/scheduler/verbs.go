package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var jobKeyRe = regexp.MustCompile(`^(j\d+)$`)

func ParseKey(key string) (string, error) {
	if !jobKeyRe.MatchString(key) {
		return "", fmt.Errorf("key: bad key '%s' (want j<n>)", key)
	}
	return key, nil
}

type CreateInput struct {
	Name    string
	Prompt  string
	Command string
	Cron    string
	At      string
	Cwd     string
	Model   string
	Busy    string
	Timeout int
	Stall   int
	Budget  float64
}

const MaxTimeoutMinutes = 1440
const MaxStallMinutes = 1440

func timeoutErr(v int) error {
	return schedErr("timeout must be 1..%d minutes (0 takes the runner default), got %d", MaxTimeoutMinutes, v)
}

func stallErr(v int) error {
	return schedErr("stall must be 1..%d minutes (0 disables the stall kill), got %d", MaxStallMinutes, v)
}

func schedErr(format string, a ...any) error {
	return fmt.Errorf("scheduler: "+format, a...)
}

func onceFields(at string, now time.Time) (string, string, error) {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return "", "", schedErr("'at' must be a valid ISO time, got '%s'", at)
	}
	if !t.After(now) {
		return "", "", schedErr("'at' must be in the future, got '%s'", at)
	}
	norm := t.UTC().Format(time.RFC3339)
	lt := t.Local()
	return norm, fmt.Sprintf("%d %d %d %d *", lt.Minute(), lt.Hour(), lt.Day(), int(lt.Month())), nil
}

func Create(ctx context.Context, db DB, ct Crontab, in CreateInput, sessionCwd, session, runnerCmd, home string, now func() time.Time) (string, error) {
	if now == nil {
		now = time.Now
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return "", schedErr("create requires a non-empty name")
	}
	prompt := in.Prompt
	command := strings.TrimSpace(in.Command)
	if prompt == "" && command == "" {
		return "", schedErr("create requires a prompt or a command")
	}
	if prompt != "" && command != "" {
		return "", schedErr("create got both a prompt and a command (one payload per job)")
	}
	if command != "" && in.Model != "" {
		return "", schedErr("command jobs need no model")
	}
	if command != "" && in.Busy != "" {
		return "", schedErr("command jobs need no busy policy")
	}
	if in.Busy != "" && in.Busy != "skip" {
		return "", schedErr("busy must be 'skip' (force is retired: eviction is the operator's act), got '%s'", in.Busy)
	}
	if command != "" && in.Budget != 0 {
		return "", schedErr("command jobs need no budget")
	}
	if in.Budget < 0 {
		return "", schedErr("budget must be >= 0 dollars, got %v", in.Budget)
	}
	if in.Timeout != 0 && (in.Timeout < 1 || in.Timeout > MaxTimeoutMinutes) {
		return "", timeoutErr(in.Timeout)
	}
	if in.Stall != 0 && (in.Stall < 1 || in.Stall > MaxStallMinutes) {
		return "", stallErr(in.Stall)
	}

	jobCwd := in.Cwd
	if jobCwd == "" {
		jobCwd = sessionCwd
	}
	if jobCwd == "" {
		return "", schedErr("create requires a workspace (workspace or a session workspace)")
	}
	model := in.Model
	if command == "" && model == "" {
		return "", schedErr("create requires a non-empty model (the fleet's model, or the job's own)")
	}
	busy := busyOf(in.Busy)

	cron := strings.TrimSpace(in.Cron)
	if cron == "" {
		return "", schedErr("create requires 'cron' (5-field or 'once' + 'at')")
	}
	var at *string
	if cron == "once" {
		if in.At == "" {
			return "", schedErr("cron 'once' requires 'at' (ISO time)")
		}
		norm, five, err := onceFields(in.At, now())
		if err != nil {
			return "", err
		}
		at = &norm
		cron = five
	} else {
		if _, err := ValidateCron(cron); err != nil {
			return "", err
		}
	}

	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx)
	if err != nil {
		return "", err
	}
	for _, j := range f.jobs {
		if j.State != "removed" && j.Name == name {
			return "", schedErr("a job named '%s' already exists (state: %s); remove it first", name, j.State)
		}
	}
	candidateID := f.mintID()
	key := candidateID

	text, err := ct.List()
	if err != nil {
		return "", err
	}
	next, _ := UpsertLine(text, key, cron, runnerCmd, home)
	if err := ct.Install(next); err != nil {
		return "", err
	}

	if err := maybeCompact(bound, tx, f, session); err != nil {
		return "", err
	}
	args := map[string]any{
		"name": name, "prompt": prompt, "cron": cron, "at": at,
		"cwd": jobCwd, "model": model, "busy": busy,
	}
	if command != "" {
		args["command"] = command
	}
	if in.Timeout > 0 {
		args["timeout"] = in.Timeout
	}
	if in.Stall > 0 {
		args["stall"] = in.Stall
	}
	if in.Budget > 0 {
		args["budget"] = in.Budget
	}
	argsJSON, _ := json.Marshal(args)
	seq, err := appendEvent(bound, f.maxSeq+1, "create", string(argsJSON), session)
	if err != nil {
		return "", err
	}
	f.apply(eventRow{seq: seq, ts: nowRFC3339(), op: "create", args: string(argsJSON)})
	created := createdRow(f, seq)
	if created == nil {
		return "", schedErr("concurrent create raced on name '%s'; the crontab line is orphaned, remove it", name)
	}
	if err := rewrite(tx, f); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	line := TaggedLine{Key: key, Cron: created.Cron, Paused: false}
	lines := jobLines(created, &line, false, now)
	return replyText(fmt.Sprintf("created %s '%s'", created.ID, created.Name), lines), nil
}
