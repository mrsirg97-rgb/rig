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
// greeting in the accent, the session row under it, then the
// banner's identity row without its context part, and its usage row
// — dim, no dotted rules (they enclosed the banner, and the banner is
// gone). Committed once at the session start and at the refresh
// points (new, sessions resume).
func RenderStatus(t Theme, in StatusIn) string {
	var b string
	b += t.Paint(SlotAccent, "welcome to rig") + "\n"
	if in.Session != "" {
		// the id's short form (a hash's first twelve, git's habit): the
		// row is a glance, /sessions is the full list.
		id := in.Session
		if len(id) > 12 {
			id = id[:12]
		}
		b += t.Paint(SlotDim, "session "+id) + "\n"
	}
	row := t.Paint(SlotDim, in.Model)
	if in.Effort != "" {
		row += t.Paint(SlotDim, " · ") + t.Paint(SlotDim, in.Effort)
	}
	b += row + "\n"
	hit := 0
	if in.Up > 0 {
		hit = in.CacheRead * 100 / in.Up
	}
	b += t.Paint(SlotDim, fmt.Sprintf("up %s down %s · cache r %s %d%%",
		formatTokens(in.Up), formatTokens(in.Down), formatTokens(in.CacheRead), hit)) + "\n"
	return b
}

// RenderStatusLine is the live status row (decision 3): the model,
// and used over the window once a turn has run — the context part
// colored at the 70/90 marks (dim under 70, warn at 70, error at 90),
// the model dim. Before the first usage: the model alone.
func RenderStatusLine(t Theme, model string, used, window int, hasUsed bool) string {
	if model == "" {
		return ""
	}
	if !hasUsed || window <= 0 {
		return t.Paint(SlotDim, model)
	}
	pct := used * 100 / window
	slot := SlotDim
	switch {
	case pct >= 90:
		slot = SlotError
	case pct >= 70:
		slot = SlotWarn
	}
	return t.Paint(SlotDim, model+" · ") +
		t.Paint(slot, formatTokens(used)+"/"+formatTokens(window))
}
