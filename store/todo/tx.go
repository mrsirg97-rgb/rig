package todo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/store"
	tododomain "github.com/mrsirg97-rgb/rig/v2/store/todo/domain"
	"sort"
	"strings"
	"time"
)

func verb(
	ctx context.Context,
	db store.DB,
	p Project,
	session, id string,
	check func(f *folded, ts *taskState) (ok, noop bool, voice string),
	op, toStatus, note string,
) (string, error) {
	return verbOn(ctx, db, p, session, fixedID(id), func(_ string, f *folded, ts *taskState) (bool, bool, string) {
		return check(f, ts)
	}, op, toStatus, func(string) string { return note })
}

func fixedID(id string) func(*folded) (string, error) {
	return func(*folded) (string, error) { return id, nil }
}

func verbOn(
	ctx context.Context,
	db store.DB,
	p Project,
	session string,
	pick func(f *folded) (string, error),
	check func(id string, f *folded, ts *taskState) (ok, noop bool, voice string),
	op, toStatus string,
	noteOf func(id string) string,
) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		id, err := pick(f)
		if err != nil {
			return "", err
		}
		note := noteOf(id)
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		ok, noop, voice := check(id, f, ts)
		if !ok {
			return "", fmt.Errorf("%s", voice)
		}
		if noop {
			return echoTask(f, session, id, note), nil
		}
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		args, _ := json.Marshal(map[string]any{"id": id})
		seq := f.nextSeq()
		if _, e := appendEvent(bound, seq, op, string(args), session, p.Key); e != nil {
			return "", e
		}
		ts.status = toStatus
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if toStatus == statusActive {
			if session == "" {
				ts.owner = anon
			} else {
				ts.owner = session
			}
		} else {
			ts.owner = ""
		}
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, note), foot), nil
	})
}

func replyText(f *folded, session, note string, mode readMode, n int, label string) string {
	var b strings.Builder
	if note != "" {
		fmt.Fprintf(&b, "\u2192 %s\n", note)
	}
	b.WriteString(renderQueue(f, session, mode, n, label))
	if foot := staleFooter(f); foot != "" {
		b.WriteString("\n" + foot)
	}
	return b.String()
}

const (
	STALE_THRESHOLD_SEQ      = 200
	COMPACT_THRESHOLD_EVENTS = 1000
)

func maybeCompact(bound context.Context, tx *sql.Tx, f *folded, session, scope string) (string, error) {
	if f.maxSeq-f.compactSeq < COMPACT_THRESHOLD_EVENTS {
		return "", nil
	}
	folded := f.maxSeq - f.compactSeq
	args, _ := json.Marshal(map[string]any{
		"tasks": snapshotOf(f), "maxId": f.maxIdNum, "maxPos": f.maxPos,
	})
	seq := f.nextSeq()
	if _, e := appendEvent(bound, seq, "compact", string(args), session, scope); e != nil {
		return "", e
	}
	f.compactSeq = seq
	f.maxSeq = seq
	tsStr := nowRFC3339()
	for _, ts := range f.tasks {
		ts.createdSeq, ts.updatedSeq, ts.updatedTs = seq, seq, tsStr
		if ts.status == statusDone {
			ts.finishedSeq = seq
		}
	}
	if _, e := tx.Exec("DELETE FROM events WHERE scope = ? AND seq < ?", scope, seq); e != nil {
		return "", fmt.Errorf("todo: compact: %w", e)
	}
	return fmt.Sprintf("\u00b7 log compacted (%d events folded into the snapshot)", folded), nil
}

func snapshotOf(f *folded) []any {
	var out []any
	for _, ts := range f.tasks {
		link := func(v string) any {
			if v == "" {
				return nil
			}
			return v
		}
		owner := any(nil)
		if ts.owner != "" {
			owner = ts.owner
		}
		m := map[string]any{
			"id": ts.id, "text": ts.text, "status": ts.status,
			"pos": ts.pos - 1, "requires": link(ts.requires), "blocks": link(ts.blocks),
			"owner": owner, "updatedTs": ts.updatedTs,
		}
		if len(ts.notes) != 0 {
			notes := []any{}
			for _, n := range ts.notes {
				notes = append(notes, map[string]any{"note": n.text, "session": n.session, "ts": n.ts})
			}
			m["notes"] = notes
		}
		out = append(out, m)
	}
	return out
}

