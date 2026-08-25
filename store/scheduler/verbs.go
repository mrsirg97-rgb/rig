package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	scheddomain "github.com/mrsirg97-rgb/rig/store/scheduler/domain"
)

var jobKeyRe = regexp.MustCompile(`^(j\d+)$`)

func ParseKey(key string) (string, error) {
	if !jobKeyRe.MatchString(key) {
		return "", fmt.Errorf("key: bad key '%s' (want j<n>)", key)
	}
	return key, nil
}

type CreateInput struct {
	Name   string
	Prompt string
	Cron   string
	At     string
	Cwd    string
	Model  string
	Busy   string
}

const defaultModel = "qwen3.8-workers"

func schedErr(format string, a ...any) error {
	return fmt.Errorf("scheduler: "+format, a...)
}

func onceFields(at string) (string, string, error) {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return "", "", schedErr("'at' must be a valid ISO time, got '%s'", at)
	}
	norm := t.UTC().Format(time.RFC3339)
	lt := t.Local()
	return norm, fmt.Sprintf("%d %d %d %d *", lt.Minute(), lt.Hour(), lt.Day(), int(lt.Month())), nil
}

func Create(ctx context.Context, db DB, ct Crontab, in CreateInput, sessionCwd, session, runnerCmd string, now func() time.Time) (string, error) {
	if now == nil {
		now = time.Now
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return "", schedErr("create requires a non-empty name")
	}
	prompt := in.Prompt
	if prompt == "" {
		return "", schedErr("create requires a non-empty prompt")
	}

	jobCwd := in.Cwd
	if jobCwd == "" {
		jobCwd = sessionCwd
	}
	if jobCwd == "" {
		return "", schedErr("create requires a working directory (cwd or a session cwd)")
	}
	model := in.Model
	if model == "" {
		model = defaultModel
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
		norm, five, err := onceFields(in.At)
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
	next, _ := UpsertLine(text, key, cron, runnerCmd)
	if err := ct.Install(next); err != nil {
		return "", err
	}

	if err := maybeCompact(bound, tx, f, session); err != nil {
		return "", err
	}
	argsJSON, _ := json.Marshal(map[string]any{
		"name": name, "prompt": prompt, "cron": cron, "at": at,
		"cwd": jobCwd, "model": model, "busy": busy,
	})
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

func createdRow(f *fold, seq int64) *jobState {
	for _, j := range f.jobs {
		if j.State != "removed" && j.UpdatedSeq == seq {
			return j
		}
	}
	return nil
}

func Pause(ctx context.Context, db DB, ct Crontab, id, sessionCwd, session string) (string, error) {
	return stateAction(ctx, db, ct, id, sessionCwd, session, "pause")
}

func Resume(ctx context.Context, db DB, ct Crontab, id, sessionCwd, session string) (string, error) {
	return stateAction(ctx, db, ct, id, sessionCwd, session, "resume")
}

func Remove(ctx context.Context, db DB, ct Crontab, id, sessionCwd, session string) (string, error) {
	return stateAction(ctx, db, ct, id, sessionCwd, session, "remove")
}

func stateAction(ctx context.Context, db DB, ct Crontab, id, sessionCwd, session, action string) (string, error) {
	_, rtx, err := db.TxReadOnly(ctx)
	if err != nil {
		return "", err
	}
	f, err := eventsOf(rtx)
	if err != nil {
		rtx.Rollback()
		return "", err
	}
	rtx.Rollback()
	job, found := f.jobs[id]
	if !found {
		return "", schedErr("no job '%s'", id)
	}
	switch action {
	case "pause":
		if job.State == "paused" {
			return "", schedErr("'%s' is already paused", id)
		}
		if job.State == "done" {
			return "", schedErr("'%s' is done; nothing to pause", id)
		}
	case "resume":
		if job.State != "paused" {
			return "", schedErr("'%s' is not paused", id)
		}
	case "remove":
		if job.State == "removed" {
			return "", schedErr("'%s' is already removed", id)
		}
	}

	key := id
	text, err := ct.List()
	if err != nil {
		return "", err
	}
	var op func(string) (string, bool)
	switch action {
	case "pause":
		op = func(t string) (string, bool) { return SetPaused(t, key, true) }
	case "resume":
		op = func(t string) (string, bool) { return SetPaused(t, key, false) }
	case "remove":
		op = func(t string) (string, bool) { return RemoveLine(t, key) }
	}
	next, foundLine := op(text)
	if foundLine && next != text {
		if err := ct.Install(next); err != nil {
			return "", err
		}
	}

	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err = eventsOf(tx)
	if err != nil {
		return "", err
	}
	if err := maybeCompact(bound, tx, f, session); err != nil {
		return "", err
	}
	argsJSON, _ := json.Marshal(map[string]any{"id": id})
	seq, err := appendEvent(bound, f.maxSeq+1, action, string(argsJSON), session)
	if err != nil {
		return "", err
	}
	f.apply(eventRow{seq: seq, ts: nowRFC3339(), op: action, args: string(argsJSON)})
	row := f.jobs[id]
	if err := rewrite(tx, f); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	var line *TaggedLine
	if action != "remove" {
		line = &TaggedLine{Key: key, Cron: row.Cron, Paused: action == "pause"}
	}
	lines := jobLines(row, line, false, time.Now)
	return replyText(fmt.Sprintf("%s %s -> %s", row.ID, row.Name, row.State), lines), nil
}

type UpdateInput struct {
	ID     string
	Name   string
	Prompt string
	Cron   string
	At     string
	Cwd    string
	Model  string
	Busy   string
}

func Update(ctx context.Context, db DB, ct Crontab, in UpdateInput, session, runnerCmd string, now func() time.Time) (string, error) {
	if now == nil {
		now = time.Now
	}
	name := strings.TrimSpace(in.Name)
	prompt := in.Prompt
	model := strings.TrimSpace(in.Model)
	cwd := strings.TrimSpace(in.Cwd)
	cron := strings.TrimSpace(in.Cron)
	at := in.At
	busy := in.Busy

	var newCron, newAt string
	switch {
	case cron != "" && at != "" && cron != "once":
		return "", schedErr("update got both a cron and an at (one cadence per update)")
	case cron == "once":
		if at == "" {
			return "", schedErr("cron 'once' requires 'at' (ISO time)")
		}
		norm, five, err := onceFields(at)
		if err != nil {
			return "", err
		}
		newCron, newAt = five, norm
	case cron != "":
		if _, err := ValidateCron(cron); err != nil {
			return "", err
		}
		newCron = cron
	case at != "":
		norm, five, err := onceFields(at)
		if err != nil {
			return "", err
		}
		newCron, newAt = five, norm
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
	job, found := f.jobs[in.ID]
	if !found {
		return "", schedErr("no job '%s'", in.ID)
	}
	if job.State == "removed" {
		return "", schedErr("job '%s' is removed", in.ID)
	}
	if name == "" && prompt == "" && cron == "" && at == "" && model == "" && cwd == "" && busy == "" {
		return "", schedErr("update needs a change")
	}
	if busy != "" && busy != "skip" && busy != "force" {
		return "", schedErr("busy must be 'skip' or 'force', got '%s'", busy)
	}
	if name != "" && name != job.Name {
		for _, j := range f.jobs {
			if j.State != "removed" && j.ID != in.ID && j.Name == name {
				return "", schedErr("a job named '%s' already exists (state: %s); remove it first", name, j.State)
			}
		}
	}
	cadenceChanged := (cron != "" || at != "") && (newCron != job.Cron || newAt != job.At)
	if cadenceChanged {
		text, err := ct.List()
		if err != nil {
			return "", err
		}
		next, _ := UpsertLine(text, in.ID, newCron, runnerCmd)
		if job.State == "paused" {
			next, _ = SetPaused(next, in.ID, true)
		}
		if err := ct.Install(next); err != nil {
			return "", err
		}
	}

	if err := maybeCompact(bound, tx, f, session); err != nil {
		return "", err
	}
	args := map[string]any{"id": in.ID}
	if name != "" {
		args["name"] = name
	}
	if prompt != "" {
		args["prompt"] = prompt
	}
	if model != "" {
		args["model"] = model
	}
	if cwd != "" {
		args["cwd"] = cwd
	}
	if busy != "" {
		args["busy"] = busy
	}
	if cadenceChanged {
		args["cron"] = newCron
		var atPtr *string
		if newAt != "" {
			atPtr = &newAt
		}
		args["at"] = atPtr
	}
	argsJSON, _ := json.Marshal(args)
	seq, err := appendEvent(bound, f.maxSeq+1, "update", string(argsJSON), session)
	if err != nil {
		return "", err
	}
	f.apply(eventRow{seq: seq, ts: nowRFC3339(), op: "update", args: string(argsJSON)})
	row := f.jobs[in.ID]
	if err := rewrite(tx, f); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	line := &TaggedLine{Key: in.ID, Cron: row.Cron, Paused: row.State == "paused"}
	lines := jobLines(row, line, false, now)
	return replyText(fmt.Sprintf("updated %s '%s'", row.ID, row.Name), lines), nil
}

type RunRecordInput struct {
	ID       string
	Status   string
	Exit     *int64
	Duration *int64
	Log      string
	Reason   string
	Started  string
	Ended    string
}

func RecordRun(ctx context.Context, db DB, in RunRecordInput) (int64, error) {
	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx)
	if err != nil {
		return 0, err
	}
	status := statusOf(in.Status)
	argsJSON, _ := json.Marshal(map[string]any{
		"id": in.ID, "status": status, "exit": in.Exit,
		"durationMs": in.Duration, "log": in.Log, "reason": in.Reason,
	})
	seq, err := appendEvent(bound, f.maxSeq+1, "run", string(argsJSON), "")
	if err != nil {
		return 0, err
	}
	job, ok := f.jobs[in.ID]
	if ok {
		job.LastStatus = status
		if in.Exit != nil {
			job.LastExit = *in.Exit
			job.LastExitSet = true
		}
		job.UpdatedSeq = seq
	}
	var reason, log *string
	if in.Reason != "" {
		r := in.Reason
		reason = &r
	}
	if in.Log != "" {
		l := in.Log
		log = &l
	}
	started := in.Started
	if started == "" {
		started = nowRFC3339()
	}
	ended := in.Ended
	if ended == "" {
		ended = nowRFC3339()
	}
	if _, err := scheddomain.NewRunDomain().InsertRun(bound, scheddomain.Run{
		Seq: seq, JobId: in.ID,
		StartedAt: started, EndedAt: ended,
		Status: status, Exit: in.Exit, DurationMs: in.Duration,
		Reason: reason, LogPath: log,
	}); err != nil {
		return 0, fmt.Errorf("scheduler: run record: %w", err)
	}
	if ok {
		if err := rewrite(tx, f); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return seq, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func statusOf(s string) string {
	switch s {
	case "ok", "fail", "skip":
		return s
	}
	return "skip"
}

type runRecord struct {
	Seq        int64
	Started    string
	Status     string
	Exit       *int64
	DurationMs *int64
	Reason     *string
	LogPath    *string
}

func Runs(ctx context.Context, db DB, id string, n int) (string, error) {
	if n <= 0 {
		n = 5
	}
	if n < 1 || n > 100 {
		return "", schedErr("runs count must be an integer 1-100, got %d", n)
	}
	_, tx, err := db.TxReadOnly(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx)
	if err != nil {
		return "", err
	}
	if _, ok := f.jobs[id]; !ok {
		return "", schedErr("no job '%s'", id)
	}

	rows, err := tx.Query(`SELECT seq, started_at, status, exit, duration_ms, reason, log_path FROM runs WHERE job_id = ? ORDER BY seq DESC LIMIT ?`, id, n)
	if err != nil {
		return "", fmt.Errorf("scheduler: runs: %w", err)
	}
	defer rows.Close()
	var out []runRecord
	for rows.Next() {
		var r runRecord
		if err := rows.Scan(&r.Seq, &r.Started, &r.Status, &r.Exit, &r.DurationMs, &r.Reason, &r.LogPath); err != nil {
			return "", fmt.Errorf("scheduler: runs: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("scheduler: runs: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	if len(out) == 0 {
		return fmt.Sprintf("%s · 0 runs (oldest first):", id), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %d run%s (oldest first):\n", id, len(out), plural(len(out)))
	for _, r := range out {
		fmt.Fprintf(&b, "%s  %s  %s\n", r.Started, r.Status, runDetail(r))
	}
	return b.String(), nil
}

func runDetail(r runRecord) string {
	if r.Status == "skip" {
		if r.Reason != nil {
			return *r.Reason
		}
		return ""
	}
	var bits []string
	if r.Exit != nil {
		bits = append(bits, fmt.Sprintf("exit %d", *r.Exit))
	}
	if r.DurationMs != nil {
		bits = append(bits, fmt.Sprintf("%dms", *r.DurationMs))
	}
	if r.LogPath != nil {
		bits = append(bits, *r.LogPath)
	}
	return strings.Join(bits, " ")
}
