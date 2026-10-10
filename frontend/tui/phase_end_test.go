package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

func liveTurnSession(t *testing.T, th Theme) *scriptedSession {
	t.Helper()
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	return s
}

func summarizeOutLoud(t *testing.T, s *scriptedSession) {
	t.Helper()
	s.fe.Notify(core.Compacting{})
	s.fe.Notify(core.Phase{Name: "summarizing"})
	breathe(t, s, 1)
	awaitScreen(t, s, "summarizing · ", true)
}

func TestANoticeBreathesAfterAnInTurnCompaction(t *testing.T) {
	th := oledTheme(t)
	s := liveTurnSession(t, th)
	summarizeOutLoud(t, s)
	s.fe.Notify(core.Compacted{Summary: "s", Dropped: 1200, Kept: 400})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.fe.Notify(core.Notice{Source: "graph", Text: "the map moved"})
	awaitScreen(t, s, "graph: the map moved", true)
}

func TestAFaultedInTurnCompactionGivesTheRowBack(t *testing.T) {
	th := oledTheme(t)
	s := liveTurnSession(t, th)
	summarizeOutLoud(t, s)
	s.fe.Notify(core.Fault{Err: errors.New("the summary call refused")})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	awaitScreen(t, s, "summarizing · ", false)
	s.fe.mu.Lock()
	aside, ticking := s.fe.aside, s.fe.tickStop != nil
	s.fe.mu.Unlock()
	if aside != "" || ticking {
		t.Fatalf("the fault ended the phase: aside=%q ticker=%v", aside, ticking)
	}
}
