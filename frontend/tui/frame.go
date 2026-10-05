package tui

import (
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

const (
	framePeriod = 16 * time.Millisecond
	animPeriod  = 120 * time.Millisecond
)

func (t *tui) startFrameTickerLocked() {
	if !(t.turnLive || t.compacting || t.noticing || t.aside != "") || t.tickStop != nil {
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
	if t.turnLive || t.compacting || t.noticing || t.aside != "" || t.tickStop == nil {
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
			live := (t.turnLive || t.compacting || t.noticing || t.aside != "") && len(t.live.lines) > 0
			if live && now.Sub(lastAnim) >= animPeriod {
				lastAnim = now
				t.frame++
				t.breatheNoticeLocked()
				dirty = true
			}
			if live && dirty {
				t.paintLiveLocked()
			}
			t.mu.Unlock()
		}
	}
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

func (t *tui) breatheNoticeLocked() {
	if !t.noticing || t.turnLive || t.compacting || t.aside != "" {
		return
	}
	t.noticeFrame++
	if t.noticeFrame < emberBreathStops {
		return
	}
	t.notices = t.notices[1:]
	t.noticeFrame = 0
	if len(t.notices) == 0 {
		t.noticing = false
		t.stopFrameTickerLocked()
	}
}

func (t *tui) enqueueNoticeLocked(n core.Notice) {
	for _, q := range t.notices {
		if q == n {
			return
		}
	}
	t.notices = append(t.notices, n)
	t.kickNoticesLocked()
}

func (t *tui) kickNoticesLocked() {
	if t.noticing || len(t.notices) == 0 || t.turnLive || t.compacting || t.aside != "" {
		return
	}
	t.noticing = true
	t.noticeFrame = 0
	t.startFrameTickerLocked()
	if len(t.live.lines) > 0 {
		t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
	}
}

func noticeSlot(l core.Level) string {
	switch l {
	case core.LevelError:
		return SlotError
	case core.LevelSuccess:
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
	t.kickNoticesLocked()
}

func (t *tui) phaseFlows() bool {
	return !t.turnLive || t.compacting || t.phase == "summarizing"
}
