package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

func sizeFixture(w, h int) func() (int, int, bool) {
	return func() (int, int, bool) { return w, h, true }
}

func screenAt(t *testing.T, s *scriptedSession, w, h int) []string {
	t.Helper()
	v := newVTScreen(w, h)
	v.feed(s.out.Bytes())
	if v.err != "" {
		t.Fatalf("harness: %s", v.err)
	}
	if v.clamped > 0 {
		t.Fatalf("the protocol relied on %d cursor clamps", v.clamped)
	}
	rows := v.rows
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

func TestStreamingProseStaysInsideTheViewport(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(50), WithSize(sizeFixture(50, 14)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt returned %q", got)
	}

	prose := strings.Repeat("thinking out loud here ", 40)
	s.fe.Notify(core.ReasoningDelta{Text: prose})
	s.tick()
	s.await("thinking out loud here")
	time.Sleep(50 * time.Millisecond)

	rows := screenAt(t, s, 50, 14)
	if len(rows) != 14 {
		t.Fatalf("the screen holds %d rows, want the 14-row viewport:\n%q", len(rows), rows)
	}
	if got := paintFree(rows[0]); !strings.Contains(got, "hidden") {
		t.Fatalf("the capped pending line does not name its hidden head: %q", rows[0])
	}
	if !strings.Contains(paintFree(rows[5]), "out loud here") {
		t.Fatalf("the pending tail's newest row is not on screen: %q", rows[5])
	}
	if paintFree(rows[7]) != "| thinking" {
		t.Fatalf("the activity row drifted: %q", rows[7])
	}
	if paintFree(rows[9]) != "❯ " {
		t.Fatalf("the input row drifted: %q", rows[9])
	}

	s.fe.Notify(core.ReasoningDelta{Text: "\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 5}})
	s.fe.Notify(core.TurnEnd{})
	s.tick()
	s.await("up 10 down 5 · cache r 0 0%")
	time.Sleep(50 * time.Millisecond)

	rows = screenAt(t, s, 50, 14)
	for _, r := range rows {
		if strings.Contains(paintFree(r), "hidden") {
			t.Fatalf("a stale live row survived the turn: %q\n%q", r, rows)
		}
	}
	n := len(rows)
	if n < 7 {
		t.Fatalf("the screen lost the transcript tail:\n%q", rows)
	}
	if paintFree(rows[n-6]) != "" {
		t.Fatalf("the blank between the transcript and the input is gone: %q", rows)
	}
	if !strings.Contains(paintFree(rows[n-7]), "out loud here") {
		t.Fatalf("the committed prose tail is not the row above the blank: %q", rows)
	}
}

type wideCmd struct {
	fakeCmd
	verbs []command.Sub
}

func (w *wideCmd) Sub() []command.Sub { return w.verbs }

func TestMenuWindowFitsTheViewport(t *testing.T) {
	th := oledTheme(t)
	verbs := make([]command.Sub, 8)
	for i := range verbs {
		verbs[i] = command.Sub{Name: fmt.Sprintf("verb%d", i+1), Desc: "test"}
	}
	wc := &wideCmd{fakeCmd: fakeCmd{name: "wide", out: "ok"}, verbs: verbs}
	s := newScriptedSession(t, th, WithWidth(50), WithSize(sizeFixture(50, 9)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{wc}, nil),
	)
	done := make(chan struct{})
	go func() {
		_, _ = s.input()
		close(done)
	}()
	s.await(promptMark(th))

	s.si.feed("/wide ")
	s.await("verb1")
	s.await(promptMark(th) + th.Paint(SlotText, " /wide "))

	rows := screenAt(t, s, 50, 9)
	if len(rows) != 9 {
		t.Fatalf("the screen holds %d rows, want the 9-row viewport:\n%q", len(rows), rows)
	}
	joined := ""
	for _, r := range rows {
		joined += paintFree(r) + "\n"
	}
	if !strings.Contains(joined, "… 7 more") {
		t.Fatalf("the shrunken menu window lost its more-tail:\n%s", joined)
	}
	if strings.Contains(joined, "verb2") {
		t.Fatalf("the menu window did not shrink to the viewport:\n%s", joined)
	}
	want := []string{"❯ /wide ", "", "huihui3.8", "xhigh · default · auto", "up 214k down 18k · cache r 187k 87%"}
	tail := rows[len(rows)-len(want):]
	for i := range want {
		if paintFree(tail[i]) != want[i] {
			t.Fatalf("the region's tail broke: %q\n%q", tail, rows)
		}
	}
	_ = done
}

func TestTypingRewritesOnlyTheInputRow(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	done := make(chan struct{})
	go func() {
		_, _ = s.input()
		close(done)
	}()
	s.await(promptMark(th))

	before := len(s.out.writeChunks())
	s.si.feed("x")
	s.await(promptMark(th) + th.Paint(SlotText, " x"))
	chunks := s.out.writeChunks()
	last := chunks[len(chunks)-1]
	if len(chunks) == before {
		t.Fatalf("the keystroke painted no frame at all")
	}
	if strings.Contains(last, "\x1b[0J") {
		t.Fatalf("a keystroke re-laid the whole region (clear-below in the frame): %q", last)
	}
	rows := screenAt(t, s, 50, 24)
	want := []string{"❯ x", "", "huihui3.8", "xhigh · default · auto", "up 214k down 18k · cache r 187k 87%"}
	tail := rows[len(rows)-len(want):]
	for i := range want {
		if paintFree(tail[i]) != want[i] {
			t.Fatalf("the screen broke on a keystroke: %q\n%q", tail, rows)
		}
	}
	_ = done
}

func TestFramesAfterACommitCarryNoCursorDown(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	done := make(chan struct{})
	go func() {
		_, _ = s.input()
		close(done)
	}()
	s.await(promptMark(th))

	s.si.feed("hi\n")
	s.await("up 214k down 18k · cache r 187k 87%")
	time.Sleep(50 * time.Millisecond)
	s.fe.Notify(core.TextDelta{Text: "reply\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 1}})
	s.fe.Notify(core.TurnEnd{})
	s.tick()
	s.await("reply")
	time.Sleep(50 * time.Millisecond)

	before := len(s.out.writeChunks())
	s.si.feed("x")
	s.await(promptMark(th) + th.Paint(SlotText, " x"))
	chunks := s.out.writeChunks()
	last := chunks[len(chunks)-1]
	if len(chunks) == before {
		t.Fatalf("the keystroke painted no frame at all")
	}
	if strings.Contains(last, "B") && strings.Contains(last, "\x1b") {
		for _, part := range strings.Split(last, "\x1b[") {
			if len(part) > 0 && part[0] >= '0' && part[0] <= '9' && strings.HasPrefix(part[1:], "B") {
				t.Fatalf("the keystroke frame re-parked with a cursor-down: %q", last)
			}
		}
	}
	_ = done
}

func TestInputWindowFitsTheViewport(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(50), WithSize(sizeFixture(50, 8)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	done := make(chan struct{})
	go func() {
		_, _ = s.input()
		close(done)
	}()
	s.await(promptMark(th))

	s.si.feed(strings.Repeat("word ", 40))
	s.await("up 214k down 18k · cache r 187k 87%")
	time.Sleep(50 * time.Millisecond)

	rows := screenAt(t, s, 50, 8)
	if len(rows) != 8 {
		t.Fatalf("the screen holds %d rows, want the 8-row viewport:\n%q", len(rows), rows)
	}
	joined := ""
	for _, r := range rows {
		joined += paintFree(r) + "\n"
	}
	if strings.Contains(joined, "❯") {
		t.Fatalf("the scrolled input window must not show the prompt glyph:\n%s", joined)
	}
	if !strings.Contains(joined, "word") {
		t.Fatalf("the input window lost its text:\n%s", joined)
	}
	want := []string{"", "huihui3.8", "xhigh · default · auto", "up 214k down 18k · cache r 187k 87%"}
	tail := rows[len(rows)-len(want):]
	for i := range want {
		if paintFree(tail[i]) != want[i] {
			t.Fatalf("the status block broke under a long input:\n%q", rows)
		}
	}
	_ = done
}

func mutableSize(w, h *int) func() (int, int, bool) {
	return func() (int, int, bool) { return *w, *h, true }
}

func checkViewportInvariants(t *testing.T, label string, v *vt, wantMark string) {
	t.Helper()
	if v.err != "" {
		t.Fatalf("%s: harness: %s", label, v.err)
	}
	if v.clamped > 0 {
		t.Fatalf("%s: the protocol relied on %d cursor clamps", label, v.clamped)
	}
	rows := v.rows
	if len(rows) != v.height {
		t.Fatalf("%s: the screen holds %d rows, want the %d-row viewport:\n%q", label, len(rows), v.height, rows)
	}
	if wantMark != "" {
		joined := paintFree(strings.Join(rows, "\n"))
		if !strings.Contains(joined, wantMark) {
			t.Fatalf("%s: the committed prose was wiped from the screen:\n%q", label, rows)
		}
	}
	bottom := paintFree(strings.Join(rows[len(rows)-4:], "\n"))
	bottom = strings.Join(strings.Fields(bottom), " ")
	if !strings.Contains(bottom, "cache r") {
		t.Fatalf("%s: the model info is not at the bottom:\n%q", label, rows)
	}
	inRow := -1
	for r := len(rows) - 1; r >= 0; r-- {
		if strings.HasPrefix(paintFree(rows[r]), "❯") {
			inRow = r
			break
		}
	}
	if inRow < 0 {
		t.Fatalf("%s: the input row is gone:\n%q", label, rows)
	}
	for r := inRow + 1; r < len(rows); r++ {
		if strings.Contains(paintFree(rows[r]), "thinking") {
			t.Fatalf("%s: the activity row painted over the status block:\n%q", label, rows)
		}
	}
	if !strings.Contains(paintFree(strings.Join(rows, "\n")), "thinking") {
		t.Fatalf("%s: the activity row is gone:\n%q", label, rows)
	}
}

func streamViewportFrames(t *testing.T, s *scriptedSession, v *vt, n int, label string) {
	t.Helper()
	painted := 0
	for i := 0; i < n; i++ {
		s.fe.Notify(core.ReasoningDelta{Text: "word word word word word "})
		s.tick()
		time.Sleep(4 * time.Millisecond)
		chunks := s.out.writeChunks()
		for ; painted < len(chunks); painted++ {
			v.feed([]byte(chunks[painted]))
		}
		checkViewportInvariants(t, label, v, "")
	}
}

func TestResizeMidStreamKeepsTheTranscript(t *testing.T) {
	th := oledTheme(t)
	w, h := 50, 14
	s := newScriptedSession(t, th, WithWidth(50), WithSize(mutableSize(&w, &h)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt returned %q", got)
	}
	v := newVTScreen(50, 14)
	streamViewportFrames(t, s, v, 10, "pre-resize")

	// the size changes under the TUI with no signal delivered: the next
	// repaint reads the new geometry and must aim with the painted one
	w, h = 36, 10
	v.width = 36
	streamViewportFrames(t, s, v, 5, "post-resize")
	checkViewportInvariants(t, "post-resize", v, "word word word")
}

func TestWinchRedrawAimsAtThePaintedRegion(t *testing.T) {
	th := oledTheme(t)
	w, h := 50, 14
	winch := make(chan struct{}, 4)
	s := newScriptedSession(t, th, WithWidth(50), WithSize(mutableSize(&w, &h)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithWinch(winch),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt returned %q", got)
	}
	v := newVTScreen(50, 14)
	streamViewportFrames(t, s, v, 10, "pre-resize")

	w, h = 36, 10
	v.width = 36
	winch <- struct{}{}
	time.Sleep(30 * time.Millisecond)
	chunks := s.out.writeChunks()
	painted := 0
	for ; painted < len(chunks); painted++ {
		v.feed([]byte(chunks[painted]))
	}
	checkViewportInvariants(t, "winch redraw", v, "word word word")

	streamViewportFrames(t, s, v, 5, "post-resize")
	checkViewportInvariants(t, "post-resize", v, "word word word")
}

func TestViewportBoundCountsTheStatusRowsItPaints(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(24), WithSize(sizeFixture(24, 10)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt returned %q", got)
	}
	// the usage row is 35 columns on a 24-column pane: the status block
	// paints five rows where the row count sees four
	v := newVTScreen(24, 10)
	streamViewportFrames(t, s, v, 20, "narrow")
}

func TestEnterWithMenuOpenRepaintsTheWholeRegion(t *testing.T) {
	th := oledTheme(t)
	verbs := make([]command.Sub, 8)
	for i := range verbs {
		verbs[i] = command.Sub{Name: fmt.Sprintf("verb%d", i+1), Desc: "test"}
	}
	wc := &wideCmd{fakeCmd: fakeCmd{name: "wide", out: "ok"}, verbs: verbs}
	s := newScriptedSession(t, th, WithWidth(50), WithSize(sizeFixture(50, 14)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{wc}, nil),
	)
	done := make(chan struct{})
	go func() {
		_, _ = s.input()
		close(done)
	}()
	s.await(promptMark(th))

	v := newVTScreen(50, 14)
	paint := func(label string) {
		t.Helper()
		chunks := s.out.writeChunks()
		for _, c := range chunks {
			v.feed([]byte(c))
		}
		if v.err != "" {
			t.Fatalf("%s: harness: %s", label, v.err)
		}
	}
	paint("start")
	n := len(s.out.writeChunks())

	s.si.feed("/wide ")
	s.await("verb1")
	time.Sleep(30 * time.Millisecond)
	chunks := s.out.writeChunks()
	for i := n; i < len(chunks); i++ {
		v.feed([]byte(chunks[i]))
	}
	n = len(chunks)

	s.si.feed("\n")
	s.await("ok")
	time.Sleep(30 * time.Millisecond)
	chunks = s.out.writeChunks()
	for i := n; i < len(chunks); i++ {
		v.feed([]byte(chunks[i]))
	}
	joined := paintFree(strings.Join(v.rows, "\n"))
	if strings.Contains(joined, "verb1") || strings.Contains(joined, "tab/↓ pick") {
		t.Fatalf("the menu rows outlived the submit:\n%q", v.rows)
	}
	_ = done
}

func TestToolResultRendersAtItsCountedWidth(t *testing.T) {
	th := oledTheme(t)
	for _, width := range []int{50, 24} {
		t.Run("width"+strconv.Itoa(width), func(t *testing.T) {
			s := newScriptedSession(t, th, WithWidth(width), WithSize(sizeFixture(width, 14)),
				WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
			)
			if got := s.prompt(promptMark(th), "go\n"); got != "go" {
				t.Fatalf("the prompt returned %q", got)
			}
			v := newVTScreen(width, 14)
			painted := 0
			step := func(label string) {
				t.Helper()
				chunks := s.out.writeChunks()
				for ; painted < len(chunks); painted++ {
					v.feed([]byte(chunks[painted]))
				}
				if v.err != "" {
					t.Fatalf("%s: harness: %s", label, v.err)
				}
				if v.clamped > 0 {
					t.Fatalf("%s: the protocol relied on %d cursor clamps", label, v.clamped)
				}
			}
			for i := 0; i < 12; i++ {
				s.fe.Notify(core.ReasoningDelta{Text: "word word word word word word "})
				s.tick()
				time.Sleep(3 * time.Millisecond)
				step("stream")
			}
			s.fe.Notify(core.ToolStart{Call: core.ToolCall{
				Name: "read", Args: []byte(`{"path":"~/Projects/rig/frontend/tui/tui.go"}`),
			}})
			time.Sleep(3 * time.Millisecond)
			step("toolstart")
			content := "package tui\n\nimport (\n\t\"bufio\"\n\t\"context\"\n\t\"io\"\n"
			for i := 0; i < 1800; i++ {
				content += "\t// a line of go source with a tab\tinside\n"
			}
			content += "}\n}\n"
			s.fe.Notify(core.ToolResult{Content: content, Duration: 400})
			time.Sleep(6 * time.Millisecond)
			step("toolresult")

			rows := v.rows
			joined := paintFree(strings.Join(rows, "\n"))
			// painted rows are terminal-width exact: a raw tab renders at the
			// next tab stop while the width math counts nothing, so a row
			// carrying one wraps into rows the bookkeeping never sees
			if strings.Contains(joined, "\t") {
				t.Fatalf("width %d: a painted row carries a raw tab:\n%q", width, rows)
			}
			// foreign fragments from other rows must not land in the block
			for _, frag := range []string{"cache r", "xhigh", "huihui"} {
				if strings.Count(joined, frag) > 1 {
					t.Fatalf("width %d: the fragment %q landed twice — torn rows inside the committed block:\n%q", width, frag, rows)
				}
			}
			// the elided block: one marker, and no fail glyph (the tool succeeded)
			if markers := strings.Count(joined, "lines hidden"); markers != 1 {
				t.Fatalf("width %d: the block carries %d elide markers, want 1:\n%q", width, markers, rows)
			}
			if strings.Contains(joined, th.Glyph(GlyphFail)) {
				t.Fatalf("width %d: a fail glyph leaked into a succeeded tool's frame:\n%q", width, rows)
			}
		})
	}
}

// resizeVT models the pane's reflow under a height change: a shrink keeps
// the top rows and cuts the bottom, a grow appends blank rows, and the
// cursor clamps into range. Width changes reflow the rows themselves and
// go through the winch test.
func resizeVT(v *vt, w, h int) *vt {
	nv := newVTScreen(w, h)
	nv.hist = v.hist
	for r := 0; r < h && r < len(v.rows); r++ {
		nv.ensureRow(r)
		nv.rows[r] = v.rows[r]
	}
	nv.r = v.r
	nv.c = v.c
	if nv.r > h-1 {
		nv.r = h - 1
	}
	if nv.c > w-1 {
		nv.c = w - 1
	}
	return nv
}

func TestKeyboardShrinkAimsInsideTheViewport(t *testing.T) {
	th := oledTheme(t)
	w, h := 50, 14
	s := newScriptedSession(t, th, WithWidth(50), WithSize(mutableSize(&w, &h)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt returned %q", got)
	}
	v := newVTScreen(50, 14)
	// one continuous feed index across the resizes: the frames replay in
	// order onto the pane the way a terminal sees them
	painted := 0
	step := func(label string, want string) {
		t.Helper()
		s.fe.Notify(core.ReasoningDelta{Text: "word word word word word "})
		s.tick()
		time.Sleep(4 * time.Millisecond)
		chunks := s.out.writeChunks()
		for ; painted < len(chunks); painted++ {
			v.feed([]byte(chunks[painted]))
		}
		checkViewportInvariants(t, label, v, want)
	}
	for i := 0; i < 10; i++ {
		step("pre-shrink", "")
	}

	// the phone's virtual keyboard opens: the pane loses four rows and
	// the width holds. No signal is needed — the size is read at the
	// repaint — and the first aim after the shrink must not overshoot
	// the shorter screen.
	w, h = 50, 10
	v = resizeVT(v, 50, 10)
	for i := 0; i < 6; i++ {
		step("post-shrink", "word word word")
	}

	// and it closes again
	w, h = 50, 14
	v = resizeVT(v, 50, 14)
	for i := 0; i < 6; i++ {
		step("regrown", "word word word")
	}
}

func TestToolBlockTabGapsHoldNoPreviousFrame(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(50), WithSize(sizeFixture(50, 20)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt returned %q", got)
	}
	v := newVTScreen(50, 20)
	painted := 0
	feed := func(label string) {
		t.Helper()
		chunks := s.out.writeChunks()
		for ; painted < len(chunks); painted++ {
			v.feed([]byte(chunks[painted]))
		}
		if v.err != "" {
			t.Fatalf("%s: harness: %s", label, v.err)
		}
		if v.clamped > 0 {
			t.Fatalf("%s: the protocol relied on %d cursor clamps", label, v.clamped)
		}
	}
	// a small region: the status block sits a few rows above the bottom
	s.fe.Notify(core.ReasoningDelta{Text: "word word word "})
	s.tick()
	time.Sleep(4 * time.Millisecond)
	feed("stream")
	s.fe.Notify(core.ToolStart{Call: core.ToolCall{
		Name: "bash", Args: []byte(`{"command":"sed -n '80,86p' golden_test.go"}`),
	}})
	time.Sleep(4 * time.Millisecond)
	feed("toolstart")
	// the tool's content is tab-indented go source: the block's rows
	// carry the same shape as the pane's previous frame
	content := "func (l *lockBuf) Reset() {\n\tl.mu.Lock()\n\tdefer l.mu.Unlock()\n\tl.b.Reset()\n}\n"
	s.fe.Notify(core.ToolResult{Content: content, Duration: 400})
	time.Sleep(6 * time.Millisecond)
	feed("toolresult")

	// a tab advances to the next eight-column stop and writes nothing:
	// the cells it skips keep whatever the previous frame left in them.
	// The seam paints the gap as spaces, so the block's rows render
	// exactly as expanded — no fragment of the frame before survives in
	// the gap (the model info, the effort row, the pend tail).
	joined := paintFree(strings.Join(v.rows, "\n"))
	for _, want := range []string{
		"  func (l *lockBuf) Reset() {",
		"        l.mu.Lock()",
		"        defer l.mu.Unlock()",
		"        l.b.Reset()",
		"  }",
		"bash ✓ 0.0s",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the block's row %q did not render as expanded — a tab gap kept the previous frame's cells:\n%q", want, v.rows)
		}
	}
	if strings.Contains(joined, "\t") {
		t.Fatalf("a painted row carries a raw tab:\n%q", v.rows)
	}
}
