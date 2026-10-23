package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
)

func (t *tui) Notify(ev evt.Signal) {
	switch ev.(type) {
	case core.ReasoningDelta, core.TextDelta, core.ToolStart, core.ToolResult,
		core.Done, core.Fault, core.Compacted, core.EmptyTurn, core.TurnEnd:

		t.mu.Lock()
		t.mu.Unlock()
	}
	switch e := ev.(type) {
	case core.ReasoningDelta:
		if e.Text == "" {
			return
		}
		t.mu.Lock()
		visible := t.showReasoning
		t.mu.Unlock()

		if visible {
			t.flow(SlotReasoning, e.Text)
		}
	case core.TextDelta:
		if e.Text == "" {
			return
		}
		t.flow(SlotText, e.Text)
	case core.ToolStart:
		t.mu.Lock()
		t.toolName = e.Call.Name
		t.toolArgs = e.Call.Args
		if t.toolStarts == nil {
			t.toolStarts = map[string]startInfo{}
		}
		t.toolStarts[e.Call.ID] = startInfo{name: e.Call.Name, args: e.Call.Args}
		t.phase = e.Call.Name
		gap := t.lastSlot == SlotText || t.lastSlot == SlotReasoning
		t.mu.Unlock()

		if gap {
			t.flow(SlotText, "\n")
		}
	case core.ToolResult:
		t.mu.Lock()
		name, args := t.toolName, t.toolArgs
		if si, ok := t.toolStarts[e.ID]; ok {
			name, args = si.name, si.args
			delete(t.toolStarts, e.ID)
		}
		gap := t.lastSlot == slotAfterTool
		block := RenderToolBlock(t.theme, t.width, name, args, e.Content, e.Err != nil, e.Duration)
		t.phase = "thinking"
		t.toolName = ""
		t.toolArgs = nil
		t.lastSlot = slotAfterTool
		t.mu.Unlock()
		if gap {
			t.flow("", "\n")
		}
		t.commit(block)
	case core.Done:

		t.mu.Lock()
		t.prompt += e.Usage.Prompt
		t.completion += e.Usage.Completion
		t.cacheRead += e.Usage.CacheRead
		t.cost += e.Usage.Cost
		if e.Usage.Prompt > 0 || e.Usage.Completion > 0 {
			t.statusUsed = e.Usage.Prompt + e.Usage.Completion
			t.statusHasUsed = true
		}

		t.statusUp, t.statusDown, t.statusCache, t.statusCost = t.prompt, t.completion, t.cacheRead, t.cost
		t.mu.Unlock()
		t.flow("", "\n")
	case core.EmptyTurn:

		t.mu.Lock()
		t.prompt += e.Usage.Prompt
		t.completion += e.Usage.Completion
		t.cacheRead += e.Usage.CacheRead
		t.cost += e.Usage.Cost
		t.mu.Unlock()
		t.flow("", "\n")
		t.commit(RenderEmptyTurn(t.theme, e))
	case core.Fault:

		t.mu.Lock()
		t.compacting = false
		t.endPhaseLocked()
		fault := RenderFault(t.theme, e.Err)
		t.mu.Unlock()
		t.flow("", "\n")
		t.commit(fault)
		t.mu.Lock()
		t.stopFrameTickerLocked()
		t.mu.Unlock()
	case core.Compacting:

		t.mu.Lock()
		t.phase = "summarizing"
		t.resetPhaseLocked()
		t.asideAt = time.Now()
		t.frame = 0
		if !t.turnLive {
			t.compacting = true
			t.startFrameTickerLocked()
		}

		if len(t.live.lines) > 0 {
			t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
		}
		t.mu.Unlock()
	case core.Compacted:

		t.mu.Lock()
		t.compacting = false
		t.endPhaseLocked()
		if t.turnLive {
			t.phase = "thinking"
		}
		chunk := RenderCompacted(t.theme, e) + "\n"
		if e.Kept > 0 {
			t.statusUsed = e.Kept
			t.statusHasUsed = true
		}
		pending := len(t.pend) > 0
		t.mu.Unlock()
		if pending {
			t.flow("", "\n")
		}
		t.commit(chunk)
		t.mu.Lock()
		t.stopFrameTickerLocked()
		t.mu.Unlock()
	case core.TurnEnd:

		t.mu.Lock()
		t.lastSlot = ""
		t.pendCol = 0
		t.codeMode = false
		pending := len(t.pend) > 0
		t.mu.Unlock()
		if pending {
			t.flow("", "\n")
		}
		t.mu.Lock()

		t.statusUp, t.statusDown, t.statusCache, t.statusCost = t.prompt, t.completion, t.cacheRead, t.cost
		t.prompt, t.completion, t.cacheRead, t.cost = 0, 0, 0, 0
		t.turnLive = false
		t.phase = "thinking"
		t.frame = 0
		t.pend = nil
		t.pendGen++
		t.toolName = ""
		t.toolArgs = nil
		t.toolStarts = nil
		t.mu.Unlock()
		t.commit("")
		t.mu.Lock()
		lastBlank := t.live.lastBlank
		t.mu.Unlock()
		if !lastBlank {
			t.commit("\n")
		}
		t.mu.Lock()
		t.stopFrameTickerLocked()
		t.mu.Unlock()
	case evt.Notice:
		t.mu.Lock()
		t.bandQueueLocked(bandEvent(noticeSlot(e.Level), e.Source+": "+printable(e.Text)))
		t.mu.Unlock()
	case machineroom.JobStatus:
		t.mu.Lock()
		t.jobLocked(e)
		t.mu.Unlock()
	case core.Phase:
		switch {
		case e.Done:
			t.mu.Lock()
			t.endPhaseLocked()
			t.mu.Unlock()
			t.flow("", "\n")
			t.commit(RenderPhaseEnd(t.theme, e) + "\n")
		case e.Text != "":
			t.mu.Lock()
			t.syncSizeLocked()
			if t.showReasoning && t.phaseFlows() {
				t.appendPhaseLocked(e.Text)
				t.dirty = true
			}
			t.mu.Unlock()
		default:
			t.mu.Lock()
			if !t.compacting {
				t.beginPhaseLocked(e.Name)
			}
			t.mu.Unlock()
		}
	case core.SwarmStatus:
		t.mu.Lock()
		t.trackStatusLocked(e)
		t.mu.Unlock()
	case core.WorkerDone:
		t.mu.Lock()
		t.inbox = append(t.inbox, e)
		t.mu.Unlock()
		select {
		case t.wake <- struct{}{}:
		default:
		}
	default:

	}
}

