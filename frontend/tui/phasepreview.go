package tui

import (
	"strconv"
	"strings"
)

const phasePreviewRows = 10

func (t *tui) phasePreviewLocked(cap int) ([]string, int) {
	if cap <= 0 || t.phaseText == "" || !t.phaseFlows() {
		return nil, 0
	}
	lines := strings.Split(expandTabs(t.phaseText), "\n")
	if n := len(lines) - 1; n > 0 && lines[n] == "" {
		lines = lines[:n]
	}
	rows := screenRows(lines, t.live.width)
	if len(rows) == 0 {
		return nil, 0
	}
	if len(rows) <= phasePreviewRows && len(rows) <= cap {
		return t.paintPreviewRows(rows), len(rows)
	}
	keep := phasePreviewRows
	if keep > cap-1 {
		keep = cap - 1
	}
	if keep < 1 {
		keep = 1
	}
	hidden := len(rows) - keep
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
