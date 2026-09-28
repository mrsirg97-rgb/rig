package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func createdRow(f *fold, seq int64) *jobState {
	for _, j := range f.jobs {
		if j.State != "removed" && j.UpdatedSeq == seq {
			return j
		}
	}
	return nil
}

func Pause(ctx context.Context, db DB, ct Crontab, id, sessionCwd, session, home string) (string, error) {
	return stateAction(ctx, db, ct, id, sessionCwd, session, "pause", home)
}

func Resume(ctx context.Context, db DB, ct Crontab, id, sessionCwd, session, home string) (string, error) {
	return stateAction(ctx, db, ct, id, sessionCwd, session, "resume", home)
}

func Remove(ctx context.Context, db DB, ct Crontab, id, sessionCwd, session, home string) (string, error) {
	return stateAction(ctx, db, ct, id, sessionCwd, session, "remove", home)
}

func findLine(text, id, home string) *TaggedLine {
	for _, l := range Scan(text, home) {
		if l.Key == id {
			cp := l
			return &cp
		}
	}
	return nil
}

func Repair(ctx context.Context, db DB, ct Crontab, id, runnerCmd, home string) (string, error) {
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

	text, err := ct.List()
	if err != nil {
		return "", err
	}

	if id != "" {
		job, found := f.jobs[id]
		if !found {
			return "", schedErr("no job '%s'", id)
		}
		if job.State == "removed" {
			return "", schedErr("'%s' is removed; nothing to repair", id)
		}
		if job.State == "done" {
			return "", schedErr("'%s' is done; nothing to repair", id)
		}
		d := driftOf(job, findLine(text, id, home))
		if d == "" {
			return fmt.Sprintf("'%s' is in sync", id), nil
		}
		next, _ := UpsertLine(text, id, job.Cron, runnerCmd, home)
		if job.State == "paused" {
			next, _ = SetPaused(next, id, true, home)
		}
		if next != text {
			if err := ct.Install(next); err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("'%s' repaired: %s", id, d), nil
	}

	var ids []string
	for key := range f.jobs {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	next := text
	var replies []string
	for _, key := range ids {
		j := f.jobs[key]
		if j.State == "removed" || j.State == "done" {
			continue
		}
		d := driftOf(j, findLine(next, key, home))
		if d == "" {
			continue
		}
		next, _ = UpsertLine(next, key, j.Cron, runnerCmd, home)
		if j.State == "paused" {
			next, _ = SetPaused(next, key, true, home)
		}
		replies = append(replies, fmt.Sprintf("'%s' repaired: %s", key, d))
	}
	if len(replies) == 0 {
		return "nothing drifted", nil
	}
	if next != text {
		if err := ct.Install(next); err != nil {
			return "", err
		}
	}
	return strings.Join(replies, "\n"), nil
}

func stateAction(ctx context.Context, db DB, ct Crontab, id, sessionCwd, session, action, home string) (string, error) {
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
		op = func(t string) (string, bool) { return SetPaused(t, key, true, home) }
	case "resume":
		op = func(t string) (string, bool) { return SetPaused(t, key, false, home) }
	case "remove":
		op = func(t string) (string, bool) { return RemoveLine(t, key, home) }
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
