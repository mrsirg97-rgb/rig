package tui

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// vt is the escape-capture harness (SPEC_TUI's testing section): a
// minimal terminal model that speaks the TUI's exact escape vocabulary
// — text, LF, cursor-up (nA), cursor-down (nB, the norm's un-park),
// set-column (nG), clear-line (2K), and SGR (m, the paint and the
// inverse, no-ops here) — and asserts the cursor's bounds
// on every byte: the cursor never escapes the rows it has been given
// (no move past the top, no landing below the deepest row). The
// live region's logical lines are at most three (the activity row, the
// pending line, the input row, decision 1); a wide line wraps across
// more terminal rows, and the protocol's arithmetic counts those — the
// committed-rows-never-rewritten half of decision 1 is asserted on the
// replayed rows themselves (exact-row checks), not by bounding the
// cursor. Any escape outside the vocabulary fails the replay: the
// named set is the program's whole escape surface.
//
// The wrap model is the deferred one (xterm's, the common case): a
// character written at the last column stays on the row until the next
// write or cursor move; the protocol's lineEnd (toCol then LF) is built
// for it and is verified against lines written to exactly the width.
type vt struct {
	width  int
	rows   []string
	r, c   int
	bottom int
	err    string
}

func newVT(width int) *vt { return &vt{width: width, rows: []string{}} }

func (v *vt) fail(why string) {
	if v.err == "" {
		v.err = why
	}
}

func (v *vt) ensureRow(r int) {
	for len(v.rows) <= r {
		v.rows = append(v.rows, "")
	}
}

func (v *vt) writeRune(r rune) {
	if v.c > 0 && v.c == v.width {
		// the deferred wrap: the previous character was written at the
		// last column; this one lands on the next row.
		v.r++
		v.c = 0
		if v.r > v.bottom {
			v.bottom = v.r
		}
	}
	v.ensureRow(v.r)
	rs := []rune(v.rows[v.r])
	if v.c > len(rs) {
		v.c = len(rs)
	}
	v.rows[v.r] = string(append(rs[:v.c], append([]rune{r}, rs[v.c:]...)...))
	v.c++
}

func (v *vt) feed(b []byte) {
	i := 0
	for i < len(b) {
		c := b[i]
		if v.err != "" {
			return
		}
		switch {
		case c == '\n':
			v.r++
			v.ensureRow(v.r)
			if v.c > v.width {
				v.c = v.width
			}
			if v.r > v.bottom {
				v.bottom = v.r
			}
			i++
		case c == 0x1b:
			if i+1 >= len(b) || b[i+1] != '[' {
				v.fail("a bare escape: the vocabulary is CSI only")
				return
			}
			j := i + 2
			for j < len(b) && !(b[j] >= 0x40 && b[j] <= 0x7e) {
				j++
			}
			if j >= len(b) {
				v.fail("an unterminated CSI")
				return
			}
			params, term := string(b[i+2:j]), b[j]
			switch term {
			case 'A':
				n, aerr := atoi(params)
				if aerr != nil || n <= 0 {
					v.fail("cursor-up with n = " + params)
					return
				}
				if v.r < n {
					v.fail("cursor-up past the top of the screen")
					return
				}
				v.r -= n
			case 'B':
				// the cursor down: the norm's un-park.
				n, aerr := atoi(params)
				if aerr != nil || n <= 0 {
					v.fail("cursor-down with n = " + params)
					return
				}
				v.r += n
				v.ensureRow(v.r)
				if v.r > v.bottom {
					v.bottom = v.r
				}
			case 'G':
				n, aerr := atoi(params)
				if aerr != nil || n < 1 {
					v.fail("set-column with n = " + params)
					return
				}
				if n-1 > v.width {
					v.fail("set-column past the width")
					return
				}
				v.c = n - 1
			case 'K':
				if params != "2" {
					v.fail("an unknown clear mode: 2K only")
					return
				}
				v.ensureRow(v.r)
				v.rows[v.r] = ""
			case 'J':
				// erase from the cursor to the end of the screen (0J):
				// the current row from the column, every row below gone.
				if params != "0" && params != "" {
					v.fail("an unknown erase mode: 0J only")
					return
				}
				v.ensureRow(v.r)
				rr := []rune(v.rows[v.r])
				if v.c < len(rr) {
					v.rows[v.r] = string(rr[:v.c])
				}
				v.rows = v.rows[:v.r+1]
			case 'm':
				// the SGR paint: a no-op here, the content model is paint-free.
			default:
				v.fail("an escape outside the vocabulary: " + string(term))
				return
			}
			i = j + 1
		default:
			r, size := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && size == 1 {
				v.fail("an invalid or orphaned UTF-8 byte")
				return
			}
			v.writeRune(r)
			i += size
		}
	}
}

