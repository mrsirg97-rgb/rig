package tui

import (
	"fmt"
	"strings"
)

// BannerIn is the banner's numbers (decision 3). The root computes them
// (the row, the session, the store's usage rows); the TUI renders them
// and nothing else.
type BannerIn struct {
	Model     string
	Effort    string
	Used      int // context used: the last ContextTokens anchor plus the estimate after it
	Window    int
	Up        int // session prompt total
	Down      int // session completion total
	CacheRead int // session cache-read total
}

// RenderBanner is the two-row banner enclosed in dotted rules
// (decisions 3, 8): row 1 the model identity and context, row 2 the
// session totals. The context part is colored at the 70/90 marks (3);
// the rows are lowercase and plain; the rule spans the full width.
func RenderBanner(t Theme, b BannerIn, width int) string {
	rule := t.Paint(SlotRule, strings.Repeat(t.Glyph(GlyphDot), width))
	pct := 0
	if b.Window > 0 {
		pct = b.Used * 100 / b.Window
	}
	ctxSlot := SlotDim
	switch {
	case pct >= 90:
		ctxSlot = SlotError
	case pct >= 70:
		ctxSlot = SlotWarn
	}
	hit := 0
	if b.Up > 0 {
		hit = b.CacheRead * 100 / b.Up
	}
	row1 := t.Paint(SlotAccent, b.Model)
	if b.Effort != "" {
		row1 += t.Paint(SlotDim, " · ") + t.Paint(SlotDim, b.Effort)
	}
	row1 += t.Paint(SlotDim, " · ") + t.Paint(ctxSlot,
		fmt.Sprintf("%s/%s %d%%", formatTokens(b.Used), formatTokens(b.Window), pct))
	row2 := t.Paint(SlotDim, fmt.Sprintf("up %s down %s · cache r %s %d%%",
		formatTokens(b.Up), formatTokens(b.Down), formatTokens(b.CacheRead), hit))
	return rule + "\n" + row1 + "\n" + row2 + "\n" + rule + "\n"
}
