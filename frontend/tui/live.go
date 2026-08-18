package tui

import (
	"io"
	"strings"
)

// live is the live-region protocol (decisions 1-2): the whole escape
// stream of the TUI, and its only cursor arithmetic. The bookkeeping
// lines are the rows the protocol owns — it rewrites them on the next
// op — in top-to-bottom order, and the terminal cursor is always on
// the last one. Everything above is committed and is never touched
// again (decision 1's immutability).
//
// The bookkeeping lines are logical lines; the terminal wraps a wide
// one across as many terminal rows as it needs (the pending prose
// line, decision 2's "the terminal wraps, rig never hand-wraps"). The
// cursor arithmetic therefore counts terminal rows (visualRows), not
// bookkeeping lines: an op moves to the top of the old region by its
// true height, clears the old rows, and rewrites from the top — a
// shorter new region never leaves an old tail, and a wrapped old row
// never bleeds through a shorter new line.
//
// The ops are:
//
//	draw            the event path: a committed chunk (a delta's closed
//	                lines, a tool block, the usage line, the compact
//	                line plus the banner reprint) flows over the old
//	                region, and the live region is re-emitted below it;
//	enter           the Enter path: the input row is frozen as
//	                scrollback (its full text — a shrunken width never
//	                hides it) and the new input row is written below,
//	                the fresh activity row between them when this line
//	                starts a turn;
//	insertActivity  the turn start: the activity row is put above the
//	                current input row (the line's delivery);
//	setActivity     the spinner tick and the phase change: the activity
//	                row rewritten in place;
//	edit            the typing path: the input row rewritten in place,
//	                the terminal cursor parked at the edit column.
//
// The caller serializes (the Frontend's mutex); the ops never block on
// the writer beyond the write itself.
type live struct {
	w      io.Writer
	lines  []string // the bookkeeping lines (painted, the rows we own)
	width  int      // the terminal's width: the wrap model's constant
	parked int      // rows the cursor sits above the region's last row (edit's mid-line park)

	// the committed history (the pager's document): every line that
	// crossed into scrollback, painted, ring-capped. The terminal owns
	// the real scrollback (decision 1); this is the copy the pager
	// shows where the emulator gives the operator no way up.
	hist []string

	// the suspension (the pager's screen): ops keep their bookkeeping
	// (l.lines stays true) but write nothing — wf is the single gate —
	// and committed lines queue in pend. resume replays them.
	suspended    bool
	pend         []string
	frozen       []string // the rows physically on screen at suspend
	frozenParked int
}

// histCap bounds the pager's document (the oldest lines drop).
const histCap = 5000

// record captures committed lines: the history always, the pend queue
// while suspended (they have not reached the screen).
func (l *live) record(lines []string) {
	l.hist = append(l.hist, lines...)
	if len(l.hist) > histCap {
		l.hist = append([]string(nil), l.hist[len(l.hist)-histCap:]...)
	}
	if l.suspended {
		l.pend = append(l.pend, lines...)
	}
}

// suspend freezes the screen for the pager: the rows on it are
// remembered, and every op from here is bookkeeping-only.
func (l *live) suspend() {
	l.frozen = append([]string(nil), l.lines...)
	l.frozenParked = l.parked
	l.suspended = true
}

// resume puts the main screen back: the frozen rows are what the
// terminal still shows, so the redraw clears them, commits what queued
// during the suspension, and re-emits the current region.
func (l *live) resume() {
	l.suspended = false
	cur := l.lines
	l.lines = l.frozen
	l.parked = l.frozenParked
	l.frozen = nil
	pend := l.pend
	l.pend = nil
	// draw's body without its record: the pend lines were recorded when
	// they queued.
	all := append(append([]string(nil), pend...), cur...)
	l.replaceRegion(all)
	l.lines = cur
}

// norm returns the cursor to the region's last row: the edit op parks
// it at the edit column, which on a wrapped input line is rows above
// the bottom; every other op's arithmetic starts from the last row.
func (l *live) norm() {
	if l.parked > 0 {
		l.wf(cursorDown(l.parked))
		l.parked = 0
	}
}

func newLive(w io.Writer, width int) *live {
	if width < 1 {
		width = 1
	}
	return &live{w: w, width: width}
}

// setWidth updates the wrap model's width (the resize, winch).
func (l *live) setWidth(w int) {
	if w >= 1 {
		l.width = w
	}
}

// visualRows is the terminal rows this logical line occupies: the
// terminal wraps a wide line (decision 2), an empty one is one row.
// The paint's escapes are invisible to the terminal, so the measure
// is the paint-free width.
func (l *live) visualRows(s string) int {
	n := (WidthOf(s) + l.width - 1) / l.width
	if n < 1 {
		n = 1
	}
	return n
}

// regionRows is the terminal rows the bookkeeping occupies.
func (l *live) regionRows() int {
	n := 0
	for _, line := range l.lines {
		n += l.visualRows(line)
	}
	return n
}

// lineEnd advances to the next row, safe at the right edge: toCol(1)
// first, then the LF. A line written to exactly the terminal's width
// leaves the cursor wrapped under the immediate-wrap model; the toCol
// cancels the pending wrap under the deferred one (xterm's, the common
// case), and the LF then lands on the next row either way, never a
// phantom blank line.
var lineEnd = toCol(1) + "\n"

// wf is the single write gate: a suspended region (the pager's screen)
// keeps its bookkeeping and writes nothing.
func (l *live) wf(s string) {
	if l.suspended {
		return
	}
	io.WriteString(l.w, s)
}