func (t *tui) commit(chunk string) {
	t.mu.Lock()
	if len(t.flowChunks) > 0 {
		chunk = strings.Join(t.flowChunks, "") + chunk
		t.flowChunks = nil
	}
	t.live.draw(chunk, t.liveLinesLocked(), t.statusLineLocked())
	t.mu.Unlock()
}

func (t *tui) paintLiveLocked() {
	chunk := ""
	if len(t.flowChunks) > 0 {
		chunk = strings.Join(t.flowChunks, "")
		t.flowChunks = nil
	}
	t.live.draw(chunk, t.liveLinesLocked(), t.statusLineLocked())
}

const menuMaxRows = 8

func (t *tui) liveLinesLocked() []string {
	lines, _, _ := t.liveRegionLocked()
	return lines
}

func (t *tui) liveRegionLocked() ([]string, string, int) {
	t.syncSizeLocked()

	pendCap, menuCap, inputCap := 1<<30, menuMaxRows, maxInputRows
	previewCap := phasePreviewRows + 1
	h := t.live.height
	if h >= 1 {
		pendCap = h
	}
	giveUp := false
	var lines []string
	var line string
	var col int
	var blocks liveBlocks
	for i := 0; i < 6 && !giveUp; i++ {
		lines, line, col, blocks = t.buildLiveLinesLocked(pendCap, menuCap, inputCap, previewCap)
		over := t.live.rowsOver(lines[blocks.pendRows:], t.statusViewportRowsLocked()) + blocks.pendRows
		if h <= 0 || over <= 0 {
			break
		}
		switch {
		case blocks.previewRows > 0 && previewCap > 0:
			if previewCap = blocks.previewRows - over; previewCap < 0 {
				previewCap = 0
			}
		case blocks.pendRows > 0 && pendCap > 0:
			if pendCap = blocks.pendRows - over; pendCap < 0 {
				pendCap = 0
			}
		case blocks.menuRows > 0 && menuCap > 0:
			if menuCap = blocks.menuRows - over; menuCap < 0 {
				menuCap = 0
			}
		case inputCap > 1:
			if inputCap = blocks.inputRows - over; inputCap < 1 {
				inputCap = 1
			}
		default:
			giveUp = true
		}
	}
	return lines, line, col
}

