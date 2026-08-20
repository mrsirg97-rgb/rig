package tui

import "strings"

func wrapSegs(th Theme, width int, segs []seg) []string {
	if width < 1 {
		width = 1
	}

	type cell struct {
		slot string
		r    rune
	}
	var cells []cell
	for _, s := range segs {
		for _, r := range s.text {
			cells = append(cells, cell{s.slot, r})
		}
	}
	if len(cells) == 0 {
		return []string{""}
	}
	var rows []string
	emit := func(from, to int) {

		var b strings.Builder
		i := from
		for i < to {
			j := i
			for j < to && cells[j].slot == cells[i].slot {
				j++
			}
			var text strings.Builder
			for k := i; k < j; k++ {
				text.WriteRune(cells[k].r)
			}
			if cells[i].slot == "" {
				b.WriteString(text.String())
			} else {
				b.WriteString(th.Paint(cells[i].slot, text.String()))
			}
			i = j
		}
		rows = append(rows, b.String())
	}
	start := 0
	col := 0
	lastSpace := -1
	for i := 0; i < len(cells); i++ {
		w := runeWidth(cells[i].r)
		if cells[i].r == ' ' {
			lastSpace = i
		}
		if col+w > width && i > start {

			if lastSpace > start {
				emit(start, lastSpace)
				start = lastSpace + 1
			} else {
				emit(start, i)
				start = i
			}

			col = 0
			for k := start; k < i; k++ {
				col += runeWidth(cells[k].r)
			}
			lastSpace = -1
			for k := start; k < i; k++ {
				if cells[k].r == ' ' {
					lastSpace = k
				}
			}
		}
		col += w
	}
	emit(start, len(cells))
	return rows
}
