package tui

import "strings"

// wrapSegs soft-wraps one closed prose line at word boundaries
// (decision 2, amended): the terminal breaks a wide line mid-word at
// the column edge; prose reads better broken at spaces. The segments'
// paint is kept per piece; a word wider than the width breaks at the
// edge (the terminal's rule, unavoidable). Preformatted content (tool
// output, fenced code) never comes here. The result is the terminal
// rows the line will occupy, each at most width columns; an empty
// line is one empty row.
func wrapSegs(th Theme, width int, segs []seg) []string {
	if width < 1 {
		width = 1
	}
	// the line as painted runs: (slot, rune) pairs, so a break keeps
	// each piece's paint.
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
		// paint the run [from,to) by slot, adjacent same-slot cells
		// joined into one paint.
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
	start := 0 // the row's first cell
	col := 0   // columns used on the row
	lastSpace := -1
	for i := 0; i < len(cells); i++ {
		w := runeWidth(cells[i].r)
		if cells[i].r == ' ' {
			lastSpace = i
		}
		if col+w > width && i > start {
			// the row is full: break at the last space if there is
			// one past the row's start, else at the edge.
			if lastSpace > start {
				emit(start, lastSpace)
				start = lastSpace + 1 // the space is the break, dropped
			} else {
				emit(start, i)
				start = i
			}
			// recount the columns of the new row's head
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