func appliedMove(f *folded, ts *taskState, pos int) bool {
	if pos < 1 || pos > len(f.tasks) {
		return false
	}
	ordered := orderedTaskStates(f)
	idx := -1
	for i, ot := range ordered {
		if ot == ts {
			idx = i
			break
		}
	}
	if idx == -1 || idx == pos-1 {
		return false
	}
	removed := make([]*taskState, 0, len(ordered))
	removed = append(removed, ordered[:idx]...)
	removed = append(removed, ordered[idx+1:]...)
	rest := make([]*taskState, 0, len(removed)+1)
	rest = append(rest, removed[:pos-1]...)
	rest = append(rest, ts)
	rest = append(rest, removed[pos-1:]...)
	for i, ot := range rest {
		ot.pos = i + 1
	}
	return true
}

func (f *folded) applyMoveEvent(e eventRow) {
	var payload struct {
		ID  string `json:"id"`
		Pos int    `json:"pos"`
	}
	if json.Unmarshal([]byte(e.args), &payload) != nil || payload.ID == "" {
		return
	}
	ts, ok := f.tasks[payload.ID]
	if !ok {
		return
	}
	if !appliedMove(f, ts, payload.Pos) {
		return
	}
	ts.updatedSeq = e.seq
	ts.updatedTs = e.ts
}

func (f *folded) applyCompactEvent(e eventRow) {
	tasks := map[string]*taskState{}
	var payload struct {
		MaxID  int `json:"maxId"`
		MaxPos int `json:"maxPos"`
		Tasks  []struct {
			ID        string  `json:"id"`
			Text      string  `json:"text"`
			Status    string  `json:"status"`
			Requires  *string `json:"requires"`
			DependsOn *string `json:"dependsOn"`
			Blocks    *string `json:"blocks"`
			Pos       int     `json:"pos"`
			Owner     string  `json:"owner"`
			UpdatedTs string  `json:"updatedTs"`
			Notes     []struct {
				Note    string `json:"note"`
				Session string `json:"session"`
				Ts      string `json:"ts"`
			} `json:"notes"`
		} `json:"tasks"`
	}
	if json.Unmarshal([]byte(e.args), &payload) == nil {
		for _, r := range payload.Tasks {
			if r.ID == "" || r.Text == "" {
				continue
			}
			status := r.Status
			switch status {
			case statusPending, statusActive, statusReview, statusDone, statusFailed:
			default:
				status = statusPending
			}
			pos := r.Pos + 1
			if pos < 1 {
				pos = 1
			}
			var requires, blocks string
			if r.Requires != nil {
				requires = *r.Requires
			} else if r.DependsOn != nil {
				requires = *r.DependsOn
			}
			if r.Blocks != nil {
				blocks = *r.Blocks
			}
			updatedTs := r.UpdatedTs
			if updatedTs == "" {
				updatedTs = e.ts
			}
			var notes []noteState
			for _, n := range r.Notes {
				if n.Note != "" {
					ts := n.Ts
					if ts == "" {
						ts = e.ts
					}
					notes = append(notes, noteState{text: n.Note, session: n.Session, ts: ts})
				}
			}
			tasks[r.ID] = &taskState{
				id: r.ID, text: r.Text, status: status,
				pos: pos, requires: requires, blocks: blocks, owner: r.Owner,
				updatedTs: updatedTs, notes: notes,
			}
		}
		for _, ts := range tasks {
			if ts.requires != "" && tasks[ts.requires] == nil {
				ts.requires = ""
			}
			if ts.blocks != "" && tasks[ts.blocks] == nil {
				ts.blocks = ""
			}
		}
	}
	for _, ts := range tasks {
		ts.createdSeq = e.seq
		ts.updatedSeq = e.seq
		if ts.status == statusDone {
			ts.finishedSeq = e.seq
		}
	}
	if payload.MaxID > f.maxIdNum {
		f.maxIdNum = payload.MaxID
	}
	if payload.MaxPos > f.maxPos {
		f.maxPos = payload.MaxPos
	}
	f.tasks = tasks
	f.compactSeq = e.seq
}

