package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func nextText(j *jobState, now func() time.Time) string {
	if j.State != "active" {
		return ""
	}
	if j.Cron == "once" {
		if j.At != "" {
			return "at " + j.At
		}
		return "at passed"
	}
	if now == nil {
		return ""
	}
	fire, ok := NextFire(mustParse(j.Cron), now())
	if !ok {
		return "never"
	}
	return "next " + fire.UTC().Format(time.RFC3339)
}

func mustParse(cron string) ParsedCron {
	p, err := ValidateCron(cron)
	if err != nil {
		return ParsedCron{}
	}
	return p
}

func lastText(j *jobState) string {
	if j.LastStatus == "" {
		return ""
	}
	bits := []string{j.LastStatus}
	if j.LastExitSet {
		bits = append(bits, fmt.Sprintf("exit %d", j.LastExit))
	}
	if j.LastTs != "" {
		bits = append(bits, j.LastTs)
	}
	return strings.Join(bits, " ")
}

func driftOf(j *jobState, line *TaggedLine) string {
	if j.State == "removed" {
		return ""
	}
	if line == nil {
		return "no crontab line"
	}
	var notes []string
	if j.State == "paused" && !line.Paused {
		notes = append(notes, "line is active")
	}
	if j.State != "paused" && line.Paused {
		notes = append(notes, "line is paused")
	}
	if line.Cron != j.Cron {
		notes = append(notes, "cron differs (crontab: "+line.Cron+")")
	}
	return strings.Join(notes, "; ")
}

func jobLines(j *jobState, line *TaggedLine, running bool, now func() time.Time) []string {
	head := fmt.Sprintf("%s %s %s", j.ID, j.Name, j.State)
	if s := nextText(j, now); s != "" {
		head += " · " + s
	}
	if s := lastText(j); s != "" {
		head += " · " + s
	}
	line2 := fmt.Sprintf("  cron %s%s · %s%s · %s",
		j.Cron,
		func() string {
			if j.At != "" {
				return " · at " + j.At
			}
			return ""
		}(),
		j.Model,
		func() string {
			if j.Busy == "force" {
				return " · busy force"
			}
			return ""
		}(),
		j.Cwd)
	lines := []string{head, line2}
	if d := driftOf(j, line); d != "" {
		lines = append(lines, "  drift: "+d)
	}
	if running {
		lines = append(lines, "  running (lock held)")
	}
	return lines
}

type jobLineSet struct {
	lines []string
}

func List(ctx context.Context, db DB, ct Crontab, sessionCwd string, probe func(key string) bool, now func() time.Time) (string, error) {
	var lines map[string]TaggedLine
	if text, err := ct.List(); err == nil {
		lines = map[string]TaggedLine{}
		for _, l := range Scan(text) {
			lines[l.Key] = l
		}
	} else {
		lines = nil
	}
	_, tx, err := db.TxReadOnly(ctx)
	if err != nil {
		return "", err
	}
	f, err := eventsOf(tx)
	if err != nil {
		tx.Rollback()
		return "", err
	}
	tx.Rollback()

	groups := map[string][]jobLineSet{}
	for id := range f.jobs {
		j := f.jobs[id]
		if j.State == "removed" {
			continue
		}
		running := false
		if probe != nil {
			running = probe(id)
		}
		var line *TaggedLine
		if lines != nil {
			if l, ok := lines[id]; ok {
				line = &l
			}
		}
		out := jobLines(j, line, running, now)
		groups[j.Cwd] = append(groups[j.Cwd], jobLineSet{lines: out})
	}

	if lines == nil {
		for cwd, g := range groups {
			for i := range g {
				if !strings.Contains(strings.Join(g[i].lines, "\n"), "drift: ") {
					g[i].lines = append(g[i].lines, "  drift: crontab unreadable")
				}
			}
			groups[cwd] = g
		}
	}

	if len(groups) == 0 {
		return "scheduler: no jobs (global.sqlite)", nil
	}
	var dirs []string
	if g, ok := groups[sessionCwd]; ok && len(g) > 0 {
		dirs = append(dirs, sessionCwd)
	}
	for d := range groups {
		if d != sessionCwd {
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	if sessionCwd != "" {
		head := []string{}
		if _, ok := groups[sessionCwd]; ok {
			head = []string{sessionCwd}
		}
		var rest []string
		for _, d := range dirs {
			if d != sessionCwd {
				rest = append(rest, d)
			}
		}
		dirs = append(head, rest...)
	}

	var b strings.Builder
	for i, d := range dirs {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s:\n", d)
		group := groups[d]
		for j, g := range group {
			if j > 0 {
				b.WriteString("\n")
			}
			b.WriteString(strings.Join(g.lines, "\n"))
		}
	}
	return b.String(), nil
}
