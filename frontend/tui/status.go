package tui

import (
	"fmt"
	"strings"
)

// StatusIn is the status line's and the startup block's numbers
// (decision 3, amended): the root computes them at the refresh points
// (the session start, new, sessions resume, a models switch) — a
// store read at those points, never per repaint.
type StatusIn struct {
	Model     string
	Effort    string
	Window    int
	Role      string // the session's stance ("" = default, SPEC_MODES 2)
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
	b := renderTitle(t)
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

// renderTitle is the greeting (decision 3, amended): "welcome to" in
// the dim, and under it the name in block letters, the ember — the one
// piece of retro texture the block keeps (decision 8: texture, never
// information; the letters spell the same word the plain row does).
// The ascii glyph set (a terminal without unicode) gets the plain row.
func renderTitle(t Theme) string {
	b := t.Paint(SlotDim, "welcome to") + "\n"
	if t.Glyph(GlyphPrompt) == ">" {
		return b + t.Paint(SlotEmber, "rig") + "\n"
	}
	for _, row := range titleRows {
		b += t.Paint(SlotEmber, row) + "\n"
	}
	return b
}

// titleRows is "rig" in three rows of block glyphs, 15 columns: the
// retro banner, sized to fit a phone's width with room to spare.
var titleRows = []string{
	"█▀▄ █ █▀▀",
	"█▀▄ █ █ █",
	"▀ ▀ ▀ ▀▀▀",
}

// RenderStatusLine is the live status (decision 3, amended, SPEC_MODES
// 2 and 3): two rows under the input. The first is the model info row —
// model · effort · used/window · role — the model in the text color,
// the active effort in its ramp color (pane's footer, SlotEffort*;
// accent for a level outside the ramp; skipped when the row names
// none), the context part colored at the 70/90 marks (dim under 70,
// warn at 70, error at 90) once a turn has run and skipped before,
// and the stance last, abbreviated (architect -> arch, reviewer ->
// rev, default shown as default), the dim. The second is the session's
// usage totals: up, down, and the cache-read hit rate. The rows are
// joined by a newline; the live region splits them (statusRows).
func RenderStatusLine(t Theme, model, effort, role string, used, window int, hasUsed bool, up, down, cacheRead int) string {
	if model == "" {
		return ""
	}
	sep := t.Paint(SlotDim, " · ")
	row1 := t.Paint(SlotText, model)
	if effort != "" {
		row1 += sep + t.Paint(effortSlot(t, effort), effort)
	}
	if hasUsed && window > 0 {
		pct := used * 100 / window
		slot := SlotDim
		switch {
		case pct >= 90:
			slot = SlotError
		case pct >= 70:
			slot = SlotWarn
		}
		row1 += sep + t.Paint(slot, formatTokens(used)+"/"+formatTokens(window))
	}
	row1 += sep + t.Paint(SlotDim, abbrevRole(role))
	hit := 0
	if up > 0 {
		hit = cacheRead * 100 / up
	}
	row2 := t.Paint(SlotDim, fmt.Sprintf("up %s down %s · cache r %s %d%%",
		formatTokens(up), formatTokens(down), formatTokens(cacheRead), hit))
	return row1 + "\n" + row2
}

// effortSlot maps a level name onto the effort ramp's slot: "low" ->
// effortLow, matched case-insensitively against pane's seven; a level
// outside the ramp paints accent (pane's fallback rule).
func effortSlot(t Theme, level string) string {
	if len(level) == 0 {
		return SlotAccent
	}
	key := "effort" + strings.ToUpper(level[:1]) + strings.ToLower(level[1:])
	if t.Slot(key) != "" {
		return key
	}
	return SlotAccent
}

// abbrevRole is the stance's short form for the info row (SPEC_MODES
// 3, amended): architect -> arch, reviewer -> rev, the default (and
// the empty root state) -> default; a name outside the shipped three
// shows as-is.
func abbrevRole(role string) string {
	switch role {
	case "architect":
		return "arch"
	case "reviewer":
		return "rev"
	case "", "default":
		return "default"
	}
	return role
}
