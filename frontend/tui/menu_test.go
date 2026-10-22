package tui

import (
	"strings"
	"testing"
	"time"

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
	deadline := time.Now().Add(3 * time.Second)
	for {
		rows := bandScreen(t, s, 60)
		screen := strings.Join(rows, "\n")
		if strings.Contains(screen, "❯ /models") {
			if strings.Contains(screen, "tab/↓ pick") {
				t.Fatalf("a single top-level match ghosts, the menu stays closed:\n%s", screen)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the input row never ghosted /models:\n%s", screen)
		}
		time.Sleep(time.Millisecond)
	}
}
