package tui

import (
	"strconv"
	"strings"
)

const (
	ESC = "\x1b["

	Reset = ESC + "0m"
)

func (t Theme) SGR(slot string) string {

	if slot == SlotBold {
		if base := t.SGR(SlotText); base != "" {
			return base + ESC + "1m"
		}
		return ESC + "1m"
	}
	hex := t.slots[slot]
	if hex == "" {
		return ""
	}
	if t.TrueColor {
		r, g, b, err := ParseHex(hex)
		if err != nil {
			return ""
		}
		return ESC + "38;2;" + strconv.Itoa(r) + ";" + strconv.Itoa(g) + ";" + strconv.Itoa(b) + "m"
	}
	return ESC + "38;5;" + strconv.Itoa(Nearest256(hex)) + "m"
}

func (t Theme) Paint(slot, s string) string {
	if s == "" {
		return s
	}
	seq := t.SGR(slot)
	if seq == "" {
		return s
	}
	return seq + s + Reset
}

func (t Theme) Invert(s string) string {
	if s == "" {
		return s
	}
	return ESC + "7m" + s + ESC + "27m"
}

func cursorUp(n int) string {
	if n <= 0 {
		return ""
	}
	return ESC + strconv.Itoa(n) + "A"
}

func cursorDown(n int) string {
	if n <= 0 {
		return ""
	}
	return ESC + strconv.Itoa(n) + "B"
}

func toCol(c int) string {
	if c <= 0 {
		c = 1
	}
	return ESC + strconv.Itoa(c) + "G"
}

const clearLine = ESC + "2K"

const clearBelow = ESC + "0J"

const (
	pasteOn  = ESC + "?2004h"
	pasteOff = ESC + "?2004l"
)

const (
	altOn      = ESC + "?1049h"
	altOff     = ESC + "?1049l"
	clearAll   = ESC + "2J"
	cursorHome = ESC + "H"
)

func truncateWidth(t Theme, s string, w int) string {
	rw := []rune(s)
	wid := 0
	for i, r := range rw {
		cw := runeWidth(r)
		if wid+cw > w {
			if w <= 1 {
				return string(rw[:i])
			}
			return string(rw[:i]) + t.Glyph(GlyphDot)
		}
		wid += cw
	}
	return s
}

func displayWidth(s string) int {
	return runeWidthSum(s)
}

func RemoveColor(s string) string {
	return sgrRe.ReplaceAllString(s, "")
}

var _ = strings.TrimSpace
