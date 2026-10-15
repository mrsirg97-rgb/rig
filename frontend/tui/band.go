package tui

import (
	"strconv"
	"strings"
	"time"
)

const bandCallCols = 25

type bandItem struct {
	status bool
	key    string
	slot   string
	text   string
}

type bandCall struct {
	call string
	at   time.Time
}

func bandCallCut(call string) string {
	if runeWidthSum(call) <= bandCallCols {
		return call
	}
	var b strings.Builder
	width := 0
	for _, r := range call {
		w := runeWidth(r)
		if width+w > bandCallCols-1 {
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

func (t *tui) bandStatusItem(key, prefix, call string, at time.Time) bandItem {
	return bandItem{status: true, key: key, slot: SlotWarn,
		text: prefix + bandCallCut(call) + " · " + swarmAge(t.now().Sub(at))}
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
	t.bandQueueLocked(t.bandStatusItem(key, prefix, call, at))
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
		t.stopFrameTickerLocked()
		return
	}
	t.bandHold = t.bandQueue[0]
	t.bandQueue = t.bandQueue[1:]
	t.bandLive = true
}

func (t *tui) bandRowLocked() string {
	var row string
	switch {
	case t.bandLive:
		row = t.theme.BreathPaint(t.bandHold.slot, t.bandFrame, t.bandHold.text)
	case t.bandHold.text != "":
		row = t.theme.Paint(SlotDim, t.bandHold.text)
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
