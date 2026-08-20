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
// 2): two rows under the input. The first is the model, with the
// non-default role's stance between it and the context — model · role ·
// used/window — the operator glances the stance the model is in — and
// used over the window once a turn has run, the context part colored at
// the 70/90 marks (dim under 70, warn at 70, error at 90), the model
// dim; before the first usage, the model alone. The second is the
// session's usage totals: up, down, and the cache-read hit rate. The
// rows are joined by a newline; the live region splits them (statusRows).
func RenderStatusLine(t Theme, model, role string, used, window int, hasUsed bool, up, down, cacheRead int) string {
	if model == "" {
		return ""
	}
	head := t.Paint(SlotText, model)
	var row1 string
	if !hasUsed || window <= 0 {
		if role != "" && role != "default" {
			head += t.Paint(SlotDim, " · "+role)
		}
		row1 = head
	} else {
		pct := used * 100 / window
		slot := SlotDim
		switch {
		case pct >= 90:
			slot = SlotError
		case pct >= 70:
			slot = SlotWarn
		}
		if role != "" && role != "default" {
			head += t.Paint(SlotDim, " · "+role+" · ")
		} else {
			head += t.Paint(SlotDim, " · ")
		}
		row1 = head + t.Paint(slot, formatTokens(used)+"/"+formatTokens(window))
	}
	hit := 0
	if up > 0 {
		hit = cacheRead * 100 / up
	}
	row2 := t.Paint(SlotDim, fmt.Sprintf("up %s down %s · cache r %s %d%%",
		formatTokens(up), formatTokens(down), formatTokens(cacheRead), hit))
	return row1 + "\n" + row2
}
