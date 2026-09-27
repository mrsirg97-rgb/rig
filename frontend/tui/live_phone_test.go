package tui

import (
	"strings"
	"testing"
)

// phoneResize models the terminal a phone shows: on shrink the top rows
// scroll into history so the cursor stays on screen; on grow those rows
// come back into view above, the cursor keeping its content row.
func phoneResize(v *vt, h int) {
	for len(v.rows) < v.height {
		v.rows = append(v.rows, "")
	}
	if h < v.height {
		drop := v.height - h
		if drop > v.r {
			drop = v.r
		}
		v.hist = append(v.hist, v.rows[:drop]...)
		v.rows = append([]string(nil), v.rows[drop:]...)
		v.r -= drop
	} else if h > v.height {
		back := h - v.height
		if back > len(v.hist) {
			back = len(v.hist)
		}
		restored := append([]string(nil), v.hist[len(v.hist)-back:]...)
		v.hist = v.hist[:len(v.hist)-back]
		v.rows = append(restored, v.rows...)
		v.r += back
	}
	v.height = h
	for len(v.rows) < v.height {
		v.rows = append(v.rows, "")
	}
	if v.bottom > v.height-1 {
		v.bottom = v.height - 1
	}
}

func regionRows(prefix string, n int) []string {
	rows := make([]string, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, prefix+string(rune('a'+i%26)))
	}
	return rows
}

func TestLiveRegionSurvivesPhoneKeyboardShrinkAndGrow(t *testing.T) {
	const width, tall, short = 40, 40, 20
	var out strings.Builder
	l := newLive(&out, width)
	l.setHeight(tall)
	v := newVTScreen(width, tall)

	// a transcript line, then a menu-sized region: 30 rows on a 40-row pane.
	tallRegion := regionRows("menu ", 28)
	tallRegion = append(tallRegion, "> /earn", "status")
	l.draw("committed line", tallRegion, "")
	v.feed([]byte(out.String()))
	out.Reset()

	// the keyboard opens: the pane shrinks under the region. the tui
	// repaints a trimmed region (the menu capped to fit).
	phoneResize(v, short)
	l.setHeight(short)
	shortRegion := append(regionRows("menu ", 8), "> /earn", "status")
	l.redraw(shortRegion)
	l.flush()
	v.feed([]byte(out.String()))
	out.Reset()

	// the keyboard closes: the pane grows and the rows that scrolled off
	// come back into view above. the tui repaints the tall region again.
	phoneResize(v, tall)
	l.setHeight(tall)
	l.redraw(tallRegion)
	l.flush()
	v.feed([]byte(out.String()))
	if v.err != "" {
		t.Fatalf("vt: %s", v.err)
	}

	screen := strings.Join(v.rows, "\n")
	// the tall region stands once, from the viewport's top; the rows the
	// short pane left standing are gone (the reset cleared the viewport).
	if n := strings.Count(screen, "menu a"); n != 2 {
		t.Fatalf("the region's repeated row stands %d times on screen, want 2 (its two real rows only):\n%s", n, screen)
	}
	if n := strings.Count(screen, "> /earn"); n != 1 {
		t.Fatalf("the input row stands %d times on screen, want 1:\n%s", n, screen)
	}
	if v.rows[0] != "menu a" {
		t.Fatalf("the reset must paint the region from the viewport's top row, got %q", v.rows[0])
	}
	// the transcript rows the grown pane showed above the region are the
	// reset's price: cleared from the viewport (a real terminal does not
	// return cleared rows to its scrollback either). the rows that never
	// came back into view are untouched in the scrollback.
	// a plain repaint after the reset aims exactly again: one region.
	out.Reset()
	l.redraw(tallRegion)
	l.flush()
	v.feed([]byte(out.String()))
	screen = strings.Join(v.rows, "\n")
	if n := strings.Count(screen, "> /earn"); n != 1 {
		t.Fatalf("after the reset a normal repaint must stay exact, got %d input rows:\n%s", n, screen)
	}
	// and a commit lands with nothing stale standing.
	out.Reset()
	l.enter("> /earn", "", "> ", "")
	v.feed([]byte(out.String()))
	screen = strings.Join(v.rows, "\n")
	if n := strings.Count(screen, "menu a"); n != 0 {
		t.Fatalf("after the commit %d menu rows still stand:\n%s", n, screen)
	}
}
