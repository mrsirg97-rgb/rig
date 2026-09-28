package tui

import (
	"time"
)

const (
	framePeriod = 16 * time.Millisecond
	animPeriod  = 120 * time.Millisecond
)

func (t *tui) startFrameTickerLocked() {
	if !(t.turnLive || t.compacting) || t.tickStop != nil {
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
	if t.turnLive || t.compacting || t.tickStop == nil {
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
			live := (t.turnLive || t.compacting) && len(t.live.lines) > 0
			if live && now.Sub(lastAnim) >= animPeriod {
				lastAnim = now
				t.frame++
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
