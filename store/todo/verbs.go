package todo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/store"
	"sort"
	"strconv"
	"strings"
	"time"
)

func Create(ctx context.Context, db store.DB, p Project, items []CreateItem, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		before := renderQueue(f, session, modePresent, 0, p.Label)
		modified, given, fresh, problems := planCreate(f, items)
		if len(problems) != 0 {
			sort.Strings(problems)
			return "", fmt.Errorf("todo: %s%s\n%s", strings.Join(problems, "; "), linkFormsHint(problems), before)
		}
		note := mergeNote(given, fresh)
		if len(items) == 0 {
			f.tasks = map[string]*taskState{}
			note = "queue cleared"
		}
		args, _ := json.Marshal(map[string]any{"tasks": asGiven(items)})
		seq := f.nextSeq()
		if _, e := appendEvent(bound, seq, "create", string(args), session, p.Key); e != nil {
			return "", e
		}
		for _, ts := range modified {
			ts.updatedSeq = seq
			ts.updatedTs = nowRFC3339()
		}
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(replyText(f, session, note, modePresent, 0, p.Label), foot), nil
	})
}

func mergeNote(given, fresh int) string {
	switch {
	case given == 0:
		return "queue unchanged: no task text given"
	case fresh == 0:
		return "queue merged: nothing new"
	case given-fresh == 0:
		return "queue merged: " + strconv.Itoa(fresh) + " new"
	default:
		return "queue merged: " + strconv.Itoa(fresh) + " new, " + strconv.Itoa(given-fresh) + " already there"
	}
}

func Start(ctx context.Context, db store.DB, p Project, id, session string, worker bool) (string, error) {
	return verb(ctx, db, p, session, id, func(f *folded, ts *taskState) (ok, noop bool, voice string) {
		switch ts.status {
		case statusPending:
			if worker {
				return false, false, "'" + id + "' is not claimed by you; a worker does not start the supervisor's board entries"
			}
			return true, false, ""
		case statusActive:
			if ts.owner == "" || ts.owner == session {
				return true, true, ""
			}
			return false, false, "'" + id + "' is already in progress (claimed by " + ts.owner + ")"
		case statusReview:
			return false, false, "'" + id + "' is in review; accept or reject it first"
		case statusDone:
			return false, false, "'" + id + "' is done; read-only"
		default:
			return false, false, "'" + id + "' failed; retry it first"
		}
	}, "start", statusActive, "'"+id+"' started")
}

func Complete(ctx context.Context, db store.DB, p Project, id, session string, worker bool) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		switch ts.status {
		case statusDone:
			return echoTask(f, session, id, "'"+id+"' completed", ""), nil
		case statusReview:
			return "", fmt.Errorf("'%s' is in review; accept or reject it first", id)
		case statusFailed:
			return "", fmt.Errorf("'%s' failed; retry it first", id)
		}
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		if ts.owner != "" && ts.owner != session {
			if worker {
				return "", fmt.Errorf("'%s' is claimed by %s", id, ts.owner)
			}
			return "", fmt.Errorf("'%s' is claimed by %s; fail it first to take over", id, ts.owner)
		}
		if worker && ts.status == statusPending {
			return "", fmt.Errorf("'%s' is not claimed by you; a worker does not complete the supervisor's board entries", id)
		}
		if blockers := blockedBy(f, ts); len(blockers) != 0 {
			return "", fmt.Errorf("%s (%s)", blockedVoice(id, blockers), blockHint(f, blockers))
		}

		args, _ := json.Marshal(map[string]any{"id": id})
		note := "'" + id + "' completed"
		if worker {
			note += "; in review"
		}
		if ts.status == statusPending {
			startSeq := f.nextSeq()
			if _, e := appendEvent(bound, startSeq, "start", string(args), session, p.Key); e != nil {
				return "", e
			}
			ts.status = statusActive
			ts.owner = session
			ts.updatedSeq = startSeq
			ts.updatedTs = nowRFC3339()
			if worker {
				note = "'" + id + "' auto-started and submitted for review"
			} else {
				note = "'" + id + "' auto-started and completed"
			}
		}
		seq := f.nextSeq()
		if _, e := appendEvent(bound, seq, "complete", string(args), session, p.Key); e != nil {
			return "", e
		}
		ts.status = statusReview
		ts.owner = ""
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if !worker {
			acceptSeq := f.nextSeq()
			if _, e := appendEvent(bound, acceptSeq, "accept", string(args), session, p.Key); e != nil {
				return "", e
			}
			ts.status = statusDone
			ts.updatedSeq = acceptSeq
			ts.updatedTs = nowRFC3339()
		}
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, note, ""), foot), nil
	})
}

