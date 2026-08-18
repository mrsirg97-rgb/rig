package tui

// The live-region tear's regressions (SPEC_TUI's testing section):
// the terminal wraps a line that reaches the last column, and the wrap
// may resolve at a flush point (a write boundary) rather than at the
// next byte — a frame that leaves the cursor at the last column across
// a write boundary shifts the cursor a row, and the next op's cursor
// tally is off by a row (the clear misses the top row, the indicator
// lands between committed text). The three tests below pin the
// invariant from three doors: the flush-aware terminal model over
// whole-line deltas, the rune-at-a-time deltas with spinner ticks
// interleaved, and the steering Enter over a parked wrapped input with
// an open pending line.

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/core"
)

// reproScreen replays the session's byte stream through the vt harness
// and returns the rows (or fails).
func reproScreen(t *testing.T, s *scriptedSession, width int) []string {
	t.Helper()
	v := newVT(width)
	v.feed(s.out.Bytes())
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%s", v.err, s.out.String())
	}
	return v.rows
}

// vtFlush is the vt harness with one terminal behavior added: the
// deferred wrap resolves at a write boundary (a flush point). A real
// terminal may resolve the pending wrap when its input processing
// flushes between two writes; a frame that leaves the cursor at the
// last column across a write boundary then shifts by one row, and the
// next op's cursor-up tally is off by a row — the clear misses the top
// row (the indicator) and the indicator lands between committed text.
//
// feedWrite is one write to the terminal: the bytes of a single
// io.WriteString. The wrap pending at the end of a feedWrite resolves
// (the cursor advances) — the flush.
type vtFlush struct {
	width int
	rows  []string
	r, c  int
	err   string
}

func newVTFlush(width int) *vtFlush { return &vtFlush{width: width} }

func (v *vtFlush) fail(why string) {
	if v.err == "" {
		v.err = why
	}
}

func (v *vtFlush) ensureRow(r int) {
	for len(v.rows) <= r {
		v.rows = append(v.rows, "")
	}
}

func (v *vtFlush) writeRune(r rune) {
	if v.c > 0 && v.c == v.width {
		v.r++
		v.c = 0
	}
	v.ensureRow(v.r)
	rs := []rune(v.rows[v.r])
	if v.c > len(rs) {
		v.c = len(rs)
	}
	v.rows[v.r] = string(append(rs[:v.c], append([]rune{r}, rs[v.c:]...)...))
	v.c++
}

// feedWrite is one write to the terminal (the write boundary).
func (v *vtFlush) feedWrite(b []byte) {
	v.feedBytes(b)
	// the flush: a pending wrap (cursor at the last column) resolves.
	if v.c > 0 && v.c == v.width {
		v.r++
		v.c = 0
		v.ensureRow(v.r)
	}
}

