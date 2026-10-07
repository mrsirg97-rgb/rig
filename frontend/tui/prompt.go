package tui

import (
	"context"
	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"io"
	"strings"
)

func (t *tui) paintInput() {
	t.mu.Lock()
	t.inputText = t.ed.text()
	t.editPos = t.ed.pos
	status := t.statusLineLocked()
	lines, line, col := t.liveRegionLocked()
	if !t.regionStableLocked(lines, status) {
		t.live.editFull(lines, col, status)
	} else {
		t.live.edit(line, col, status)
	}
	t.mu.Unlock()
}

func (t *tui) regionStableLocked(lines []string, status string) bool {
	if t.live.paintedWidth != t.live.width {
		return false
	}
	old := t.live.lines
	if len(lines) < 1 {
		return false
	}
	oldStatusRows := statusRows(t.live.status)
	newStatusRows := statusRows(status)
	offset := len(newStatusRows)
	if len(oldStatusRows) != offset || len(old) < 1+offset {
		return false
	}
	oldIn := old[len(old)-1-offset]
	newIn := lines[len(lines)-1]
	if t.live.visualRows(oldIn) != t.live.visualRows(newIn) {
		return false
	}
	oldPart := old[:len(old)-1-offset]
	newPart := lines[:len(lines)-1]
	if len(oldPart) != len(newPart) {
		return false
	}
	for i := range oldPart {
		if oldPart[i] != newPart[i] {
			return false
		}
	}
	return true
}

func (t *tui) onEnter() {
	t.mu.Lock()

	if t.menuOpenLocked() && t.menuNavigated {
		accept := t.menuAcceptLocked()
		t.mu.Unlock()
		t.ed.setText(accept)
		t.mu.Lock()
		t.menuSyncLocked()
		t.mu.Unlock()
		t.paintInput()
		return
	}

	if !t.menuOpenLocked() {
		if next, ok := t.tabTextLocked(); ok {
			t.mu.Unlock()
			t.ed.setText(next)
			t.mu.Lock()
		}
	}
	t.mu.Unlock()
	line, submitted := t.ed.apply(keyEnter, 0)
	if !submitted || strings.TrimSpace(line) == "" {
		return
	}
	t.mu.Lock()
	t.syncSizeLocked()

	promptRows := wrapSegs(t.theme, t.width, []seg{
		{slot: SlotEmber, text: t.theme.Glyph(GlyphPrompt)},
		{slot: SlotText, text: " " + displayInput(line)},
	})
	full := strings.Join(promptRows, "\n")
	t.inputText = ""
	t.editPos = 0
	t.menuSyncLocked()
	wasLive := t.turnLive
	established := t.turnEstablished
	isCmd := t.commands != nil && command.IsCommandLine(line)
	if !wasLive && !isCmd {

		t.turnLive = true
		t.phase = "thinking"
		t.frame = 0
	}

	activity := ""
	if wasLive {
		activity = t.activityLineLocked()
	}
	t.live.enter(full, activity, t.inputLineLocked(), t.statusLineLocked())
	t.mu.Unlock()
	if isCmd {
		t.pending <- line
		return
	}
	if wasLive {

		t.mu.Lock()
		t.pend = nil
		t.pendGen++
		t.mu.Unlock()
		t.steer(line)
		return
	}
	_ = established

	t.pending <- line
}

func (t *tui) steer(line string) {
	t.mu.Lock()
	t.slot = line
	t.hasSlot = true
	t.steeredLive = true
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (t *tui) quitSession() {
	t.mu.Lock()
	t.quit = true
	if t.turnLive && t.cancel != nil {
		t.cancel()
	}
	t.mu.Unlock()
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (t *tui) onInputEOF() {
	if line, submitted := t.ed.apply(keyEnter, 0); submitted && strings.TrimSpace(line) != "" {
		t.pending <- line
	}
	t.quitSession()
	close(t.pending)
}

func (t *tui) Input(ctx context.Context) (string, error) {
	t.mu.Lock()
	if cancel, ok := core.InterruptFrom(ctx); ok {
		t.cancel = cancel
	}
	t.reading = true

	if !t.started {

		t.started = true
		committed := t.sessionStartLocked()
		t.live.draw(committed, t.liveLinesLocked(), t.statusLineLocked())
	}
	t.mu.Unlock()

	t.readerOnce.Do(func() { go t.readLoop() })
	defer func() {
		t.mu.Lock()
		t.reading = false
		t.mu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if block, ok := t.drainInbox(ctx); ok {
			return block, nil
		}
		if line, ok := t.takeSlot(); ok {
			if strings.TrimSpace(line) == "" {
				continue
			}

			t.startTurnLocked(ctx)
			return line, nil
		}

		select {
		case line, ok := <-t.pending:
			if !ok {
				return "", io.EOF
			}
			if out, delivered := t.consume(ctx, line); delivered {
				return out, nil
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case line, ok := <-t.pending:
			if !ok {
				return "", io.EOF
			}
			if out, delivered := t.consume(ctx, line); delivered {
				return out, nil
			}

		case <-t.wake:
			t.mu.Lock()
			quit := t.quit
			t.mu.Unlock()
			if quit {
				return "", io.EOF
			}
		case <-t.statusTicks:
			t.mu.Lock()
			t.statusTickLocked()
			t.mu.Unlock()
		}
	}
}

func (t *tui) consume(ctx context.Context, line string) (string, bool) {
	if strings.TrimSpace(line) == "" {
		return "", false
	}
	if t.commands != nil && command.IsCommandLine(line) {
		t.dispatch(ctx, line)
		return "", false
	}
	out := line
	if strings.HasPrefix(line, "//") {
		out = command.Unescape(line)
	}

	t.startTurnLocked(ctx)
	return out, true
}

func (t *tui) startTurnLocked(ctx context.Context) {
	t.mu.Lock()
	t.turnCtx = ctx
	t.steeredLive = false
	t.turnLive = true
	t.turnEstablished = false
	t.pend = nil
	t.pendGen++
	t.dirty = false
	t.flowChunks = nil
	t.phase = "thinking"
	t.frame = 0
	t.toolName = ""
	t.toolArgs = nil
	t.toolStarts = nil
	t.startFrameTickerLocked()
	t.live.draw("", t.liveLinesLocked(), t.statusLineLocked())
	t.mu.Unlock()
}

func (t *tui) takeSlot() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.hasSlot {
		return "", false
	}
	line := t.slot
	t.slot = ""
	t.hasSlot = false
	return line, true
}
