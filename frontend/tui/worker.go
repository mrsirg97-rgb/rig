package tui

import (
	"context"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

func (t *tui) drainInbox(ctx context.Context) (string, bool) {
	t.mu.Lock()
	if len(t.inbox) == 0 {
		t.mu.Unlock()
		return "", false
	}
	block := core.WorkerBlock(t.inbox)
	t.inbox = nil
	t.syncSizeLocked()
	promptRows := wrapSegs(t.theme, t.width, []seg{
		{slot: SlotEmber, text: t.theme.Glyph(GlyphPrompt)},
		{slot: SlotText, text: " " + displayInput(block)},
	})
	full := strings.Join(promptRows, "\n")
	activity := ""
	if t.turnLive {
		activity = t.activityLineLocked()
	}
	t.live.enter(full, activity, t.inputLineLocked(), t.statusLineLocked())
	t.mu.Unlock()
	t.startTurnLocked(ctx)
	return block, true
}
