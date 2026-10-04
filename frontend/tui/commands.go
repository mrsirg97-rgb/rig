package tui

import (
	"context"
	"github.com/mrsirg97-rgb/rig/v2/command"
	"strings"
)

func (t *tui) dispatch(ctx context.Context, line string) {

	name, args := command.Parse(line)
	cmd, ok := t.commands[name]
	if !ok {
		t.commit(t.theme.Paint(SlotDim,
			"unknown command: "+name+" (known: "+strings.Join(t.known, ", ")+")"))
		return
	}
	out, err := cmd.Run(ctx, args, t.env)
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case err != nil:
		t.live.draw(t.commandOpeningLocked(name, args)+"\n"+t.theme.Paint(SlotError, err.Error()), t.liveLinesLocked(), t.statusLineLocked())
		return
	case name == "todo" || name == "scheduler":
		if out != "" {
			opening := t.commandOpeningLocked(name, args)
			if name == "todo" {
				t.live.draw(RenderTodoBlock(t.theme, opening, out), t.liveLinesLocked(), t.statusLineLocked())
			} else {
				t.live.draw(RenderSchedulerBlock(t.theme, opening, out), t.liveLinesLocked(), t.statusLineLocked())
			}
		}
	default:
		if out == "" {
			break
		}
		opening := t.commandOpeningLocked(name, args)
		if block, ok := RenderListBlock(t.theme, opening, out); ok {
			t.live.draw(block, t.liveLinesLocked(), t.statusLineLocked())
			break
		}
		t.live.draw(RenderReplyBlock(t.theme, opening, name, out), t.liveLinesLocked(), t.statusLineLocked())
	}
	fresh := name == "new" || (name == "sessions" && strings.HasPrefix(args, "resume"))
	if t.statusIn != nil {
		t.recaptureStatusLocked(t.statusIn(context.Background()), fresh)
		t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
	}
}

const (
	openingRows = 1
	inputRows   = 1
)

func (t *tui) lines() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	status := strings.Count(t.statusLineLocked(), "\n")
	return t.height - status - openingRows - inputRows
}

func (t *tui) commandOpeningLocked(name, args string) string {
	s := t.theme.Paint(SlotEmber, "/"+name)
	if args != "" {
		s += t.theme.Paint(SlotDim, " · ") + t.theme.Paint(SlotText, args)
	}
	return s
}

func (t *tui) Ask(ctx context.Context, prompt string) bool {
	reply := make(chan bool, 1)
	t.mu.Lock()
	t.askText, t.askReply = prompt, reply
	t.mu.Unlock()
	t.paintInput()
	select {
	case ans := <-reply:
		return ans
	case <-ctx.Done():
		t.mu.Lock()
		t.askText, t.askReply = "", nil
		t.mu.Unlock()
		t.paintInput()
		return false
	}
}

func (t *tui) askAnswer(ans bool) {
	t.mu.Lock()
	reply := t.askReply
	t.askText, t.askReply = "", nil
	t.mu.Unlock()
	if reply == nil {
		return
	}
	reply <- ans
	t.paintInput()
}

func (t *tui) Steer(text string) bool {
	t.mu.Lock()
	t.slot = text
	t.hasSlot = true
	live := t.turnLive
	wasLive := t.steeredLive
	t.steeredLive = false
	cancel := t.cancel
	t.mu.Unlock()
	if live && cancel != nil {
		cancel()
		return true
	}
	return wasLive
}

func (t *tui) Interrupt() bool {
	t.mu.Lock()
	live := t.turnLive
	wasLive := t.steeredLive
	t.steeredLive = false
	cancel := t.cancel
	t.mu.Unlock()
	if live && cancel != nil {
		cancel()
		return true
	}
	return wasLive
}

func (t *tui) ClearSlot() {
	t.mu.Lock()
	t.slot = ""
	t.hasSlot = false
	t.mu.Unlock()
}

func (t *tui) LiveTurn() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.turnLive
}

func (t *tui) activityLineLocked() string {
	if !(t.turnLive || t.compacting) && t.noticing && len(t.notices) > 0 {
		n := t.notices[0]
		return t.theme.BreathPaint(noticeSlot(n.Level), t.noticeFrame, n.Source+": "+n.Text)
	}
	label := t.phase
	if label == "" {
		label = "thinking"
	}
	return t.theme.EmberPaint(t.frame, label)
}