type liveBlocks struct {
	pendRows    int
	previewRows int
	menuRows    int
	inputRows   int
}

func (t *tui) buildLiveLinesLocked(pendCap, menuCap, inputCap, previewCap int) ([]string, string, int, liveBlocks) {
	var blocks liveBlocks
	var lines []string
	if t.turnLive || t.compacting || t.aside != "" || t.escArm > 0 {

		if pl, rows := t.pendingBlockLocked(pendCap); rows > 0 {
			blocks.pendRows = rows
			lines = append(lines, pl...)
			lines = append(lines, "")
		} else if !t.live.lastBlank {

			lines = append(lines, "")
		}
		lines = append(lines, t.activityLineLocked())
		if pl, rows := t.phasePreviewLocked(previewCap); rows > 0 {
			blocks.previewRows = rows
			lines = append(lines, pl...)
		}
	}

	if len(lines) > 0 || !t.live.lastBlank {
		lines = append(lines, "")
	}
	if t.askReply != nil {

		lines = append(lines, t.askLineLocked())
	} else if ml := t.menuLinesLocked(menuCap); len(ml) > 0 {
		blocks.menuRows = len(ml)
		lines = append(lines, ml...)
	}
	in, col := t.inputLineAndColLocked(inputCap)
	blocks.inputRows = t.live.visualRows(in)
	return append(lines, in), in, col, blocks
}

func (t *tui) pendingBlockLocked(cap int) ([]string, int) {
	if cap <= 0 || len(t.pend) == 0 {
		return nil, 0
	}
	rows := t.pendRowsLocked()
	if len(rows) == 1 && rows[0] == "" {
		return nil, 0
	}
	if len(rows) <= cap {
		return rows, len(rows)
	}
	tailRows := cap - 1
	if tailRows < 1 {
		tailRows = 1
	}
	hidden := len(rows) - tailRows
	tail := rows[len(rows)-tailRows:]
	if cap >= 2 {
		return append([]string{
			t.theme.Paint(SlotDim, "· "+strconv.Itoa(hidden)+" lines hidden ·"),
		}, tail...), tailRows + 1
	}
	return tail, tailRows
}

func (t *tui) statusViewportRowsLocked() int {
	n := 0
	for _, sr := range statusRows(t.statusLineLocked()) {
		n += t.live.visualRows(sr)
	}
	return n
}

func (t *tui) askLineLocked() string {
	return t.theme.Paint(SlotWarn, "approve "+printable(t.askText)+"?") +
		t.theme.Paint(SlotDim, "  [y run · n decline · esc interrupts]")
}

func (t *tui) statusLineLocked() string {
	st := RenderStatusLine(t.theme, t.statusModel, t.statusEffort, t.statusRole, t.statusApprove, t.statusUsed, t.statusWindow, t.statusHasUsed,
		t.statusUp, t.statusDown, t.statusCache, t.statusCost)
	if st == "" {
		return ""
	}
	s := "\n" + st
	if rows := RenderStatusRows(t.theme, t.statusRows); rows != "" {
		s += "\n" + rows
	}
	return s + "\n" + t.bandRowLocked()
}

