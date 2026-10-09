package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

func RenderSwarmBand(t Theme, st core.SwarmStatus) string {
	if !swarmAnyRunning(st.Workers) {
		return ""
	}
	var rows []string
	for _, role := range []string{"worker", "reviewer"} {
		if row, ok := swarmRoleRow(t, st, role); ok {
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return ""
	}
	return swarmRule(t) + "\n" + strings.Join(rows, "\n")
}

func IsDelegateBand(st core.SwarmStatus) bool {
	for _, w := range st.Workers {
		if w.Role == delegateRole {
			return true
		}
	}
	return false
}

const delegateRole = "delegate"

func RenderDelegateBand(t Theme, st core.SwarmStatus, since, now time.Time) string {
	var ws []core.SwarmWorker
	queued := 0
	for _, w := range st.Workers {
		switch {
		case w.Role != delegateRole:
		case w.State == "running":
			ws = append(ws, w)
		case w.State == "queued":
			queued++
		}
	}
	if len(ws) == 0 || since.IsZero() {
		return ""
	}
	sep := t.Paint(SlotDim, " "+t.Glyph(GlyphDot)+" ")
	word := wordFor(len(ws))
	head := t.Paint(SlotDim, "delegating") + sep +
		t.Paint(SlotText, fmt.Sprintf("%d %s", len(ws), word)) + sep
	if queued > 0 {
		head += t.Paint(SlotText, fmt.Sprintf("%d queued", queued)) + sep
	}
	head += t.Paint(SlotDim, swarmAge(now.Sub(since)))
	return swarmRule(t) + "\n" + head + "\n" + t.Paint(SlotDim, swarmCallRow(ws, now))
}

func wordFor(n int) string {
	if n == 1 {
		return "worker"
	}
	return "workers"
}

func swarmCallRow(ws []core.SwarmWorker, now time.Time) string {
	recent, found := swarmLatestCall(ws)
	if !found {
		return "—"
	}
	age := "—"
	if !recent.ToolAt.IsZero() {
		age = swarmAge(now.Sub(recent.ToolAt))
	}
	return fmt.Sprintf("#%d %s · %s", recent.ID, recent.Tool, age)
}

func swarmLatestCall(ws []core.SwarmWorker) (core.SwarmWorker, bool) {
	var best core.SwarmWorker
	found := false
	for _, w := range ws {
		if w.Tool == "" {
			continue
		}
		if !found || w.ToolAt.After(best.ToolAt) {
			best, found = w, true
		}
	}
	return best, found
}

func bandRunning(st core.SwarmStatus) bool {
	return IsDelegateBand(st) && swarmAnyRunning(st.Workers)
}

func swarmRule(t Theme) string {
	return t.Paint(SlotDim, strings.Repeat(t.Glyph(GlyphDot), 4))
}

func swarmAnyRunning(workers []core.SwarmWorker) bool {
	for _, w := range workers {
		if w.State == "running" {
			return true
		}
	}
	return false
}

func swarmRoleRow(t Theme, st core.SwarmStatus, role string) (string, bool) {
	var ws []core.SwarmWorker
	for _, w := range st.Workers {
		if w.Role == role {
			ws = append(ws, w)
		}
	}
	if len(ws) == 0 {
		return "", false
	}
	label := "workers"
	queue := st.Pending
	glyph := GlyphPlus
	if role == "reviewer" {
		label = "reviewer"
		queue = st.Review
		glyph = GlyphReview
	}
	done, failed := 0, 0
	for _, w := range ws {
		done += w.Done
		failed += w.Failed
	}
	sep := t.Paint(SlotDim, " "+t.Glyph(GlyphDot)+" ")
	best := swarmBusiest(ws)
	return t.Paint(SlotDim, label) + t.Paint(SlotText, fmt.Sprintf(" %d", len(ws))) + sep +
		t.Paint(SlotDim, t.Glyph(glyph)) + t.Paint(SlotText, fmt.Sprintf("%d", queue)) + " " +
		t.Paint(SlotSuccess, t.Glyph(GlyphOK)) + t.Paint(SlotText, fmt.Sprintf("%d", done)) + " " +
		t.Paint(SlotError, t.Glyph(GlyphFail)) + t.Paint(SlotText, fmt.Sprintf("%d", failed)) + sep +
		t.Paint(SlotDim, swarmTail(best)), true
}

func swarmBusiest(ws []core.SwarmWorker) core.SwarmWorker {
	best := ws[0]
	bestInFlight, bestSettled, bestOldest := swarmRank(best)
	for _, w := range ws[1:] {
		inFlight, settled, oldest := swarmRank(w)
		if inFlight > bestInFlight || (inFlight == bestInFlight && (settled > bestSettled || (settled == bestSettled && oldest > bestOldest))) {
			best, bestInFlight, bestSettled, bestOldest = w, inFlight, settled, oldest
		}
	}
	return best
}

func swarmRank(w core.SwarmWorker) (inFlight, settled, oldest int) {
	inFlight = 0
	if w.Task != "" {
		inFlight = 1
	}
	return inFlight, w.Done + w.Failed, -w.ID
}

func swarmTail(w core.SwarmWorker) string {
	task := w.Task
	if task == "" {
		task = "—"
	}
	age := "—"
	if !w.Heartbeat.IsZero() {
		age = swarmAge(time.Since(w.Heartbeat))
	}
	return fmt.Sprintf("w%d %s %s", w.ID, task, age)
}

func swarmAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
