package scheduler

import (
	"context"
	"strings"
	"time"
)

func Show(ctx context.Context, db DB, ct Crontab, id string, home string, probe func(key string) bool, now func() time.Time) (string, error) {
	_, tx, err := db.TxReadOnly(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx)
	if err != nil {
		return "", err
	}
	j, ok := f.jobs[id]
	if !ok {
		return "", schedErr("no job '%s' (list shows every job)", id)
	}
	if j.State == "removed" {
		return "", schedErr("'%s' is removed; list shows the live jobs", id)
	}
	text, err := ct.List()
	var line *TaggedLine
	if err == nil {
		line = findLine(text, id, home)
	}
	running := false
	if probe != nil {
		running = probe(id)
	}
	lines := jobLines(j, line, running, now)
	if err != nil && !strings.Contains(strings.Join(lines, "\n"), "drift: ") {
		lines = append(lines, "  drift: crontab unreadable")
	}
	out := strings.Join(lines, "\n")
	last, err := runRows(tx, id, 1)
	if err != nil {
		return "", err
	}
	if len(last) > 0 {
		out += "\n  last " + runLine(last[0])
	}
	return out, nil
}
