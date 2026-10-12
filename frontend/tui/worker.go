package tui

import (
	"context"
	"strconv"
	"strings"

	"github.com/mrsirg97-rgb/rig/v3/core"
)

func (t *tui) drainInbox(ctx context.Context) (string, bool) {
	t.mu.Lock()
	if len(t.inbox) == 0 {
		t.mu.Unlock()
		return "", false
	}
	inbox := t.inbox
	t.inbox = nil
	t.syncSizeLocked()
	chunk := ""
	for _, done := range inbox {
		if chunk != "" {
			chunk += "\n"
		}
		chunk += RenderReturnBlock(t.theme, t.width, done)
	}
	if len(t.flowChunks) > 0 {
		chunk = strings.Join(t.flowChunks, "") + chunk
		t.flowChunks = nil
	}
	t.live.draw(chunk, t.liveLinesLocked(), t.statusLineLocked())
	promptRows := wrapSegs(t.theme, t.width, []seg{
		{slot: SlotEmber, text: t.theme.Glyph(GlyphPrompt)},
		{slot: SlotText, text: " " + returnLine(inbox)},
	})
	full := strings.Join(promptRows, "\n")
	activity := ""
	if t.turnLive {
		activity = t.activityLineLocked()
	}
	t.live.enter(full, activity, t.inputLineLocked(), t.statusLineLocked())
	t.mu.Unlock()
	t.startTurnLocked(ctx)
	return core.WorkerBlock(inbox), true
}

func (t *tui) Returns() (string, bool) {
	t.mu.Lock()
	if len(t.inbox) == 0 {
		t.mu.Unlock()
		return "", false
	}
	inbox := t.inbox
	t.inbox = nil
	gap := t.lastSlot == slotAfterTool
	t.lastSlot = slotAfterTool
	chunk := ""
	for _, done := range inbox {
		chunk += RenderReturnBlock(t.theme, t.width, done) + "\n"
	}
	chunk += strings.Join(wrapSegs(t.theme, t.width, []seg{
		{slot: SlotEmber, text: t.theme.Glyph(GlyphPrompt)},
		{slot: SlotText, text: " " + returnLine(inbox)},
	}), "\n")
	t.mu.Unlock()
	if gap {
		t.flow("", "\n")
	}
	t.commit(chunk)
	return core.WorkerBlock(inbox), true
}

func returnLine(inbox []core.WorkerDone) string {
	ids := make([]string, 0, len(inbox))
	for _, done := range inbox {
		ids = append(ids, "#"+strconv.Itoa(done.N))
	}
	return "delegate " + strings.Join(ids, ", ") + " returned"
}
