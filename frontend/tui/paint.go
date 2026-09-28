package tui

import (
	"strings"
)

const maxInputRows = 5

func (t *tui) inputLineLocked() string {
	line, _ := t.inputLineAndColLocked(maxInputRows)
	return line
}

func (t *tui) inputLineAndColLocked(maxRows int) (string, int) {
	t.syncSizeLocked()
	glyph := t.theme.Glyph(GlyphPrompt)
	prefixCols := displayWidth(glyph) + 1
	text := displayInput(t.inputText)
	runes := []rune(text)
	width := t.width
	if width < 2 {
		width = 2
	}
	cursorCol := prefixCols + runeWidthSum(string(runes[:t.editPos])) + 1

	lineCols := prefixCols + displayWidth(text)
	pad := ""
	if lineCols%width == 0 {
		pad = " "
	}
	totalCols := lineCols + len(pad)
	totalRows := (totalCols + width - 1) / width
	if totalRows <= maxRows {
		t.inputScroll = 0
		line := t.theme.Paint(SlotEmber, glyph) +
			t.theme.Paint(SlotText, " "+text+pad)

		if totalRows == 1 && t.editPos == len(runes) {
			if hint := t.hintLocked(); hint != "" {
				room := width - lineCols - 2
				if room > 1 {
					if displayWidth(hint) > room {
						hint = truncateWidth(t.theme, hint, room)
					}
					line += t.theme.Paint(SlotDim, hint)
				}
			}
		}
		return line, cursorCol
	}

	cursorRow := (cursorCol - 1) / width
	scroll := t.inputScroll
	if max := totalRows - maxRows; scroll > max {
		scroll = max
	}
	if scroll < 0 {
		scroll = 0
	}
	if cursorRow < scroll {
		scroll = cursorRow
	}
	if cursorRow > scroll+maxRows-1 {
		scroll = cursorRow - maxRows + 1
	}
	t.inputScroll = scroll
	logical := glyph + " " + text + pad
	win := sliceCols(logical, scroll*width, (scroll+maxRows)*width)
	var line string
	if scroll == 0 {
		line = t.theme.Paint(SlotEmber, glyph) +
			t.theme.Paint(SlotText, strings.TrimPrefix(win, glyph))
	} else {
		line = t.theme.Paint(SlotText, win)
	}
	return line, cursorCol - scroll*width
}

func displayInput(s string) string {
	if !strings.ContainsAny(s, "\n\t") {
		return s
	}
	r := []rune(s)
	for i, c := range r {
		switch c {
		case '\n':
			r[i] = '⏎'
		case '\t':
			r[i] = ' '
		}
	}
	return string(r)
}

func sliceCols(s string, start, end int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		w := runeWidth(r)
		if col+w <= start {
			col += w
			continue
		}
		if col >= end {
			break
		}
		if col < start {
			b.WriteString(" ")
			col += w
			continue
		}
		if col+w > end {
			break
		}
		b.WriteRune(r)
		col += w
	}
	return b.String()
}

func paintLines(t Theme, slot, text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = t.Paint(slot, l)
	}
	return strings.Join(lines, "\n")
}

type seg struct {
	slot string
	text string
}

func paintSegs(t Theme, segs []seg) string {
	var b strings.Builder
	for _, s := range segs {
		if s.slot == "" {
			b.WriteString(s.text)
			continue
		}
		b.WriteString(t.Paint(s.slot, s.text))
	}
	return b.String()
}

func (t *tui) expandTabsLocked(text string) string {
	if !strings.ContainsAny(text, "\t\n") && t.pendCol >= 0 {
		for _, r := range text {
			t.pendCol += runeWidth(r)
		}
		return text
	}
	var b strings.Builder
	for _, r := range text {
		switch r {
		case '\n':
			t.pendCol = 0
			b.WriteRune(r)
		case '\t':
			n := 8 - t.pendCol%8
			b.WriteString(strings.Repeat(" ", n))
			t.pendCol += n
		default:
			t.pendCol += runeWidth(r)
			b.WriteRune(r)
		}
	}
	return b.String()
}

const slotAfterTool = "\x00after-tool"

func (t *tui) flow(slot, text string) {
	t.mu.Lock()

	base := len(t.pend)
	boundary := (slot != "" && slot != SlotReasoning && t.lastSlot == SlotReasoning) ||
		(slot != "" && t.lastSlot == slotAfterTool)
	if boundary {
		sep := "\n"
		if len(t.pend) > 0 {
			sep = "\n\n"
		}
		t.pend = append(t.pend, seg{slot: slot, text: sep})
		t.pendCol = 0
	}
	if slot != "" {
		t.lastSlot = slot
	}
	t.pend = append(t.pend, seg{slot: slot, text: t.expandTabsLocked(text)})
	if lines := t.takeClosedLinesLocked(base); len(lines) > 0 {
		t.flowChunks = append(t.flowChunks, strings.Join(lines, "\n")+"\n")
	}
	t.dirty = true
	t.mu.Unlock()
}

func (t *tui) takeClosedLinesLocked(base int) []string {
	fresh := t.pend[base:]
	newline := false
	for _, s := range fresh {
		if strings.Contains(s.text, "\n") {
			newline = true
			break
		}
	}
	if !newline {
		return nil
	}
	var lines []string
	cur := append([]seg(nil), t.pend[:base]...)
	for _, s := range fresh {
		parts := strings.Split(s.text, "\n")
		for i, p := range parts {
			if i < len(parts)-1 {
				if p != "" {
					cur = append(cur, seg{slot: s.slot, text: p})
				}
				lines = append(lines, t.commitLineLocked(cur)...)
				cur = nil
			} else if p != "" {
				cur = append(cur, seg{slot: s.slot, text: p})
			}
		}
	}
	t.pend = cur
	t.pendGen++
	return lines
}

func (t *tui) commitLineLocked(cur []seg) []string {
	if t.codeMode {

		plain := paintFreeSegs(cur)
		if strings.HasPrefix(strings.TrimSpace(plain), "```") {
			t.codeMode = false
			return nil
		}
		return []string{t.theme.Paint(SlotDim, "  "+plain)}
	}
	if t.markdown && isTextLine(cur) {
		out, fence, info := mdLine(t.theme, cur)
		if fence {
			t.codeMode = true
			if info != "" {
				return []string{t.theme.Paint(SlotDim, "  "+info)}
			}
			return nil
		}
		cur = out
	}
	return wrapSegs(t.theme, t.width, cur)
}

func isReasoningLine(segs []seg) bool {
	for _, s := range segs {
		if s.slot != SlotReasoning && s.slot != "" {
			return false
		}
	}
	return true
}

func isTextLine(segs []seg) bool {
	for _, s := range segs {
		if s.slot != SlotText && s.slot != "" {
			return false
		}
	}
	return true
}

func paintFreeSegs(segs []seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
	}
	return b.String()
}
