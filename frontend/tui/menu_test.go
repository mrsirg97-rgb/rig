package tui

import (
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/command"
)

func menuSession(t *testing.T) *scriptedSession {
	t.Helper()
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60), WithSize(sizeFixture(60, 20)),
		WithCommands(command.All(), &command.Env{}))
	started := make(chan struct{})
	go func() {
		close(started)
		_, _ = s.fe.Input(s.ctx)
	}()
	<-started
	s.await(promptMark(th))
	return s
}

func TestTheMenuOpensAtASingleSub(t *testing.T) {
	s := menuSession(t)
	s.si.feed("/decision")
	s.await("enqueue a training run off the turn")
	s.await("tab/↓ pick · enter runs")
}

func TestASingleTopLevelMatchGhostsAndKeepsTheMenuClosed(t *testing.T) {
	s := menuSession(t)
	s.si.feed("/mod")
	s.await("els")
	bandSettle(t, s)
	for _, row := range bandScreen(t, s, 60) {
		if strings.Contains(row, "tab/↓ pick") {
			t.Fatalf("a single top-level match ghosts, the menu stays closed:\n%s", strings.Join(bandScreen(t, s, 60), "\n"))
		}
	}
}
