package tui

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

type vt struct {
	width   int
	height  int
	hist    []string
	rows    []string
	r, c    int
	bottom  int
	err     string
	clamped int
}

func newVT(width int) *vt { return &vt{width: width, rows: []string{}} }

func newVTScreen(width, height int) *vt {
	return &vt{width: width, height: height, rows: []string{}}
}

func (v *vt) scroll() {
	if v.height <= 0 {
		return
	}
	if len(v.rows) < v.height {
		v.ensureRow(len(v.rows))
		return
	}
	v.hist = append(v.hist, v.rows[0])
	rest := make([]string, 0, v.height)
	rest = append(rest, v.rows[1:]...)
	rest = append(rest, "")
	v.rows = rest
}

func (v *vt) advance() {
	if v.height > 0 && v.r >= v.height-1 {
		v.scroll()
		v.r = v.height - 1
		return
	}
	v.r++
	if v.height > 0 && v.r > v.bottom {
		v.bottom = v.r
	}
	v.ensureRow(v.r)
}

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

		v.c = 0
		v.advance()
	}
	v.ensureRow(v.r)
	rs := []rune(v.rows[v.r])
	if v.c < len(rs) {
		rs[v.c] = r
	} else {
		rs = append(rs, r)
	}
	v.rows[v.r] = string(rs)
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
			v.advance()
			if v.c > v.width {
				v.c = v.width
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
				if v.height > 0 {
					if v.r < n {
						v.clamped++
						v.r = 0
						break
					}
					v.r -= n
					break
				}
				if v.r < n {
					v.fail("cursor-up past the top of the screen")
					return
				}
				v.r -= n
			case 'B':

				n, aerr := atoi(params)
				if aerr != nil || n <= 0 {
					v.fail("cursor-down with n = " + params)
					return
				}
				if v.height > 0 {
					if v.r+n > v.height-1 {
						v.clamped++
						v.r = v.height - 1
						break
					}
					v.r += n
					break
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
				v.ensureRow(v.r)
				switch params {
				case "2":
					v.rows[v.r] = ""
				case "":
					rs := []rune(v.rows[v.r])
					if v.c < len(rs) {
						v.rows[v.r] = string(rs[:v.c])
					}
				default:
					v.fail("an unknown clear mode: " + params + "K")
					return
				}
			case 'J':

				if params != "0" && params != "" {
					v.fail("an unknown erase mode: 0J only")
					return
				}
				v.ensureRow(v.r)
				rr := []rune(v.rows[v.r])
				if v.c < len(rr) {
					v.rows[v.r] = string(rr[:v.c])
				}
				if v.height > 0 {
					for i := v.r + 1; i < v.height; i++ {
						v.ensureRow(i)
						v.rows[i] = ""
					}
					break
				}
				v.rows = v.rows[:v.r+1]
			case 'm':

			case 'h', 'l':
				if params != "?2026" {
					v.fail("a mode outside the vocabulary: " + params + string(term))
					return
				}

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

func TestLiveSyncFrames(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	l := newLive(&out, 40)
	l.draw(th.Paint(SlotText, "hello"), []string{"tail"}, "")

	stream := out.String()
	if !strings.HasPrefix(stream, syncOn) || !strings.HasSuffix(stream, syncOff) {
		t.Fatalf("the frame is not wrapped in the sync mode: %q", stream)
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(stream, syncOn), syncOff)
	if !strings.Contains(mid, "hello") {
		t.Fatalf("the wrapped frame lost its content: %q", mid)
	}

	l.edit(th.Paint(SlotText, " in"), 2, "")
	stream = out.String()
	if got := strings.Count(stream, syncOn); got != 2 {
		t.Fatalf("each flush must carry its own sync pair: %d on-markers, want 2\n%q", got, stream)
	}
	if got := strings.Count(stream, syncOff); got != 2 {
		t.Fatalf("sync pairs must balance: %d off-markers, want 2", got)
	}
	if strings.Contains(stream, syncOn+syncOff) {
		t.Fatal("an empty frame was flushed")
	}
}

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

	l.draw(block, []string{inEmpty}, "")

	l.edit(prompt+th.Paint(SlotText, " hello"), 7, "")

	l.enter("hello", activity, inEmpty, "")

	l.draw(th.Paint(SlotReasoning, "thinking..."), []string{activity, inEmpty}, "")
	l.draw(th.Paint(SlotText, "hel"), []string{activity, inEmpty}, "")
	l.draw(paintLines(th, SlotText, "lo\n"), []string{activity, inEmpty}, "")
	l.draw(toolBlock, []string{activity, inEmpty}, "")

	l.edit(inSteer, 15, "")
	l.enter(inSteer, "", inEmpty, "")

	l.draw(usage, []string{inEmpty}, "")

	l.insertActivity(activity)

	l.setActivity(th.Paint(SlotDim, "/") + th.Paint(SlotAccent, " bash"))
	l.setActivity(activity)

	v := newVT(50)
	v.feed([]byte(out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%q", v.err, out.String())
	}

	want := []string{
		paintFree(blockLines[0]), paintFree(blockLines[1]), paintFree(blockLines[2]), paintFree(blockLines[3]), paintFree(blockLines[4]), paintFree(blockLines[5]),
		"hello",
		"",
		"thinking...",
		"hel",
		"lo",
		"bash · $ go test ./x",
		"  ok  \tx\t0.4s",
		"bash ✓ 0.4s",
		"❯ fix the retry",
		"",
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

	l.draw(th.Paint(SlotDim, "up 1.2k down 220 · cache r 0 0%"), []string{inEmpty}, status)

	v := newVT(50)
	v.feed([]byte(out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%q", v.err, out.String())
	}
	want := []string{
		"hi",
		"",
		"hel",
		"up 1.2k down 220 · cache r 0 0%",
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

	l.editFull([]string{th.Invert(menuA), menuB, menuC, inCmd}, 3, status)

	l.edit(prompt+th.Paint(SlotText, " /sc"), 4, status)

	l.editFull([]string{inEmpty}, 3, status)

	v := newVT(50)
	v.feed([]byte(out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%q", v.err, out.String())
	}
	want := []string{
		"❯ ",
		"huihui3.8",
	}
	if len(v.rows) != len(want) {
		t.Fatalf("the screen has %d rows, want %d:\n%q", len(v.rows), len(want), v.rows)
	}
	for i := range want {
		if v.rows[i] != want[i] {
			t.Fatalf("row %d = %q, want %q\nall: %q", i, v.rows[i], want[i], v.rows)
		}
	}

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
