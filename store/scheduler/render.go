package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// --- reply shaping (pane's renderers, themeless) and List ---
//
// Voices and shapes are pane's; ANSI/theme tokens drop to plain text, as
// this runtime's other store replies do.

// nextText is the head-line's scheduling bit (pane's nextText).
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

// lastText is the head-line's audit bit (pane's lastText).
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

// driftOf computes the store/crontab mismatch note (pane's view() drift).
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

// jobLines is one job's rendered lines (pane's jobLines, themeless).
func jobLines(j *jobState, scope string, line *TaggedLine, running bool, now func() time.Time) []string {
	head := fmt.Sprintf("%s %s %s", j.ID, j.Name, j.State)
	if s := nextText(j, now); s != "" {
		head += " · " + s
	}
	if s := lastText(j); s != "" {
		head += " · " + s
	}
	line2 := fmt.Sprintf("  scope %s · cron %s%s · %s%s · %s",
		scope, j.Cron,
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

// renderJobs is pane's sectioned listing: global first, then cwd.
func renderJobs(global, cwd []jobLineSet) string {
	section := func(scope string, jobs []jobLineSet) string {
		if len(jobs) == 0 {
			return scope + ": no jobs"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s:\n", scope)
		for i, j := range jobs {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(strings.Join(j.lines, "\n"))
		}
		return b.String()
	}
	return section("global", global) + "\n" + section("cwd", cwd)
}

type jobLineSet struct {
	lines []string
}

// List renders both scopes with drift and running probes (pane's
// core.list). Probe nil means "not held"; now nil means "do not compute
// next fires". Voice is pane's.
func List(ctx context.Context, st Stores, ct Crontab, sessionCwd string, probe func(key string) bool, now func() time.Time) (string, error) {
	var lines map[string]TaggedLine
	if text, err := ct.List(); err == nil {
		lines = map[string]TaggedLine{}
		for _, l := range Scan(text) {
			lines[l.Key] = l
		}
	} else {
		lines = nil // crontab unreadable: every job drifts
	}
	scopeOf := func(db DB) string {
		if db.DB == st.Global.DB {
			return "global"
		}
		return "cwd"
	}
	read := func(db DB) ([]jobLineSet, error) {
		_, tx, err := db.TxReadOnly(ctx)
		if err != nil {
			return nil, err
		}
		f, err := eventsOf(tx)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		tx.Rollback()
		var out []jobLineSet
		scope := scopeOf(db)
		for id := range f.jobs {
			j := f.jobs[id]
			if j.State == "removed" {
				continue
			}
			key, err := KeyOf(scope, scopeHash(scope, sessionCwd), id)
			if err != nil {
				return nil, err
			}
			running := false
			if probe != nil {
				running = probe(key)
			}
			var line *TaggedLine
			if lines != nil {
				if l, ok := lines[key]; ok {
					line = &l
				}
			}
			out = append(out, jobLineSet{lines: jobLines(j, scope, line, running, now)})
		}
		return out, nil
	}
	global, err := read(st.Global)
	if err != nil {
		return "", err
	}
	cwd, err := read(st.Cwd)
	if err != nil {
		return "", err
	}
	// drift is computed against the job's own scope store above; an
	// unreadable crontab flags every job with pane's voice
	if lines == nil {
		reflag := func(jobs []jobLineSet) []jobLineSet {
			for i := range jobs {
				if !strings.Contains(strings.Join(jobs[i].lines, "\n"), "drift: ") {
					jobs[i].lines = append(jobs[i].lines, "  drift: crontab unreadable")
				}
			}
			return jobs
		}
		global = reflag(global)
		cwd = reflag(cwd)
	}
	return renderJobs(global, cwd), nil
}
