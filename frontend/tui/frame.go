package tui

import (
	"time"

	"github.com/mrsirg97-rgb/evt"
)

const (
	framePeriod = 16 * time.Millisecond
	animPeriod  = 120 * time.Millisecond
)

func (t *tui) startFrameTickerLocked() {
	if !t.busyLocked() || t.tickStop != nil {
		return
	}
	if t.ticker == nil && t.ticks == nil {
		t.ticker = time.NewTicker(framePeriod)
		t.ticks = t.ticker.C
	}
	t.tickStop = make(chan struct{})
	go t.tickLoop()
}

func (t *tui) stopFrameTickerLocked() {
	if t.busyLocked() || t.tickStop == nil {
		return
	}
	close(t.tickStop)
	t.tickStop = nil
	if t.ticker != nil {
		t.ticker.Stop()
		t.ticker = nil
		t.ticks = nil
	}
}

func (t *tui) tickLoop() {
	t.mu.Lock()
	ticks := t.ticks
	stop := t.tickStop
	t.mu.Unlock()
	if ticks == nil || stop == nil {
		return
	}
	lastAnim := time.Time{}
	for {
		select {
		case <-t.closed:
			return
		case <-stop:
			return
		case now := <-ticks:
			t.mu.Lock()
			dirty := t.dirty
			t.dirty = false
			live := t.busyLocked() && len(t.live.lines) > 0
			expired := false
			if t.escArm > 0 && !now.Before(t.escAt) {
				t.escArm = 0
				t.escAt = time.Time{}
				dirty = true
				expired = true
			}
			if live && now.Sub(lastAnim) >= animPeriod {
				lastAnim = now
				t.frame++
				animating := t.turnLive || t.compacting || t.bandLive || t.aside != "" || t.escArm > 0
				t.breatheBandLocked()
				if animating || t.bandSummaryMovedLocked() {
					dirty = true
				}
			}
			if live && dirty {
				t.paintLiveLocked()
			}
			if expired {
				t.stopFrameTickerLocked()
			}
			t.mu.Unlock()
		}
	}
}

func (t *tui) busyLocked() bool {
	return t.turnLive || t.compacting || t.bandLive || t.aside != "" || t.escArm > 0 || len(t.bandWork) > 0
}

func (t *tui) winchLoop() {
	for {
		select {
		case <-t.closed:
			return
		case <-t.winch:
			t.mu.Lock()
			t.syncSizeLocked()
			if t.pg != nil {
				t.pg.width, t.pg.height = t.width, t.height
				t.pg.clamp()
				t.pg.render(t.live.w, t.theme)
			} else if len(t.live.lines) > 0 {
				t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
			}
			t.mu.Unlock()
		}
	}
}

func noticeSlot(l evt.Level) string {
	switch l {
	case evt.LevelError:
		return SlotError
	case evt.LevelSuccess:
		return SlotSuccess
	}
	return SlotText
}

func (t *tui) beginPhaseLocked(name string) {
	t.aside = name
	t.resetPhaseLocked()
	t.asideAt = time.Now()
	t.frame = 0
	t.startFrameTickerLocked()
	if len(t.live.lines) > 0 {
		t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
	}
}

func (t *tui) endPhaseLocked() {
	t.aside = ""
	t.resetPhaseLocked()
	t.stopFrameTickerLocked()
}

func (t *tui) phaseFlows() bool {
	return !t.turnLive || t.compacting || t.phase == "summarizing"
}
