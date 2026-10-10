package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

func reproScreen(t *testing.T, s *scriptedSession, width int) []string {
	t.Helper()
	v := newVT(width)
	v.feed(s.out.Bytes())
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%s", v.err, s.out.String())
	}
	return v.rows
}

func streamAndScreen(t *testing.T, width int, text string) []string {
	t.Helper()
	th := oledTheme(t)
	ticks := make(chan time.Time, 64)
	s := newScriptedSession(t, th, WithWidth(width),
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

func isIndicator(r string) bool {
	rr := strings.TrimPrefix(r, " ")
	return rr == "thinking" || rr == "bash"
}

func TestTearCharByChar(t *testing.T) {
	text := "first reasoning line that wraps across the edge of the terminal " +
		"and keeps going to force a wrap\n" +
		"second line of the reasoning that also wraps around\n" +
		"third short line\n"
	rows := streamAndScreen(t, 24, text)
	t.Logf("final screen:\n%s", strings.Join(rows, "\n"))

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

func TestTearSteeringEnter(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(12),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	ctx, cancel := context.WithCancel(context.Background())
	ctx = core.WithInterrupt(ctx, cancel)
	saved := s.ctx
	s.ctx = ctx
	defer func() { s.ctx = saved }()
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}

	s.fe.Notify(core.ReasoningDelta{Text: "streaming reasoning that wraps around and around and around\n"})
	s.fe.Notify(core.ReasoningDelta{Text: "still thinking "})
	s.tick()
	s.await("thinking")

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

	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	rows := reproScreen(t, s, 12)
	t.Logf("final screen:\n%s", strings.Join(rows, "\n"))

	marks := []string{"streaming", "❯ steer this", "more after"}
	last := -1
	for _, m := range marks {
		idx := strings.Index(strings.Join(rows, "\n"), m)
		if idx < 0 || idx <= last {
			t.Fatalf("the committed marker %q is missing or out of order:\n%q", m, rows)
		}
		last = idx
	}

	for i, r := range rows {
		if strings.ContainsAny(r, "|/-\\") &&
			(strings.Contains(r, "streaming") || strings.Contains(r, "asoning") || strings.Contains(r, "around")) {
			t.Fatalf("row %d: a frame character inside committed prose: %q", i, r)
		}
	}
}
