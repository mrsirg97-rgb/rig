package tui

import (
	"strconv"
	"strings"
)

const phasePreviewRows = 10

func (t *tui) resetPhaseLocked() {
	t.phaseLines = nil
	t.phaseOpen = ""
	t.phaseHidden = 0
}

func (t *tui) appendPhaseLocked(text string) {
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			t.phaseOpen += text
			break
		}
		t.phaseLines = append(t.phaseLines, t.phaseOpen+text[:i])
		t.phaseOpen = ""
		text = text[i+1:]
	}
	t.trimPhaseLocked()
}

func (t *tui) trimPhaseLocked() {
	if len(t.phaseLines) <= phasePreviewRows {
		return
	}
	cut := len(t.phaseLines) - phasePreviewRows
	for _, l := range t.phaseLines[:cut] {
		t.phaseHidden += len(screenRows([]string{expandTabs(l)}, t.live.width))
	}
	t.phaseLines = append(t.phaseLines[:0], t.phaseLines[cut:]...)
}

func (t *tui) phasePreviewLocked(cap int) ([]string, int) {
	if cap <= 0 || !t.phaseFlows() {
		return nil, 0
	}
	if len(t.phaseLines) == 0 && t.phaseOpen == "" {
		return nil, 0
	}
	lines := make([]string, 0, len(t.phaseLines)+1)
	for _, l := range t.phaseLines {
		lines = append(lines, expandTabs(l))
	}
	if t.phaseOpen != "" {
		lines = append(lines, expandTabs(t.phaseOpen))
	}
	rows := screenRows(lines, t.live.width)
	if len(rows) == 0 {
		return nil, 0
	}
	if t.phaseHidden == 0 && len(rows) <= phasePreviewRows && len(rows) <= cap {
		return t.paintPreviewRows(rows), len(rows)
	}
	keep := phasePreviewRows
	if keep > cap-1 {
		keep = cap - 1
	}
	if keep < 1 {
		keep = 1
	}
	if keep > len(rows) {
		keep = len(rows)
	}
	hidden := t.phaseHidden + len(rows) - keep
	tail := t.paintPreviewRows(rows[len(rows)-keep:])
	if cap < 2 {
		return tail, 1
	}
	header := t.theme.Paint(SlotDim, "\u00b7 "+strconv.Itoa(hidden)+" rows above \u00b7")
	return append([]string{header}, tail...), keep + 1
}

func (t *tui) paintPreviewRows(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = t.theme.Paint(SlotReasoning, r)
	}
	return out
}
