package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

func TestTearNoSyncPromptNeverBlanks(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(24),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}

	runes := []rune("first reasoning line that wraps across the edge of the terminal and keeps going to force a wrap")
	for i, r := range runes {
		s.fe.Notify(core.ReasoningDelta{Text: string(r)})
		if i%7 == 0 {
			s.tick()
		}
	}
	s.fe.Notify(core.ReasoningDelta{Text: " second reasoning line that also wraps around\n"})
	s.tick()
	var big strings.Builder
	for i := 0; i < 40; i++ {
		big.WriteString("line ")
		big.WriteString(strings.Repeat("x", 60))
		big.WriteString(" ends here\n")
	}
	s.fe.Notify(core.ReasoningDelta{Text: big.String()})
	s.tick()
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	stable := 0
	for {
		n := len(s.out.Bytes())
		time.Sleep(2 * time.Millisecond)
		if len(s.out.Bytes()) == n {
			stable++
			if stable >= 25 {
				break
			}
		} else {
			stable = 0
		}
	}

	v := newVT(24)
	stream := s.out.Bytes()
	seen := false
	const split = 9
	for off := 0; off < len(stream); off += split {
		end := off + split
		if end > len(stream) {
			end = len(stream)
		}
		v.feed(stream[off:end])
		if v.err != "" {
			t.Fatalf("harness: %s\nstream:\n%s", v.err, s.out.String())
		}
		present := false
		for _, r := range v.rows {
			if strings.Contains(r, "❯") {
				present = true
				break
			}
		}
		if present {
			seen = true
		}
		if seen && !present {
			from := off - 60
			if from < 0 {
				from = 0
			}
			t.Fatalf("the prompt vanished at byte %d:\n%s\nstream tail:\n%q", off,
				strings.Join(v.rows, "\n"), stream[from:end])
		}
	}
}

func TestTearNoSyncPairIsOneWrite(t *testing.T) {
	th := oledTheme(t)
	out := &lockBuf{}
	l := newLive(out, 40)
	l.draw(th.Paint(SlotText, "hello"), []string{"tail"}, "")

	out.mu.Lock()
	defer out.mu.Unlock()
	if len(out.writes) != 1 {
		t.Fatalf("one flush must be one write: %d writes", len(out.writes))
	}
	chunk := out.b.String()
	if !strings.HasPrefix(chunk, syncOn) || !strings.HasSuffix(chunk, syncOff) {
		t.Fatalf("the sync pair must close the frame's write: %q", chunk)
	}
}