func (t *tui) trackStatusLocked(st core.SwarmStatus) {
	ws := make([]core.SwarmWorker, len(st.Workers))
	for i, w := range st.Workers {
		w.Task, w.Tool = printable(w.Task), printable(w.Tool)
		ws[i] = w
	}
	st.Workers = ws
	delegate := IsDelegateBand(st)
	before := t.swarm
	if delegate {
		before = t.delegate
	}
	if len(st.Workers) == 0 {
		t.swarm, t.delegate = st, st
		t.bandWorksLocked("delegating", "", -1, time.Time{})
		t.bandWorksLocked("swarm", "", -1, time.Time{})
		return
	}
	label := "swarm"
	if delegate {
		label = "delegating"
	}
	running, active := 0, 0
	for _, w := range st.Workers {
		switch w.State {
		case "running":
			running++
			active++
		case "queued":
			active++
		}
	}
	if running > 0 && !swarmAnyRunning(before.Workers) {
		t.bandQueueLocked(bandEvent(SlotText, label+" · "+strconv.Itoa(running)+" "+wordFor(running)))
	}
	if active > 0 {
		t.bandWorksLocked(label, label, running, time.Time{})
	} else {
		t.bandWorksLocked(label, "", -1, time.Time{})
	}
	if delegate {
		t.delegate = st
	} else {
		t.swarm = st
	}
	for _, w := range st.Workers {
		if w.Tool == "" {
			continue
		}
		t.bandWorkerCallLocked(bandWorkerKey(delegate, w.ID), "#"+strconv.Itoa(w.ID)+" ", w.Tool, w.ToolAt)
	}
}

func (t *tui) jobLocked(st machineroom.JobStatus) {
	switch st.State() {
	case machineroom.JobRunning:
		t.bandWorksLocked("job/"+st.Job(), "job "+st.Job(), st.Workers(), st.Started())
		if st.Call() == "" {
			text := "scheduled job"
			if st.Workers() > 0 {
				text += " · " + strconv.Itoa(st.Workers()) + " " + wordFor(st.Workers())
			}
			t.bandQueueLocked(bandEvent(SlotText, text))
			return
		}
		t.bandWorkerCallLocked("job/"+st.Job(), st.Job()+" #1 ", st.Call(), st.CallAt())
	case machineroom.JobDone:
		t.bandWorksLocked("job/"+st.Job(), "", -1, time.Time{})
		text := "job " + st.Job() + " done · " + swarmAge(st.Ended().Sub(st.Started()))
		if st.Summary() != "" {
			text += " · " + st.Summary()
		}
		t.bandQueueLocked(bandEvent(SlotSuccess, text))
	case machineroom.JobFailed:
		t.bandWorksLocked("job/"+st.Job(), "", -1, time.Time{})
		text := "job " + st.Job() + " failed"
		if st.Reason() != "" {
			text += " · " + st.Reason()
		}
		t.bandQueueLocked(bandEvent(SlotError, text))
	}
}

func (t *tui) recaptureStatusLocked(in StatusIn, fresh bool) {
	t.statusModel = in.Model
	t.statusEffort = in.Effort
	t.statusRole = in.Role
	t.statusApprove = in.Approve
	t.statusWindow = in.Window
	if fresh {
		t.statusUsed = 0
		t.statusHasUsed = false
	}
	t.statusUp, t.statusDown, t.statusCache, t.statusCost = in.Up, in.Down, in.CacheRead, in.Cost
	t.statusRows = in.Rows
}

func (t *tui) statusTickLocked() {
	if t.turnLive || t.compacting || t.statusIn == nil {
		return
	}
	old := t.statusLineLocked()
	t.recaptureStatusLocked(t.statusIn(context.Background()), false)
	if t.statusLineLocked() != old {
		t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
	}
}

func (t *tui) sessionStartLocked() string {
	var b strings.Builder
	if t.statusIn != nil {
		in := t.statusIn(context.Background())
		t.recaptureStatusLocked(in, true)
		b.WriteString(renderStatus(t.theme, in, t.titleName, t.titleRows, t.titleTagline))
	}
	return b.String()
}