func (v *vtFlush) feedBytes(b []byte) {
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
				// erase from the cursor to the end of the screen (0J).
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

// TestTearFlushBoundaries is the live-region tear (SPEC_TUI's testing
// section): a repaint is many small writes, and a line written exactly
// to the last column leaves a pending wrap across a write boundary —
// the terminal resolves it at the flush, the cursor shifts a row, and
// the next op's clear misses the top row (the indicator), which lands
// between committed text. Stream multi-line wrapped reasoning through
// the flush-aware terminal model, write by write as the tty delivers
// them, and assert no indicator bytes land between committed text.
func TestTearFlushBoundaries(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(20),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	// lines that land exactly on the width (20) force a pending wrap at
	// write boundaries, the way a decoder's deltas do.
	s.fe.Notify(core.ReasoningDelta{Text: "twenty char line one\n"})
	s.fe.Notify(core.ReasoningDelta{Text: "twenty char line two\n"})
	s.fe.Notify(core.ReasoningDelta{Text: "third\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	v := newVTFlush(20)
	for _, chunk := range s.out.writeChunks() {
		v.feedWrite([]byte(chunk))
	}
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%s", v.err, s.out.String())
	}
	t.Logf("screen:\n%s", strings.Join(v.rows, "\n"))
	// the committed reasoning lines, in order, each intact — no
	// indicator (spinner frame + phase) between them.
	wantIdx := []string{"twenty char line one", "twenty char line two", "third"}
	last := -1
	for _, want := range wantIdx {
		idx := -1
		for i := last + 1; i < len(v.rows); i++ {
			if v.rows[i] == want {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("the committed line %q is missing or out of order:\n%q", want, v.rows)
		}
		last = idx
	}
	// no indicator row between the first committed reasoning line and
	// the end of the committed block (before the usage line).
	usage := -1
	for i := len(v.rows) - 1; i >= 0; i-- {
		if strings.Contains(v.rows[i], "cache r") {
			usage = i
			break
		}
	}
	for i := 0; i < usage; i++ {
		r := strings.TrimLeft(v.rows[i], " ")
		if (strings.HasPrefix(r, "| ") || strings.HasPrefix(r, "/ ") ||
			strings.HasPrefix(r, "- ") || strings.HasPrefix(r, `\ `)) &&
			strings.Contains(v.rows[i], "thinking") {
			t.Fatalf("the indicator landed at row %d, between committed text:\n%q", i, v.rows)
		}
	}
}

// streamAndScreen runs a turn with the given reasoning text (delivered
// one rune at a time, ticks interleaved) and returns the final vt screen.
func streamAndScreen(t *testing.T, width int, text string) []string {
	t.Helper()
	th := oledTheme(t)
	ticks := make(chan time.Time, 64)
	s := newScriptedSession(t, WithTheme(th), WithWidth(width),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(ticks))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	runes := []rune(text)
	for i, r := range runes {
		s.fe.Notify(core.ReasoningDelta{Text: string(r)})
		if i%7 == 0 {
			ticks <- time.Time{}
		}
	}
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	// let the tick loop drain
	deadline := time.Now().Add(2 * time.Second)
	for {
		n := len(s.out.Bytes())
		time.Sleep(2 * time.Millisecond)
		if len(s.out.Bytes()) == n && time.Since(deadline) > 0 {
			break
		}
	}
	v := newVT(width)
	v.feed(s.out.Bytes())
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%s", v.err, s.out.String())
	}
	return v.rows
}

// isIndicator reports whether a row is the activity row (the indicator):
// it starts with a spinner frame or the phase word.
func isIndicator(r string) bool {
	rr := strings.TrimPrefix(r, " ")
	for _, f := range []string{"|", "/", "-", "\\"} {
		if strings.HasPrefix(rr, f+" ") || strings.HasPrefix(rr, f+" thinking") || strings.HasPrefix(rr, f+" bash") {
			return true
		}
	}
	return strings.Contains(rr, "thinking") || strings.Contains(rr, " bash")
}

// TestTearCharByChar: stream reasoning one rune at a time (fast deltas,
// spinner ticks interleaved) with the pending line wrapping, and assert
// no indicator bytes land between committed text on the final screen.
func TestTearCharByChar(t *testing.T) {
	text := "first reasoning line that wraps across the edge of the terminal " +
		"and keeps going to force a wrap\n" +
		"second line of the reasoning that also wraps around\n" +
		"third short line\n"
	rows := streamAndScreen(t, 24, text)
	t.Logf("final screen:\n%s", strings.Join(rows, "\n"))

	// Find the committed reasoning lines and check that no indicator row
	// is wedged between the first and the last committed reasoning line.
	first, last := -1, -1
	for i, r := range rows {
		if strings.Contains(r, "reasoning") || strings.Contains(r, "second line") || strings.Contains(r, "third short") {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 {
		t.Fatalf("no committed reasoning line found:\n%q", rows)
	}
	for i := first; i <= last; i++ {
		if isIndicator(rows[i]) {
			t.Fatalf("the indicator (activity row) landed at row %d, between committed reasoning lines:\n%q", i, rows)
		}
	}
}

// TestTearSteeringEnter: a steering Enter mid-stream, with a parked
// wrapped input and an open pending line, then the interrupted turn's
// end and the next turn. enter() freezes the input row and leaves the
// activity row and the pending line standing (they become scrollback,
// the interrupt's snapshot); the tear would be a frame character inside
// a committed prose row, not the snapshot's own indicator row.
func TestTearSteeringEnter(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(12),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	ctx, cancel := context.WithCancel(context.Background())
	ctx = core.WithInterrupt(ctx, cancel)
	saved := s.ctx
	s.ctx = ctx
	defer func() { s.ctx = saved }()
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	// establish the turn and open a pending line.
	s.fe.Notify(core.ReasoningDelta{Text: "streaming reasoning that wraps around and around and around\n"})
	s.fe.Notify(core.ReasoningDelta{Text: "still thinking "})
	s.await("still thinking")
	// type a long line (wrapped, parked) and steer with Enter.
	long := "steer this turn in a long way"
	s.si.feed(long)
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.fe.mu.Lock()
		p := s.fe.live.parked
		s.fe.mu.Unlock()
		if p > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	s.si.feed("\n")
	s.awaitCtxDone(ctx)
	s.fe.Notify(core.ReasoningDelta{Text: "more after the interrupt\n"})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnInterrupt})
	s.ctx = saved
	line, err := s.input()
	if line != long || err != nil {
		t.Fatalf("the steering line = (%q, %v)", line, err)
	}
	// finish the second turn.
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	rows := reproScreen(t, s, 12)
	t.Logf("final screen:\n%s", strings.Join(rows, "\n"))

	// the committed content stands, in order: turn one's reasoning, the
	// frozen steering line, turn two's output.
	marks := []string{"streaming re", "❯ steer this", "more after t"}
	last := -1
	for _, m := range marks {
		idx := strings.Index(strings.Join(rows, "\n"), m)
		if idx < 0 || idx <= last {
			t.Fatalf("the committed marker %q is missing or out of order:\n%q", m, rows)
		}
		last = idx
	}
	// the tear: a frame character inside a committed prose row. The
	// snapshot's own indicator row (frame + " thinking") is the
	// standing activity row enter() leaves, not a tear.
	for i, r := range rows {
		if strings.ContainsAny(r, "|/-\\") &&
			(strings.Contains(r, "streaming") || strings.Contains(r, "asoning") || strings.Contains(r, "around")) {
			t.Fatalf("row %d: a frame character inside committed prose: %q", i, r)
		}
	}
}