func paintFree(s string) string { return sgrRe.ReplaceAllString(s, "") }

// TestLiveWalkPaintedLines is the walk's chunk contract made a test:
// a committed chunk whose lines are each whole painted fragments (the
// TUI's paintLines rule) flows with no phantom row, including a
// deliberate blank line in the middle of the committed text.
func TestLiveWalkPaintedLines(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	chunk := strings.Join([]string{
		th.Paint(SlotText, "line one"),
		th.Paint(SlotText, ""),
		th.Paint(SlotText, "line three"),
	}, "\n")
	var out strings.Builder
	l := newLive(&out, 40)
	l.draw(chunk, []string{"tail"}, "")
	v := newVT(40)
	v.feed([]byte(out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s", v.err)
	}
	want := []string{"line one", "", "line three", "tail"}
	if len(v.rows) != len(want) {
		t.Fatalf("the painted chunk left a phantom row: %d rows, want %d:\n%q", len(v.rows), len(want), v.rows)
	}
	for i := range want {
		if v.rows[i] != want[i] {
			t.Fatalf("row %d = %q, want %q\nall: %q", i, v.rows[i], want[i], v.rows)
		}
	}
}

// TestLiveRegionProtocol is the named live-region case: the redraw is
// cursor-up/clear/reprint over at most three lines, and the committed
// bytes are never rewritten (decision 1's immutability), asserted over
// the whole session script — start, typing, Enter, a reasoning and text
// stream, a tool round trip, a steering Enter, the interrupted turn's
// end, and the next turn's start.
func TestLiveRegionProtocol(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	prompt := th.Paint(SlotAccent, th.Glyph(GlyphPrompt))
	inEmpty := prompt + th.Paint(SlotText, " ")
	activity := th.Paint(SlotDim, "| thinking")
	inSteer := prompt + th.Paint(SlotText, " fix the retry")

	block := RenderStatus(th, StatusIn{
		Model: "huihui3.8", Effort: "xhigh", Window: 262144,
		Up: 214000, Down: 18200, CacheRead: 187000,
	})
	blockLines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	usage := RenderUsage(th, 3200, 136, 918)
	toolBlock := strings.Join([]string{
		"bash · $ go test ./x",
		"  ok  \tx\t0.4s",
		"bash ✓ 0.4s",
	}, "\n")

	var out strings.Builder
	l := newLive(&out, 50)

	// session start: the startup block plus the prompt (the first draw).
	l.draw(block, []string{inEmpty}, "")
	// typing: the input row rewritten in place, the cursor parked.
	l.edit(prompt+th.Paint(SlotText, " hello"), 7, "")
	// Enter at the quiet prompt: the line freezes, the activity row and
	// the fresh input land below.
	l.enter("hello", activity, inEmpty, "")
	// the turn's stream: reasoning, a partial text delta, a complete
	// one, the tool round trip.
	l.draw(th.Paint(SlotReasoning, "thinking..."), []string{activity, inEmpty}, "")
	l.draw(th.Paint(SlotText, "hel"), []string{activity, inEmpty}, "")
	l.draw(paintLines(th, SlotText, "lo\n"), []string{activity, inEmpty}, "")
	l.draw(toolBlock, []string{activity, inEmpty}, "")
	// a steering Enter mid-turn: the input row freezes, the activity
	// row above is left standing.
	l.edit(inSteer, 15, "")
	l.enter(inSteer, "", inEmpty, "")
	// the interrupted turn's end: the usage line commits, the live
	// region resets to the input row alone.
	l.draw(usage, []string{inEmpty}, "")
	// the next turn starts on the delivered steer line: the activity
	// row is put above the input row.
	l.insertActivity(activity)
	// a tick and a phase switch (the in-place rewrites).
	l.setActivity(th.Paint(SlotDim, "/") + th.Paint(SlotAccent, " bash"))
	l.setActivity(activity)

	v := newVT(50)
	v.feed([]byte(out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%q", v.err, out.String())
	}

	want := []string{
		paintFree(blockLines[0]), // the greeting (the fixture names no session)
		"hello",
		"", // the prompt's margin (decision 2, amended): a blank under the frozen line
		"thinking...",
		"hel",
		"lo",
		"bash · $ go test ./x",
		"  ok  \tx\t0.4s",
		"bash ✓ 0.4s",
		"| thinking", // the interrupted turn's activity row, left standing (frozen at its last frame)
		"❯ fix the retry",
		"", // the steer's margin
		paintFree(usage),
		"| thinking",
		"❯ ",
	}
	if len(v.rows) != len(want) {
		t.Fatalf("the screen has %d rows, want %d:\n%q", len(v.rows), len(want), v.rows)
	}
	for i := range want {
		if v.rows[i] != want[i] {
			t.Fatalf("row %d = %q, want %q\nall: %q", i, v.rows[i], want[i], v.rows)
		}
	}
}

// TestLiveRegionWidthExact is the wrap-safety edge: a committed line
// written to exactly the terminal width, and the lineEnd sequence
// (toCol then LF) must not leave a phantom blank line under the
// deferred-wrap model. Two exact-width rules in a row, then the input
// row: exactly three rows, no phantom between.
func TestLiveRegionWidthExact(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	rule := th.Paint(SlotRule, strings.Repeat(th.Glyph(GlyphDot), 12))
	var out strings.Builder
	l := newLive(&out, 12)
	l.draw(rule+"\n"+rule, []string{"x"}, "")
	v := newVT(12)
	v.feed([]byte(out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s", v.err)
	}
	want := []string{
		strings.Repeat(th.Glyph(GlyphDot), 12),
		strings.Repeat(th.Glyph(GlyphDot), 12),
		"x",
	}
	if len(v.rows) != len(want) {
		t.Fatalf("a phantom row: %d rows, want %d:\n%q", len(v.rows), len(want), v.rows)
	}
	for i := range want {
		if v.rows[i] != want[i] {
			t.Fatalf("row %d = %q, want %q\nall: %q", i, v.rows[i], want[i], v.rows)
		}
	}
}

// TestLiveRegionImmutability is the invariant made direct: every row
// the cursor has left behind keeps exactly the bytes it was given — the
// committed bytes are never rewritten (decision 1), asserted over an
// adversarial script that commits fifty lines while redrawing the live
// region between every one.
func TestLiveRegionImmutability(t *testing.T) {
	th, err := ResolveTheme("p1", json.RawMessage(`{"base":"p1","glyphs":"ascii"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	prompt := th.Paint(SlotAccent, th.Glyph(GlyphPrompt))
	in := prompt + th.Paint(SlotText, " ")

	var out strings.Builder
	l := newLive(&out, 30)
	l.draw("first committed line", []string{in}, "")
	for i := 0; i < 50; i++ {
		l.edit(prompt+th.Paint(SlotText, " draft "+strconv.Itoa(i)), 4, "")
		l.draw("committed "+strconv.Itoa(i), []string{in}, "")
	}
	v := newVT(30)
	v.feed([]byte(out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s", v.err)
	}
	if v.rows[0] != "first committed line" {
		t.Fatalf("the first committed row was rewritten: %q", v.rows[0])
	}
	for i := 0; i < 50; i++ {
		if want := "committed " + strconv.Itoa(i); v.rows[i+1] != want {
			t.Fatalf("committed row %d was rewritten: %q, want %q", i, v.rows[i+1], want)
		}
	}
	if want := paintFree(in); v.rows[51] != want {
		t.Fatalf("the final input row = %q, want %q", v.rows[51], want)
	}
}

// TestLiveRegionStatusRow is the amended layout (decision 3, the live
// region's last row): the status row stands below the input row, the
// whole script re-lays and re-parks over it, and the in-place rewrites
// (the tick, the edit) leave it standing.
func TestLiveRegionStatusRow(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	prompt := th.Paint(SlotAccent, th.Glyph(GlyphPrompt))
	inEmpty := prompt + th.Paint(SlotText, " ")
	activity := th.Paint(SlotDim, "| thinking")
	status := th.Paint(SlotDim, "huihui3.8")

	var out strings.Builder
	l := newLive(&out, 50)
	l.draw("", []string{inEmpty}, status)
	l.edit(prompt+th.Paint(SlotText, " hi"), 4, status)
	l.enter("hi", activity, inEmpty, status)
	l.draw(th.Paint(SlotText, "hel"), []string{activity, inEmpty}, status)
	l.setActivity(th.Paint(SlotDim, "/") + th.Paint(SlotAccent, " bash"))
	l.setActivity(activity)
	// the turn's end: the usage commits, the region resets, the status
	// row stands.
	l.draw(th.Paint(SlotDim, "up 1.2k down 220 · cache r 0 0%"), []string{inEmpty}, status)

	v := newVT(50)
	v.feed([]byte(out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%q", v.err, out.String())
	}
	want := []string{
		"hi",
		"", // the prompt's margin
		"hel",
		"up 1.2k down 220 · cache r 0 0%", // the turn end: the region resets, the activity row (a live row) goes with it
		"❯ ",
		paintFree(status),
	}
	if len(v.rows) != len(want) {
		t.Fatalf("the screen has %d rows, want %d:\n%q", len(v.rows), len(want), v.rows)
	}
	for i := range want {
		if v.rows[i] != want[i] {
			t.Fatalf("row %d = %q, want %q\nall: %q", i, v.rows[i], want[i], v.rows)
		}
	}
}

// TestLiveRegionMenuRows is the amended layout's menu (decision 9):
// the rows stand above the input row, the selection row is inverted,
// and the editFull op re-lays the region when the shape changes.
func TestLiveRegionMenuRows(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	prompt := th.Paint(SlotAccent, th.Glyph(GlyphPrompt))
	inEmpty := prompt + th.Paint(SlotText, " ")
	inCmd := prompt + th.Paint(SlotText, " /s")
	menuA := th.Paint(SlotAccent, "scheduler") + th.Paint(SlotText, "  the scheduler's jobs")
	menuB := th.Paint(SlotAccent, "sessions") + th.Paint(SlotText, "  the session's store")
	menuC := th.Paint(SlotAccent, "steer") + th.Paint(SlotText, "  a note into the queue")
	status := th.Paint(SlotDim, "huihui3.8")

	var out strings.Builder
	l := newLive(&out, 50)
	l.draw("", []string{inEmpty}, status)
	l.edit(inCmd, 3, status)
	// the menu opens: the shape changes, the whole region re-lays.
	l.editFull([]string{th.Invert(menuA), menuB, menuC, inCmd}, 3, status)
	// a keystroke with the shape standing: the input row in place.
	l.edit(prompt+th.Paint(SlotText, " /sc"), 4, status)
	// the menu closes: the shape changes again.
	l.editFull([]string{inEmpty}, 3, status)

	v := newVT(50)
	v.feed([]byte(out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%q", v.err, out.String())
	}
	want := []string{
		"❯ ",
		"huihui3.8", // the status row stands; the menu's freed rows are erased below it
	}
	if len(v.rows) != len(want) {
		t.Fatalf("the screen has %d rows, want %d:\n%q", len(v.rows), len(want), v.rows)
	}
	for i := range want {
		if v.rows[i] != want[i] {
			t.Fatalf("row %d = %q, want %q\nall: %q", i, v.rows[i], want[i], v.rows)
		}
	}
	// the menu frame laid the rows menu, input, status — in order in
	// the stream (the screen at that moment).
	frame := out.String()
	if iMenu := strings.Index(frame, th.Invert(menuA)); iMenu < 0 {
		t.Fatal("the inverted selection row is not in the stream")
	} else {
		rest := frame[iMenu:]
		iB := strings.Index(rest, menuB)
		iC := strings.Index(rest, menuC)
		iIn := strings.Index(rest, inCmd)
		iSt := strings.Index(rest, status)
		if !(iB > 0 && iC > iB && iIn > iC && iSt > iIn) {
			t.Fatalf("the menu frame's row order broke: b=%d c=%d in=%d st=%d", iB, iC, iIn, iSt)
		}
	}
}
