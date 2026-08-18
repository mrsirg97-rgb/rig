package tui

import (
	"strconv"
	"strings"
)

// The escape helpers (the layout's ansi.go): the color, the cursor, and
// the clear-line. This is the whole escape vocabulary of the program:
// the live region redraw is cursor-up, clear-line, reprint (decision 2)
// plus the column move of the single-line editor.

const (
	ESC = "\x1b["
	// Reset ends every painted fragment; the terminal's default color is
	// the terminal's, never ours.
	Reset = ESC + "0m"
)

// SGR is the slot's color sequence in the theme's mode: truecolor
// (38;2;r;g;b) when the terminal reports it, else the nearest 256 index
// (38;5;n) — named, automatic, not configurable (decision 7).
func (t Theme) SGR(slot string) string {
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

// Paint wraps s in the slot's color and a reset. Empty s or an unknown
// slot is the bare text: the renderer composes fragments, and an empty
// fragment must not leave a dangling escape.
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

// Invert is reverse video (SGR 7): the menu's selected row (decision 9,
// amended). A mode, not a slot: the downconvert and the palette do not
// touch it, and the width walk's SGR filter already skips it.
func (t Theme) Invert(s string) string {
	if s == "" {
		return s
	}
	return ESC + "7m" + s + ESC + "27m"
}

// Cursor up n lines (the live region's only upward move; n is at most
// the live region's height, decision 2's "at most three lines").
func cursorUp(n int) string {
	if n <= 0 {
		return ""
	}
	return ESC + strconv.Itoa(n) + "A"
}

// Cursor down n lines (the edit op's un-park: the cursor returns to
// the region's last row before the next op's arithmetic; CSI B never
// scrolls, and the target rows are the region's own).
func cursorDown(n int) string {
	if n <= 0 {
		return ""
	}
	return ESC + strconv.Itoa(n) + "B"
}

// To column 1 (0-based ANSI column 0 is column 1).
func toCol(c int) string {
	if c <= 0 {
		c = 1
	}
	return ESC + strconv.Itoa(c) + "G"
}

// ClearLine wipes the whole line under or before the cursor.
const clearLine = ESC + "2K"

// clearBelow erases from the cursor to the end of the screen (CSI 0J):
// a shrinking live region's freed rows go with it, so no cleared row
// stands under the region.
const clearBelow = ESC + "0J"

// Bracketed paste mode (decision 9): on at raw mode, off at Close.
const (
	pasteOn  = ESC + "?2004h"
	pasteOff = ESC + "?2004l"
)

// The alternate screen (the pager's, decision 2's one exception: the
// main UI never enters it), and its canvas ops.
const (
	altOn      = ESC + "?1049h"
	altOff     = ESC + "?1049l"
	clearAll   = ESC + "2J"
	cursorHome = ESC + "H"
)

// truncateWidth cuts s to at most w display columns (runewidth), adding
// the ellipsis glyph when it overflows. The live region is measured
// (decision 10); committed prose is never (decision 8).
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

// displayWidth is the runewidth of s (go-runewidth, decision 10's named
// dependency: glyph and CJK width for the live region and previews).
func displayWidth(s string) int {
	return runeWidthSum(s)
}

// RemoveColor strips every SGR sequence — the tests and the width math
// read the bare text; the stream itself is never re-shrunk.
func RemoveColor(s string) string {
	return sgrRe.ReplaceAllString(s, "")
}

var _ = strings.TrimSpace