func Fail(ctx context.Context, db store.DB, p Project, id, session string, worker bool) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		var voice string
		switch ts.status {
		case statusPending:
			if worker {
				voice = "'" + id + "' is not claimed by you; a worker does not fail the supervisor's board entries"
			} else {
				voice = "'" + id + "' is pending; start it first"
			}
		case statusReview:
			if ts.owner != session {
				voice = "'" + id + "' is in review; accept or reject it first"
			}
		case statusDone:
			voice = "'" + id + "' is done; read-only"
		case statusFailed:
			voice = "'" + id + "' is already failed"
		}
		if voice != "" {
			return "", fmt.Errorf("%s", voice)
		}
		if worker && ts.owner != "" && ts.owner != session {
			return "", fmt.Errorf("'%s' is claimed by %s", id, ts.owner)
		}
		released := ""
		if ts.owner != "" && ts.owner != session {
			released = ts.owner
		}
		note := "'" + id + "' failed"
		if released != "" {
			note += " (released from " + released + ")"
		}
		args, _ := json.Marshal(map[string]any{"id": id})
		seq := f.nextSeq()
		if _, e := appendEvent(bound, seq, "fail", string(args), session, p.Key); e != nil {
			return "", e
		}
		ts.status = statusFailed
		ts.owner = ""
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, note, ""), foot), nil
	})
}

func Retry(ctx context.Context, db store.DB, p Project, id, session string) (string, error) {
	return verb(ctx, db, p, session, id, func(f *folded, ts *taskState) (ok, noop bool, voice string) {
		if ts.status == statusFailed {
			return true, false, ""
		}
		return false, false, "'" + id + "' is not failed; nothing to retry"
	}, "retry", statusPending, "'"+id+"' back to pending")
}

const MaxNoteLen = 1000

func Claim(ctx context.Context, db store.DB, p Project, session, status string) (string, error) {
	if session == "" {
		session = anon
	}
	if status != "" && status != statusReview {
		return "", fmt.Errorf("todo: unknown claim status %q (only %s)", status, statusReview)
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		var ts *taskState
		for _, ot := range orderedTaskStates(f) {
			if status == statusReview {
				if ot.status == statusReview && ot.owner == "" {
					ts = ot
					break
				}
				continue
			}
			if ot.status == statusPending && len(blockedBy(f, ot)) == 0 {
				ts = ot
				break
			}
		}
		if ts == nil {
			return withFoot("nothing to do", foot), nil
		}
		args, _ := json.Marshal(map[string]any{"id": ts.id})
		seq := f.nextSeq()
		if _, e := appendEvent(bound, seq, "claim", string(args), session, p.Key); e != nil {
			return "", e
		}
		note := "'" + ts.id + "' claimed"
		if ts.status == statusPending {
			ts.status = statusActive
		} else {
			note = "'" + ts.id + "' claimed for review"
		}
		ts.owner = session
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, ts.id, note, p.Dir), foot), nil
	})
}

func Note(ctx context.Context, db store.DB, p Project, id, text, session string) (string, error) {
	if session == "" {
		session = anon
	}
	note, err := cleanNote(text, "note")
	if err != nil {
		return "", err
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		if _, ok := f.tasks[id]; !ok {
			return "", unknownTask(p, id)
		}
		args, _ := json.Marshal(map[string]any{"id": id, "note": note})
		seq := f.nextSeq()
		ts, e := appendEvent(bound, seq, "note", string(args), session, p.Key)
		if e != nil {
			return "", e
		}
		f.tasks[id].notes = append(f.tasks[id].notes, noteState{text: note, session: session, ts: ts})
		return withFoot(echoTask(f, session, id, "note added to '"+id+"'", ""), foot), nil
	})
}

