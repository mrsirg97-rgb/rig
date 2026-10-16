package tui

import (
	"strconv"
	"strings"
	"time"
)

const bandCallCols = 25

const bandRestPeriod = 30 * time.Second

type bandItem struct {
	status bool
	key    string
	slot   string
	text   string
}

type bandWork struct {
	label   string
	workers int
	since   time.Time
}

type bandCall struct {
	call string
	at   time.Time
}

func bandCallCut(call string) string {
	return bandCut(call, bandCallCols)
}

func bandCut(text string, cols int) string {
	if cols < 1 || runeWidthSum(text) <= cols {
		return text
	}
	var b strings.Builder
	width := 0
	for _, r := range text {
		w := runeWidth(r)
		if width+w > cols-1 {
			break
		}
		b.WriteRune(r)
		width += w
	}
	return b.String() + "…"
}

func bandEvent(slot, text string) bandItem {
	return bandItem{slot: slot, text: text}
}

func (t *tui) bandStatusItem(key, prefix, call string) bandItem {
	return bandItem{status: true, key: key, slot: SlotWarn, text: prefix + bandCallCut(call)}
}

func (t *tui) bandWorksLocked(key, label string, workers int, since time.Time) {
	if workers < 0 {
		if _, ok := t.bandWork[key]; !ok {
			return
		}
		delete(t.bandWork, key)
		t.stopFrameTickerLocked()
		if !t.bandLive && len(t.live.lines) > 0 {
			t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
		}
		return
	}
	if t.bandWork == nil {
		t.bandWork = map[string]bandWork{}
	}
	if w, ok := t.bandWork[key]; ok {
		since = w.since
	} else if since.IsZero() {
		since = t.now()
	}
	t.bandWork[key] = bandWork{label: label, workers: workers, since: since}
	t.startFrameTickerLocked()
}

func (t *tui) bandSummaryLocked() string {
	var latest bandWork
	found := false
	for _, w := range t.bandWork {
		if !found || w.since.After(latest.since) || (w.since.Equal(latest.since) && w.label < latest.label) {
			latest, found = w, true
		}
	}
	if !found {
		return ""
	}
	text := latest.label
	if latest.workers > 0 {
		text += " · " + strconv.Itoa(latest.workers) + " " + wordFor(latest.workers)
	}
	return text + " · " + swarmAge(t.now().Sub(latest.since))
}

func (t *tui) bandSummaryMovedLocked() bool {
	if t.bandLive {
		return false
	}
	text := t.bandSummaryLocked()
	if text == t.bandShown {
		return false
	}
	t.bandShown = text
	return true
}

func (t *tui) bandWorkerCallLocked(key, prefix, call string, at time.Time) {
	if call == "" {
		return
	}
	seen := bandCall{call, at}
	if t.bandCalls[key] == seen {
		return
	}
	if t.bandCalls == nil {
		t.bandCalls = map[string]bandCall{}
	}
	t.bandCalls[key] = seen
	t.bandQueueLocked(t.bandStatusItem(key, prefix, call))
}

func (t *tui) bandQueueLocked(item bandItem) {
	if item.status {
		for i, q := range t.bandQueue {
			if q.status && q.key == item.key {
				t.bandQueue[i] = item
				t.bandKickLocked()
				return
			}
		}
	} else {
		if t.bandLive && !t.bandHold.status &&
			t.bandHold.slot == item.slot && t.bandHold.text == item.text {
			return
		}
		for _, q := range t.bandQueue {
			if q.slot == item.slot && q.text == item.text {
				return
			}
		}
	}
	t.bandQueue = append(t.bandQueue, item)
	t.bandKickLocked()
}

func (t *tui) bandKickLocked() {
	if t.bandLive || len(t.bandQueue) == 0 {
		return
	}
	t.bandHold = t.bandQueue[0]
	t.bandQueue = t.bandQueue[1:]
	t.bandFrame = 0
	t.bandLive = true
	t.bandWakeLocked()
	t.startFrameTickerLocked()
	if len(t.live.lines) > 0 {
		t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
	}
}

func (t *tui) breatheBandLocked() {
	if !t.bandLive {
		return
	}
	t.bandFrame++
	if t.bandFrame < emberBreathStops {
		return
	}
	t.bandFrame = 0
	t.bandLive = false
	if len(t.bandQueue) == 0 {
		t.bandRestLocked()
		t.stopFrameTickerLocked()
		return
	}
	t.bandHold = t.bandQueue[0]
	t.bandQueue = t.bandQueue[1:]
	t.bandLive = true
}

func (t *tui) bandRestLocked() {
	t.bandWakeLocked()
	t.bandRestUntil = t.now().Add(bandRestPeriod)
	t.bandRest = time.AfterFunc(bandRestPeriod, t.bandFade)
}

func (t *tui) bandWakeLocked() {
	if t.bandRest != nil {
		t.bandRest.Stop()
		t.bandRest = nil
	}
	t.bandRestUntil = time.Time{}
}

func (t *tui) bandFade() {
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case <-t.closed:
		return
	default:
	}
	t.bandFadeLocked()
}

func (t *tui) bandFadeLocked() {
	if t.bandLive || t.bandRestUntil.IsZero() || t.now().Before(t.bandRestUntil) {
		return
	}
	t.bandWakeLocked()
	t.bandHold = bandItem{}
	if len(t.live.lines) > 0 {
		t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
	}
}

func (t *tui) bandRowLocked() string {
	text := bandCut(t.bandHold.text, t.live.width)
	var row string
	t.bandShown = t.bandSummaryLocked()
	summary := bandCut(t.bandShown, t.live.width)
	switch {
	case t.bandLive:
		row = t.theme.BreathPaint(t.bandHold.slot, t.bandFrame, text)
	case summary != "":
		row = t.theme.Paint(SlotDim, summary)
	case text != "":
		row = t.theme.Paint(SlotDim, text)
	default:
		row = t.theme.Paint(SlotDim, "—")
	}
	return swarmRule(t.theme) + "\n" + row
}

func bandWorkerKey(delegate bool, id int) string {
	if delegate {
		return "delegate/" + strconv.Itoa(id)
	}
	return "swarm/" + strconv.Itoa(id)
}
