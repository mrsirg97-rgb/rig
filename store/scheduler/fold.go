package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	scheddomain "github.com/mrsirg97-rgb/rig/store/scheduler/domain"
)

type jobState struct {
	ID          string
	Name        string
	Prompt      string
	Cron        string
	At          string
	Cwd         string
	Model       string
	Busy        string
	State       string
	LastStatus  string
	LastTs      string
	LastExit    int64
	LastExitSet bool
	CreatedSeq  int64
	UpdatedSeq  int64
}

type fold struct {
	jobs       map[string]*jobState
	maxSeq     int64
	compactSeq int64
}

func newFold() *fold {
	return &fold{jobs: map[string]*jobState{}}
}

func (j *jobState) atPtr() *string {
	if j.At != "" {
		return &j.At
	}
	return nil
}
func (j *jobState) lastStatusPtr() *string {
	if j.LastStatus == "" {
		return nil
	}
	return &j.LastStatus
}
func (j *jobState) lastTsPtr() *string {
	if j.LastTs == "" {
		return nil
	}
	return &j.LastTs
}
func (j *jobState) lastExitPtr() *int64 {
	if !j.LastExitSet {
		return nil
	}
	return &j.LastExit
}

type eventRow struct {
	seq     int64
	op      string
	args    string
	session string
	ts      string
}

func (f *fold) mintID() string {
	max := 0
	for id := range f.jobs {
		if len(id) > 1 && id[0] == 'j' {
			n := 0
			for _, c := range id[1:] {
				if c < '0' || c > '9' {
					n = -1
					break
				}
				n = n*10 + int(c-'0')
			}
			if n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("j%d", max+1)
}

func (f *fold) apply(e eventRow) {
	if e.seq > f.maxSeq {
		f.maxSeq = e.seq
	}
	switch e.op {
	case "create":
		f.applyCreate(e)
	case "compact":
		f.applyCompact(e)
	default:
		f.applyVerb(e)
	}
}

func (f *fold) applyCreate(e eventRow) {
	var a struct {
		Name   string  `json:"name"`
		Prompt string  `json:"prompt"`
		Cron   string  `json:"cron"`
		At     *string `json:"at"`
		Cwd    string  `json:"cwd"`
		Model  string  `json:"model"`
		Busy   string  `json:"busy"`
	}
	if json.Unmarshal([]byte(e.args), &a) != nil || a.Name == "" {
		return
	}

	for _, j := range f.jobs {
		if j.State != "removed" && j.Name == a.Name {
			return
		}
	}
	id := f.mintID()
	var at string
	if a.At != nil {
		at = *a.At
	}
	f.jobs[id] = &jobState{
		ID: id, Name: a.Name, Prompt: a.Prompt, Cron: a.Cron,
		At: at, Cwd: a.Cwd, Model: a.Model,
		Busy: busyOf(a.Busy), State: "active",
		CreatedSeq: e.seq, UpdatedSeq: e.seq,
	}
}

func busyOf(b string) string {
	if b == "force" {
		return "force"
	}
	return "skip"
}

func (f *fold) applyVerb(e eventRow) {
	var a struct {
		ID     string   `json:"id"`
		Status string   `json:"status"`
		Exit   *float64 `json:"exit"`
		Reason string   `json:"reason"`
	}
	if json.Unmarshal([]byte(e.args), &a) != nil || a.ID == "" {
		return
	}
	j, ok := f.jobs[a.ID]
	if !ok {
		return
	}
	switch e.op {
	case "pause":
		if j.State != "active" {
			return
		}
		j.State = "paused"
	case "resume":
		if j.State != "paused" {
			return
		}
		j.State = "active"
	case "remove":
		if j.State == "removed" {
			return
		}
		j.State = "removed"
	case "run":
		if a.Status != "" {
			j.LastStatus = a.Status
		}
		j.LastTs = e.ts
		if a.Exit != nil {
			j.LastExit = int64(*a.Exit)
			j.LastExitSet = true
		}
	default:
		return
	}
	j.UpdatedSeq = e.seq
}

type compactJob struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prompt     string  `json:"prompt"`
	Cron       string  `json:"cron"`
	At         *string `json:"at"`
	Cwd        string  `json:"cwd"`
	Model      string  `json:"model"`
	Busy       string  `json:"busy"`
	State      string  `json:"state"`
	CreatedSeq int64   `json:"created_seq"`
	UpdatedSeq int64   `json:"updated_seq"`
	LastStatus *string `json:"lastStatus"`
	LastTs     *string `json:"lastTs"`
	LastExit   *int64  `json:"lastExit"`
}

func (f *fold) applyCompact(e eventRow) {
	var a struct {
		Jobs []compactJob `json:"jobs"`
	}
	tasks := map[string]*jobState{}
	if json.Unmarshal([]byte(e.args), &a) == nil {
		for _, r := range a.Jobs {
			if r.ID == "" || r.Name == "" {
				continue
			}
			state := r.State
			switch state {
			case "active", "paused", "done", "removed":
			default:
				continue
			}
			j := &jobState{
				ID: r.ID, Name: r.Name, Prompt: r.Prompt, Cron: r.Cron,
				Cwd: r.Cwd, Model: r.Model, Busy: busyOf(r.Busy),
				State: state,
			}
			if r.At != nil {
				j.At = *r.At
			}
			if r.LastStatus != nil {
				j.LastStatus = *r.LastStatus
			}
			if r.LastTs != nil {
				j.LastTs = *r.LastTs
			}
			if r.LastExit != nil {
				j.LastExit = *r.LastExit
				j.LastExitSet = true
			}
			tasks[r.ID] = j
		}
	}
	for _, j := range tasks {
		j.CreatedSeq = e.seq
		j.UpdatedSeq = e.seq
	}
	f.jobs = tasks
	f.compactSeq = e.seq
}

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
			ID: j.ID, Name: j.Name, Prompt: j.Prompt, Cron: j.Cron,
			At: at, Cwd: j.Cwd, Model: j.Model, Busy: j.Busy, State: j.State,
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
			`INSERT INTO jobs (id, name, prompt, cron, at, cwd, model, busy, state,
			    last_status, last_ts, last_exit, created_seq, updated_seq)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			j.ID, j.Name, j.Prompt, j.Cron, nullStr(j.At), j.Cwd, j.Model, j.Busy, j.State,
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

func replyText(note string, lines []string) string {
	var b strings.Builder
	if note != "" {
		fmt.Fprintf(&b, "%s\n", note)
	}
	b.WriteString(strings.Join(lines, "\n"))
	return b.String()
}
