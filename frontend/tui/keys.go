package tui

import (
	"io"
	"strings"
	"time"
)

const escDelay = 30 * time.Millisecond

func (t *tui) onKey(k key, r rune) {

	t.mu.Lock()
	asking := t.askReply != nil
	cancel := t.cancel
	t.mu.Unlock()
	if asking {
		switch {
		case r == 'y' || r == 'Y':
			t.askAnswer(true)
		case r == 'n' || r == 'N':
			t.askAnswer(false)
		case k == keyEsc:
			t.askAnswer(false)
			if cancel != nil {
				cancel()
			}
		case k == keyCtrlC:
			t.askAnswer(false)
			t.quitSession()
		}
		return
	}
	if !t.fromPager && t.pagerKey(k, r) {
		return
	}
	switch k {
	case keyPgUp:
		t.enterPager()
	case keyEnter:
		t.onEnter()
	case keyCtrlC:

		t.quitSession()
	case keyCtrlD:

		if strings.TrimSpace(t.ed.text()) == "" {
			t.quitSession()
		}
	case keyCtrlT:

		t.mu.Lock()
		t.showReasoning = !t.showReasoning
		t.mu.Unlock()
	case keyEsc:

		if t.closeMenu() {
			return
		}
		t.mu.Lock()
		live := t.turnLive
		cancel := t.cancel
		stop := t.idleInterrupt
		t.mu.Unlock()
		if strings.TrimSpace(t.ed.text()) == "" {
			switch {
			case live:
				if cancel != nil {
					cancel()
				}
			case stop != nil:
				stop()
			default:
				t.ed.apply(keyEsc, 0)
				t.mu.Lock()
				t.menuSyncLocked()
				t.mu.Unlock()
				t.paintInput()
			}
			return
		}
		t.ed.apply(keyEsc, 0)
		t.mu.Lock()
		t.menuSyncLocked()
		t.mu.Unlock()
		t.paintInput()
	case keyTab:

		t.mu.Lock()
		if t.menuOpenLocked() {
			t.menuNavigated = true
		}
		next, changed := t.tabTextLocked()
		t.mu.Unlock()
		if changed {
			t.ed.setText(next)
			t.mu.Lock()
			t.menuSyncLocked()
			t.mu.Unlock()
		}
		t.paintInput()
	case keyShiftTab:

		t.mu.Lock()
		if t.menuOpenLocked() {
			t.menuNavigated = true
			n := len(t.menuCands)
			t.menuSel = (t.menuSel - 1 + n) % n
		}
		t.mu.Unlock()
		t.paintInput()
	case keyUp, keyDown:

		t.mu.Lock()
		open := t.menuOpenLocked()
		if open {
			t.menuNavigated = true
			n := len(t.menuCands)
			if k == keyDown {
				t.menuSel = (t.menuSel + 1) % n
			} else {
				t.menuSel = (t.menuSel - 1 + n) % n
			}
		}
		t.mu.Unlock()
		if open {
			t.paintInput()
			return
		}
		t.ed.apply(k, r)
		t.mu.Lock()
		t.menuSyncLocked()
		t.mu.Unlock()
		t.paintInput()
	default:
		before := t.ed.text()
		t.ed.apply(k, r)
		if t.ed.text() != before {
			t.mu.Lock()
			t.menuSyncLocked()
			t.mu.Unlock()
		}
		t.paintInput()
	}
}

func (t *tui) menuSyncLocked() {
	t.menuCands, _, _ = t.completionLocked()
	t.menuSel = 0
	t.menuDead = false
	t.menuNavigated = false
}

func (t *tui) menuOpenLocked() bool {
	return len(t.menuCands) >= 2 && !t.menuDead
}

func (t *tui) closeMenu() bool {
	t.mu.Lock()
	open := t.menuOpenLocked()
	if open {
		t.menuDead = true
		t.menuNavigated = false
	}
	t.mu.Unlock()
	if open {
		t.paintInput()
	}
	return open
}

func (t *tui) pagerKey(k key, r rune) bool {
	t.mu.Lock()
	if t.pg == nil {
		t.mu.Unlock()
		return false
	}
	empty := strings.TrimSpace(t.ed.text()) == ""
	moved := false
	switch {
	case k == keyEsc, k == keyText && (r == 'q' || r == 'Q') && empty, k == keyEnter && empty:

		t.exitPagerLocked()
		t.mu.Unlock()
		return true
	case k == keyPgUp:
		moved = t.pg.move(t.pg.pageUp())
	case k == keyPgDn:
		moved = t.pg.move(-t.pg.pageDown())
	case k == keyHome && empty:
		moved = t.pg.move(len(t.pg.lines))
	case k == keyEnd && empty:
		moved = t.pg.move(-len(t.pg.lines))
	case k == keyUp && empty:
		moved = t.pg.move(1)
	case k == keyDown && empty:
		moved = t.pg.move(-1)
	case k == keyCtrlC:

		t.exitPagerLocked()
		t.mu.Unlock()
		t.quitSession()
		return true
	default:

		t.mu.Unlock()
		if k == keyEnter {
			t.mu.Lock()
			t.exitPagerLocked()
			t.mu.Unlock()
			t.onEnter()
			return true
		}
		t.fromPager = true
		t.onKey(k, r)
		t.fromPager = false
		return true
	}
	if moved {
		t.pg.render(t.live.w, t.theme)
	}
	t.mu.Unlock()
	return true
}

func (t *tui) enterPager() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pg != nil {
		return
	}
	t.pg = newPager(t.live.hist, t.width, t.height)
	t.pg.footer = t.footerLocked()
	t.live.suspend()
	io.WriteString(t.live.w, altOn+t.pg.frame(t.theme))
}

func (t *tui) footerLocked() []string {
	rows := t.liveLinesLocked()
	return append(rows, statusRows(t.statusLineLocked())...)
}

func (t *tui) repaintPagerLocked() {
	if t.pg == nil {
		return
	}
	t.pg.lines = t.live.hist
	t.pg.footer = t.footerLocked()
	t.pg.clamp()
	t.pg.render(t.live.w, t.theme)
}

func (t *tui) exitPagerLocked() {
	if t.pg == nil {
		return
	}
	t.pg = nil
	io.WriteString(t.live.w, altOff)
	t.live.resume()
}