func cleanNote(text, verb string) (string, error) {
	note := strings.Join(strings.Fields(text), " ")
	if note == "" {
		if verb == "reject" {
			return "", fmt.Errorf("todo: reject requires a reason")
		}
		return "", fmt.Errorf("todo: note must not be empty")
	}
	if len(note) > MaxNoteLen {
		return "", fmt.Errorf("todo: %s is too long (%d chars; max %d)", verb, len(note), MaxNoteLen)
	}
	return note, nil
}

func Accept(ctx context.Context, db store.DB, p Project, id, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		if e := reviewHold(ts, id, session); e != nil {
			return "", e
		}
		if blockers := blockedBy(f, ts); len(blockers) != 0 {
			return "", fmt.Errorf("%s (%s)", blockedVoice(id, blockers), blockHint(f, blockers))
		}
		args, _ := json.Marshal(map[string]any{"id": id})
		note := "'" + id + "' accepted"
		if ts.owner == "" {
			claimSeq := f.nextSeq()
			if _, e := appendEvent(bound, claimSeq, "claim", string(args), session, p.Key); e != nil {
				return "", e
			}
			ts.owner = session
			ts.updatedSeq = claimSeq
			ts.updatedTs = nowRFC3339()
			note = "'" + id + "' auto-claimed and accepted"
		}
		seq := f.nextSeq()
		if _, e := appendEvent(bound, seq, "accept", string(args), session, p.Key); e != nil {
			return "", e
		}
		ts.status = statusDone
		ts.owner = ""
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, note, ""), foot), nil
	})
}

func Reject(ctx context.Context, db store.DB, p Project, id, reason, session string) (string, error) {
	if session == "" {
		session = anon
	}
	note, err := cleanNote(reason, "reject")
	if err != nil {
		return "", err
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		if e := reviewHold(ts, id, session); e != nil {
			return "", e
		}
		args, _ := json.Marshal(map[string]any{"id": id, "note": note})
		reply := "'" + id + "' rejected; reason noted"
		if ts.owner == "" {
			claimSeq := f.nextSeq()
			if _, e := appendEvent(bound, claimSeq, "claim", string(args), session, p.Key); e != nil {
				return "", e
			}
			ts.owner = session
			ts.updatedSeq = claimSeq
			ts.updatedTs = nowRFC3339()
			reply = "'" + id + "' auto-claimed and rejected; reason noted"
		}
		seq := f.nextSeq()
		rejectTs, e := appendEvent(bound, seq, "reject", string(args), session, p.Key)
		if e != nil {
			return "", e
		}
		ts.status = statusPending
		ts.owner = ""
		ts.notes = append(ts.notes, noteState{text: note, session: session, ts: rejectTs})
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, reply, ""), foot), nil
	})
}

func reviewHold(ts *taskState, id, session string) error {
	switch ts.status {
	case statusPending:
		return fmt.Errorf("'%s' is pending; not in review", id)
	case statusActive:
		return fmt.Errorf("'%s' is in progress; complete it first", id)
	case statusDone:
		return fmt.Errorf("'%s' is done; read-only", id)
	case statusFailed:
		return fmt.Errorf("'%s' failed; retry it first", id)
	}
	if ts.owner != "" && ts.owner != session {
		return fmt.Errorf("'%s' is claimed for review by %s", id, ts.owner)
	}
	return nil
}

func Move(ctx context.Context, db store.DB, p Project, id string, pos int, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		if pos < 1 || pos > len(f.tasks) {
			return "", fmt.Errorf("move position for '%s' must be between 1 and %d, got %d", id, len(f.tasks), pos)
		}
		args, _ := json.Marshal(map[string]any{"id": id, "pos": pos})
		seq := f.nextSeq()
		if _, e := appendEvent(bound, seq, "move", string(args), session, p.Key); e != nil {
			return "", e
		}
		if appliedMove(f, ts, pos) {
			ts.updatedSeq = seq
			ts.updatedTs = nowRFC3339()
		}
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, "'"+id+"' moved to position "+strconv.Itoa(pos), ""), foot), nil
	})
}

