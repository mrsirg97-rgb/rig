package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

func RenderSwarmNotice(t Theme, text string) string {
	return t.Paint(SlotDim, text)
}

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
	bi, bd, bn := swarmRank(best)
	for _, w := range ws[1:] {
		i, d, n := swarmRank(w)
		if i > bi || (i == bi && (d > bd || (d == bd && n > bn))) {
			best, bi, bd, bn = w, i, d, n
		}
	}
	return best
}

func swarmRank(w core.SwarmWorker) (int, int, int) {
	inFlight := 0
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
