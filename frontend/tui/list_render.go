package tui

import (
	"regexp"
	"strings"
)

var listRowRe = regexp.MustCompile(`^  (\S+)( +)(\[([ xr!~])\] )?(.*)$`)

func RenderListBlock(t Theme, opening, reply string) (string, bool) {
	lines := strings.Split(strings.TrimRight(reply, "\n"), "\n")
	if len(lines) < 2 {
		return "", false
	}
	rows := 0
	for _, line := range lines[1:] {
		if listRowRe.MatchString(line) {
			rows++
		}
	}
	if rows == 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString(opening)
	b.WriteString("\n")
	b.WriteString(t.Paint(SlotDim, lines[0]))
	for _, line := range lines[1:] {
		b.WriteString("\n")
		switch {
		case strings.HasPrefix(line, "· "):
			b.WriteString(t.Paint(SlotDim, "  "+line))
		case strings.HasPrefix(line, "    "):
			b.WriteString(t.Paint(SlotDim, line))
		default:
			m := listRowRe.FindStringSubmatch(line)
			if m == nil {
				b.WriteString(t.Paint(SlotText, line))
				continue
			}
			b.WriteString(t.renderListRow(m[1], m[2], m[4], m[5]))
		}
	}
	return b.String(), true
}

func (t Theme) renderListRow(id, pad, mark, rest string) string {
	var b strings.Builder
	if mark != "" {
		glyph, slot := t.listMarkGlyph(mark)
		b.WriteString(t.Paint(slot, glyph))
		b.WriteString(" ")
	} else {
		b.WriteString("  ")
	}
	b.WriteString(t.Paint(SlotDim, id))
	b.WriteString(pad)
	segs := strings.Split(rest, " · ")
	b.WriteString(t.Paint(SlotText, segs[0]))
	for _, seg := range segs[1:] {
		b.WriteString(t.Paint(SlotDim, " · "+seg))
	}
	return b.String()
}

func (t Theme) listMarkGlyph(mark string) (string, string) {
	switch mark {
	case "x":
		return t.Glyph(GlyphDone), SlotSuccess
	case "~":
		return t.Glyph(GlyphActive), SlotAccent
	case "r":
		return t.Glyph(GlyphReview), SlotWarn
	case "!":
		return t.Glyph(GlyphFail), SlotError
	default:
		return t.Glyph(GlyphPending), SlotDim
	}
}
