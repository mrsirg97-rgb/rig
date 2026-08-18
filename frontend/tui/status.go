package tui

import (
	"fmt"
)

// StatusIn is the status line's and the startup block's numbers
// (decision 3, amended): the root computes them at the refresh points
// (the session start, new, sessions resume, a models switch) — a
// store read at those points, never per repaint.
type StatusIn struct {
	Model     string
	Effort    string
	Window    int
	Session   string // the session id (the startup block's second row)
	Up        int    // session prompt total
	Down      int    // session completion total
	CacheRead int    // session cache-read total
}

// RenderStatus is the startup block (decision 3, amended): the
// greeting in the ember and the session row under it — committed once
// at the session start and at the refresh points (new, sessions
// resume). The model and usage rows are the live status now
// (RenderStatusLine), under the input.
func RenderStatus(t Theme, in StatusIn) string {
	b := t.Paint(SlotEmber, "welcome to rig") + "\n"
	if in.Session != "" {
		// the id's short form (a hash's first twelve, git's habit): the
		// row is a glance, /sessions is the full list.
		id := in.Session
		if len(id) > 12 {
			id = id[:12]
		}
		b += t.Paint(SlotDim, "session "+id) + "\n"
	}
	return b
}

// RenderStatusLine is the live status (decision 3, amended): two rows
// under the input. The first is the model, with used over the window
// once a turn has run — the context part colored at the 70/90 marks
// (dim under 70, warn at 70, error at 90), the model dim; before the
// first usage, the model alone. The second is the session's usage
// totals: up, down, and the cache-read hit rate. The rows are joined
// by a newline; the live region splits them (statusRows).
func RenderStatusLine(t Theme, model string, used, window int, hasUsed bool, up, down, cacheRead int) string {
	if model == "" {
		return ""
	}
	var row1 string
	if !hasUsed || window <= 0 {
		row1 = t.Paint(SlotDim, model)
	} else {
		pct := used * 100 / window
		slot := SlotDim
		switch {
		case pct >= 90:
			slot = SlotError
		case pct >= 70:
			slot = SlotWarn
		}
		row1 = t.Paint(SlotDim, model+" · ") +
			t.Paint(slot, formatTokens(used)+"/"+formatTokens(window))
	}
	hit := 0
	if up > 0 {
		hit = cacheRead * 100 / up
	}
	row2 := t.Paint(SlotDim, fmt.Sprintf("up %s down %s · cache r %s %d%%",
		formatTokens(up), formatTokens(down), formatTokens(cacheRead), hit))
	return row1 + "\n" + row2
}