// clearRegion clears the old bookkeeping's terminal rows, top to
// bottom, and leaves the cursor on its last row at column 1.
func (l *live) clearRegion() {
	l.norm()
	old := l.regionRows()
	if old == 0 {
		return
	}
	l.wf(cursorUp(old - 1))
	for r := 0; r < old; r++ {
		l.wf(clearLine)
		l.wf(toCol(1))
		if r+1 < old {
			l.wf("\n")
		}
	}
}

// redraw replaces the live region with newLines: the old rows are
// cleared (however many terminal rows they wrapped to), the new lines
// are written from the top of the old region, and the cursor ends on
// the new region's last row. The committed rows above the region are
// untouched (decision 1).
func (l *live) redraw(newLines []string) {
	l.replaceRegion(newLines)
	l.lines = newLines
}

// replaceRegion clears the old region's terminal rows and writes rows
// from the top of the old region (the write half of the redraw; the
// bookkeeping is the caller's — draw's chunk rows are committed
// scrollback, not the region).
func (l *live) replaceRegion(rows []string) {
	l.clearRegion()
	if old := l.regionRows(); old > 0 {
		l.wf(cursorUp(old - 1))
	}
	for i, line := range rows {
		l.wf(toCol(1))
		l.wf(line)
		if i < len(rows)-1 {
			l.wf(lineEnd)
		}
	}
}

// draw commits the committed chunk (if any) and re-emits the live
// region as newLines (the redraw, chunk prefixed).
//
// Chunk contract: the chunk is split on newlines into the walk's lines,
// so a painted fragment must never span one — a paint's SGR and reset
// are no-ops to the terminal, but to the walk a reset landing on its
// own split piece is a phantom line. The callers paint per line
// (paintLines); the renderers already emit whole painted lines.
func (l *live) draw(committed string, newLines []string) {
	var all []string
	if committed != "" {
		cs := strings.Split(committed, "\n")
		if cs[len(cs)-1] == "" {
			cs = cs[:len(cs)-1]
		}
		l.record(cs)
		all = append(all, cs...)
	}
	all = append(all, newLines...)
	l.replaceRegion(all)
	l.lines = newLines
}

// enter freezes the current input row — its full text, so a width
// shrink mid-line never truncates the committed prompt — and writes the
// new input row below it, the fresh activity row between them when
// this line starts a turn (a quiet Enter); a steering Enter (the turn
// already live) leaves the activity row above untouched. The frozen
// row's terminal rows are cleared first, whatever they wrapped to.
func (l *live) enter(fullLine, activity, inputLine string) {
	l.record([]string{fullLine})
	l.norm()
	// the input row (the bookkeeping's last line, however many terminal
	// rows it wrapped to) is cleared and rewritten as its full text; the
	// rows above it (the activity row, the pending line) stand where they
	// are and become scrollback. The cursor is on the input row's last
	// terminal row, so its top is its own height minus one rows up.
	if len(l.lines) > 0 {
		in := l.visualRows(l.lines[len(l.lines)-1])
		if in > 1 {
			l.wf(cursorUp(in - 1))
		}
		for r := 0; r < in; r++ {
			l.wf(clearLine)
			l.wf(toCol(1))
			if r+1 < in {
				l.wf("\n")
			}
		}
		if in > 1 {
			l.wf(cursorUp(in - 1))
		}
	}
	l.wf(toCol(1))
	l.wf(fullLine)
	l.wf(lineEnd)
	if activity != "" {
		l.wf(activity)
		l.wf(lineEnd)
	}
	l.wf(inputLine)
	if activity != "" {
		l.lines = []string{activity, inputLine}
	} else {
		l.lines = []string{inputLine}
	}
}

// insertActivity puts a fresh activity row above the current input row
// (the turn start: the line's delivery, where the reader's enter left
// the input row alone) and re-emits the input row below it. The
// committed rows above are untouched.
func (l *live) insertActivity(activity, inputLine string) {
	l.redraw([]string{activity, inputLine})
}

// setActivity rewrites the activity row in place (the spinner tick,
// the phase switch): the redraw with the rest of the region standing;
// the cursor lands on the input row at column 1, and the next edit
// restores the edit column.
func (l *live) setActivity(activity string) {
	if len(l.lines) < 2 {
		return
	}
	rest := make([]string, 0, len(l.lines))
	rest = append(rest, activity)
	rest = append(rest, l.lines[1:]...)
	l.redraw(rest)
}

// edit rewrites the input row in place and parks the terminal cursor
// at the edit column (one-based, over the painted line's display
// width, wrapping with the line). The input row is the bookkeeping's
// last line; its terminal rows are cleared and rewritten, and the
// cursor lands on the edit column's own row.
func (l *live) edit(inputLine string, cursorCol int) {
	if len(l.lines) == 0 {
		return
	}
	l.norm()
	idx := len(l.lines) - 1
	// the input row is the bookkeeping's last line, and the cursor is on
	// its last terminal row: its top is its own height minus one rows up.
	old := l.visualRows(l.lines[idx])
	if old > 1 {
		l.wf(cursorUp(old - 1))
	}
	for r := 0; r < old; r++ {
		l.wf(clearLine)
		l.wf(toCol(1))
		if r+1 < old {
			l.wf("\n")
		}
	}
	if old > 1 {
		l.wf(cursorUp(old - 1))
	}
	l.wf(toCol(1))
	l.wf(inputLine)
	// the edit column is linear over the painted line: its row and
	// column within the wrapped line.
	newRows := l.visualRows(inputLine)
	row := (cursorCol - 1) / l.width
	col := (cursorCol-1)%l.width + 1
	if up := newRows - 1 - row; up > 0 {
		l.wf(cursorUp(up))
		l.parked = up
	}
	l.wf(toCol(col))
	l.lines[idx] = inputLine
}
