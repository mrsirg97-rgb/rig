package tui

import (
	"context"
	"fmt"
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
