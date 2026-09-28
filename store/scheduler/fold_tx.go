package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	scheddomain "github.com/mrsirg97-rgb/rig/v2/store/scheduler/domain"
	"sort"
	"strings"
	"time"
)

const COMPACT_THRESHOLD_EVENTS = 1000

func nowRFC3339() string {

	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func eventsOf(tx *sql.Tx) (*fold, error) {
	rows, err := tx.Query(`SELECT seq, op, args, session, ts FROM events ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("scheduler: event log: %w", err)
	}
	defer rows.Close()
	f := newFold()
	for rows.Next() {
		var e eventRow
		var session sql.NullString
		if err := rows.Scan(&e.seq, &e.op, &e.args, &session, &e.ts); err != nil {
			return nil, fmt.Errorf("scheduler: event log: %w", err)
		}
		e.session = session.String
		f.apply(e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: event log: %w", err)
	}
	return f, nil
}

func appendEvent(bound context.Context, seq int64, op, args, session string) (int64, error) {
	var sess *string
	if session != "" {
		s := session
		sess = &s
	}
	ev, err := scheddomain.NewEventDomain().InsertEvent(bound, scheddomain.Event{
		Seq: seq, Ts: nowRFC3339(), Op: op, Args: args, Session: sess,
	})
	if err != nil {
		return 0, fmt.Errorf("scheduler: event append: %w", err)
	}
	return ev.Seq, nil
}

func maybeCompact(bound context.Context, tx *sql.Tx, f *fold, session string) error {
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM events`).Scan(&count); err != nil {
		return fmt.Errorf("scheduler: compact count: %w", err)
	}
	if count < COMPACT_THRESHOLD_EVENTS {
		return nil
	}
	var snapshot []any
	for _, j := range f.jobs {
		at := j.atPtr()
		ls := j.lastStatusPtr()
		lt := j.lastTsPtr()
		le := j.lastExitPtr()
		snapshot = append(snapshot, compactJob{
			ID: j.ID, Name: j.Name, Prompt: j.Prompt, Command: j.commandPtr(),
			Cron: j.Cron, At: at, Cwd: j.Cwd, Model: j.Model, Busy: j.Busy,
			Timeout:    j.timeoutPtr(),
			Stall:      j.stallPtr(),
			Budget:     j.budgetPtr(),
			State:      j.State,
			LastStatus: ls, LastTs: lt, LastExit: le,
		})
	}
	if snapshot == nil {
		snapshot = []any{}
	}
	args, _ := json.Marshal(map[string]any{"jobs": snapshot})
	seq, e := appendEvent(bound, f.maxSeq+1, "compact", string(args), session)
	if e != nil {
		return e
	}
	f.compactSeq = seq
	f.maxSeq = seq
	for _, j := range f.jobs {
		j.CreatedSeq = seq
		j.UpdatedSeq = seq
	}
	if e := rewrite(tx, f); e != nil {
		return e
	}
	if _, e := tx.Exec(`DELETE FROM events WHERE seq < ?`, seq); e != nil {
		return fmt.Errorf("scheduler: compact: %w", e)
	}
	return nil
}

func rewrite(tx *sql.Tx, f *fold) error {
	if _, err := tx.Exec(`DELETE FROM jobs`); err != nil {
		return fmt.Errorf("scheduler: rewrite: %w", err)
	}
	var order []*jobState
	for _, j := range f.jobs {
		order = append(order, j)
	}
	sort.Slice(order, func(i, j int) bool {
		return order[i].ID < order[j].ID
	})
	for _, j := range order {
		_, err := tx.Exec(
			`INSERT INTO jobs (id, name, prompt, command, cron, at, cwd, model, busy, timeout, stall, budget, state,
			    last_status, last_ts, last_exit, created_seq, updated_seq)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			j.ID, j.Name, j.Prompt, nullStr(j.Command), j.Cron, nullStr(j.At), j.Cwd, j.Model, j.Busy,
			nullInt64(j.TimeoutSet, j.Timeout), nullInt64(j.StallSet, j.Stall), nullFloat64(j.BudgetSet, j.Budget), j.State,
			nullStr(j.LastStatus), nullStr(j.LastTs), nullInt64(j.LastExitSet, j.LastExit),
			j.CreatedSeq, j.UpdatedSeq,
		)
		if err != nil {
			return fmt.Errorf("scheduler: rewrite: %w", err)
		}
	}
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt64(set bool, v int64) any {
	if !set {
		return nil
	}
	return v
}

func nullFloat64(set bool, v float64) any {
	if !set {
		return nil
	}
	return v
}

func replyText(note string, lines []string) string {
	var b strings.Builder
	if note != "" {
		fmt.Fprintf(&b, "%s\n", note)
	}
	b.WriteString(strings.Join(lines, "\n"))
	return b.String()
}
