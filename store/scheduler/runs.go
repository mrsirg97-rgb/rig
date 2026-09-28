package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	scheddomain "github.com/mrsirg97-rgb/rig/v2/store/scheduler/domain"
	"sort"
	"strings"
)

type RunRecordInput struct {
	ID       string
	Status   string
	Exit     *int64
	Duration *int64
	Log      string
	Reason   string
	Started  string
	Ended    string
	Cost     *float64
	Done     bool
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
	if ok && in.Done {
		doneJSON, _ := json.Marshal(map[string]any{"id": in.ID})
		doneSeq, err := appendEvent(bound, seq+1, "done", string(doneJSON), "")
		if err != nil {
			return 0, err
		}
		f.apply(eventRow{seq: doneSeq, ts: nowRFC3339(), op: "done", args: string(doneJSON)})
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
		Reason: reason, LogPath: log, Cost: in.Cost,
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