func unknownTask(p Project, id string) error {
	where := p.Label
	if where == "" {
		where = "this queue"
	}
	return fmt.Errorf("no task '%s' in %s (ids are minted by the tool; copy from a reply)", id, where)
}

func withFoot(reply, foot string) string {
	if foot == "" {
		return reply
	}
	return reply + "\n" + foot
}

func mutate(ctx context.Context, db store.DB, p Project, act func(bound context.Context, tx *sql.Tx, f *folded) (string, error)) (string, error) {
	bound, tx, err := db.Tx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx, p.Key)
	if err != nil {
		return "", err
	}
	f.label = p.Label
	reply, err := act(bound, tx, f)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return reply, nil
}

func eventsOf(tx *sql.Tx, scope string) (*folded, error) {
	rows, err := tx.Query("SELECT seq, op, args, session, ts, scope FROM events ORDER BY seq")
	if err != nil {
		return nil, fmt.Errorf("todo: event log: %w", err)
	}
	defer rows.Close()
	f := newFolded()
	for rows.Next() {
		var e eventRow
		var session sql.NullString
		if err := rows.Scan(&e.seq, &e.op, &e.args, &session, &e.ts, &e.scope); err != nil {
			return nil, fmt.Errorf("todo: event log: %w", err)
		}
		e.session = session.String
		f.globalSeq = e.seq
		if e.scope != scope {
			continue
		}
		f.apply(e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("todo: event log: %w", err)
	}
	return f, nil
}

func appendEvent(bound context.Context, seq int64, op, args, session, scope string) (string, error) {
	if session == "" {
		session = anon
	}
	s := session
	sess := &s
	ts := nowRFC3339()
	_, err := tododomain.NewEventDomain().InsertEvent(bound, tododomain.Event{
		Seq: seq, Ts: ts, Op: op, Args: args, Session: sess, Scope: scope,
	})
	if err != nil {
		return "", fmt.Errorf("todo: event append: %w", err)
	}
	return ts, nil
}

func rewrite(tx *sql.Tx, f *folded, scope string) error {
	if _, err := tx.Exec("DELETE FROM task_deps WHERE scope = ?", scope); err != nil {
		return fmt.Errorf("todo: rewrite: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM tasks WHERE scope = ?", scope); err != nil {
		return fmt.Errorf("todo: rewrite: %w", err)
	}
	var order []*taskState
	for _, ts := range f.tasks {
		order = append(order, ts)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].pos != order[j].pos {
			return order[i].pos < order[j].pos
		}
		return order[i].createdSeq < order[j].createdSeq
	})
	for _, ts := range order {
		_, err := tx.Exec(
			"INSERT INTO tasks (scope, id, text, status, pos, created_seq, updated_seq) VALUES (?, ?, ?, ?, ?, ?, ?)",
			scope, ts.id, ts.text, ts.status, ts.pos, ts.createdSeq, ts.updatedSeq,
		)
		if err != nil {
			return fmt.Errorf("todo: rewrite: %w", err)
		}
		for _, edge := range []struct{ kind, ref string }{
			{"requires", ts.requires}, {"blocks", ts.blocks},
		} {
			if edge.ref == "" {
				continue
			}
			_, err := tx.Exec(
				"INSERT INTO task_deps (scope, task_id, kind, depends_on, created_seq) VALUES (?, ?, ?, ?, ?)",
				scope, ts.id, edge.kind, edge.ref, ts.updatedSeq,
			)
			if err != nil {
				return fmt.Errorf("todo: rewrite: %w", err)
			}
		}
	}
	return nil
}

func asGiven(items []CreateItem) []any {
	var out []any
	for _, it := range items {
		m := map[string]any{"text": it.Text}
		switch {
		case it.Requires != nil:
			m["requires"] = *it.Requires
		case it.RequiresNull:
			m["requires"] = nil
		}
		switch {
		case it.Blocks != nil:
			m["blocks"] = *it.Blocks
		case it.BlocksNull:
			m["blocks"] = nil
		}
		out = append(out, m)
	}
	return out
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