func Release(ctx context.Context, db store.DB, p Project, id, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		switch ts.status {
		case statusDone:
			return "", fmt.Errorf("'%s' is done; read-only", id)
		case statusFailed:
			return "", fmt.Errorf("'%s' failed; retry it first", id)
		case statusPending:
			return "", fmt.Errorf("'%s' is not claimed; start it first", id)
		}
		review := ts.status == statusReview
		if ts.owner == session {
			if review {
				return "", fmt.Errorf("'%s' is claimed for review by you; accept or reject it", id)
			}
			return "", fmt.Errorf("'%s' is claimed by you; complete or fail it", id)
		}
		owner := ts.owner
		if owner == "" {
			if review {
				return "", fmt.Errorf("'%s' is in review and not claimed for review", id)
			}
			return "", fmt.Errorf("'%s' is not claimed; start it first", id)
		}
		if !staleClaim(ts, time.Now()) {
			if review {
				return "", fmt.Errorf("'%s' is claimed for review by %s (fresh); a live claim is not released", id, owner)
			}
			return "", fmt.Errorf("'%s' is claimed by %s (fresh); a live claim is not released — fail it first to take over", id, owner)
		}
		args, _ := json.Marshal(map[string]any{"id": id})
		seq := f.nextSeq()
		if _, e := appendEvent(bound, seq, "release", string(args), session, p.Key); e != nil {
			return "", e
		}
		if review {
			ts.owner = ""
			ts.updatedSeq = seq
			ts.updatedTs = nowRFC3339()
			if e := rewrite(tx, f, p.Key); e != nil {
				return "", e
			}
			return withFoot(echoTask(f, session, id, "'"+id+"' released (was claimed for review by "+owner+")", ""), foot), nil
		}
		ts.status = statusPending
		ts.owner = ""
		ts.updatedSeq = seq
		ts.updatedTs = nowRFC3339()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return withFoot(echoTask(f, session, id, "'"+id+"' released (was claimed by "+owner+")", ""), foot), nil
	})
}

func Reap(ctx context.Context, db store.DB, p Project, ended []string, session string) (string, error) {
	if session == "" {
		session = anon
	}
	dead := map[string]bool{}
	for _, id := range ended {
		dead[id] = true
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		now := time.Now()
		released := []string{}
		for id, ts := range f.tasks {
			if (ts.status != statusActive && ts.status != statusReview) || ts.owner == "" || ts.owner == session {
				continue
			}
			if !dead[ts.owner] && !staleClaim(ts, now) {
				continue
			}
			owner := ts.owner
			args, _ := json.Marshal(map[string]any{"id": id})
			seq := f.nextSeq()
			if _, e := appendEvent(bound, seq, "release", string(args), session, p.Key); e != nil {
				return "", e
			}
			claimed := "was claimed by "
			if ts.status == statusReview {
				claimed = "was claimed for review by "
			} else {
				ts.status = statusPending
			}
			ts.owner = ""
			ts.updatedSeq = seq
			ts.updatedTs = nowRFC3339()
			released = append(released, id+" ("+claimed+owner+")")
		}
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		if len(released) == 0 {
			return withFoot("", foot), nil
		}
		sort.Strings(released)
		note := "released " + strconv.Itoa(len(released)) + " dead claim" + claimPlural(len(released)) + ": " + strings.Join(released, ", ")
		return withFoot("\u2192 "+note, foot), nil
	})
}

func Prune(ctx context.Context, db store.DB, p Project, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		foot, e := maybeCompact(bound, tx, f, session, p.Key)
		if e != nil {
			return "", e
		}
		n := 0
		for _, ts := range f.tasks {
			if ts.status == statusDone {
				n++
			}
		}
		if n == 0 {
			return withFoot(replyText(f, session, "nothing to prune (no done tasks)", modePresent, 0, p.Label), foot), nil
		}
		seq := f.nextSeq()
		args, _ := json.Marshal(map[string]any{"done": n})
		if _, e := appendEvent(bound, seq, "prune", string(args), session, p.Key); e != nil {
			return "", e
		}
		f.applyPrune()
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		note := "pruned " + strconv.Itoa(n) + " done task" + claimPlural(n)
		return withFoot(replyText(f, session, note, modePresent, 0, p.Label), foot), nil
	})
}

func (f *folded) applyPrune() {
	for id, ts := range f.tasks {
		if ts.status == statusDone {
			delete(f.tasks, id)
		}
	}
}

func staleClaim(ts *taskState, now time.Time) bool {
	t, err := time.Parse(time.RFC3339, ts.updatedTs)
	if err != nil {
		return false
	}
	return now.Sub(t) > StaleClaimAfter
}

func claimPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
