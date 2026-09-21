package tui

type pendWrap struct {
	gen   int
	width int
	done  int
	last  seg
	rows  []string
	carry []cell
	col   int
}

func (p pendWrap) holds(pend []seg, w, gen int) bool {
	if p.gen != gen || p.width != w || p.done > len(pend) {
		return false
	}
	return p.done == 0 || pend[p.done-1] == p.last
}

func (t *tui) pendRowsLocked() []string {
	w := t.live.width
	if w < 1 {
		w = 1
	}
	pw := &t.pw
	if !pw.holds(t.pend, w, t.pendGen) {
		rows, carry, col := wrapCells(t.theme, flattenSegs(nil, t.pend), w, 0)
		*pw = pendWrap{gen: t.pendGen, width: w, done: len(t.pend), rows: rows, carry: carry, col: col}
		if len(t.pend) > 0 {
			pw.last = t.pend[len(t.pend)-1]
		}
		return pw.rows
	}
	if pw.done < len(t.pend) {
		rows, carry, col := wrapCells(t.theme, flattenSegs(pw.carry, t.pend[pw.done:]), w, pw.col)
		pw.rows = append(pw.rows[:len(pw.rows)-1], rows...)
		pw.carry, pw.col, pw.done = carry, col, len(t.pend)
		pw.last = t.pend[len(t.pend)-1]
	}
	return pw.rows
}
