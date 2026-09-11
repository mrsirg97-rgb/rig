package tui

import (
	"io"
	"strings"
)

type live struct {
	w      io.Writer
	lines  []string
	width  int
	height int
	parked int

	// the geometry the screen was last painted in: the region's visual
	// row count and the width that count was taken at. every aim is
	// relative to the painted region, so a resize may rebuild rows at
	// the new width but must still aim with these.
	paintedRows  int
	paintedWidth int

	hist []string

	suspended    bool
	pend         []string
	frozen       []string
	frozenParked int

	onSuspended func()

	frame strings.Builder

	lastBlank bool

	status string
}

const histCap = 5000

func (l *live) record(lines []string) {
	l.hist = append(l.hist, lines...)
	if len(l.hist) > histCap {
		l.hist = append([]string(nil), l.hist[len(l.hist)-histCap:]...)
	}
	if l.suspended {
		l.pend = append(l.pend, lines...)
	}
}

func (l *live) collapseBlanks(lines []string) []string {
	out := lines[:0:0]
	for _, line := range lines {
		blank := WidthOf(line) == 0
		if blank && l.lastBlank {
			continue
		}
		out = append(out, line)
		l.lastBlank = blank
	}
	return out
}

func (l *live) suspend() {
	l.frozen = append([]string(nil), l.lines...)
	l.frozenParked = l.parked
	l.suspended = true
}

func (l *live) resume() {
	l.suspended = false
	cur := l.lines
	l.lines = l.frozen
	l.parked = l.frozenParked
	l.frozen = nil
	pend := l.pend
	l.pend = nil

	all := append(append([]string(nil), pend...), cur...)
	l.lines = cur
	l.replaceRegion(all)
	l.flush()
}

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
	return &live{w: w, width: width, paintedWidth: width}
}

func (l *live) setWidth(w int) {
	if w >= 1 {
		l.width = w
	}
}

func (l *live) setHeight(h int) {
	l.height = h
}

func (l *live) rowsOver(lines []string, extra int) int {
	if l.height <= 0 {
		return 0
	}
	n := extra
	for _, line := range lines {
		n += l.visualRows(line)
	}
	return n - l.height
}

func (l *live) visualRows(s string) int {
	n := (WidthOf(s) + l.width - 1) / l.width
	if n < 1 {
		n = 1
	}
	return n
}

var lineEnd = toCol(1) + "\n"

func (l *live) wf(s string) {
	if l.suspended {
		return
	}
	l.frame.WriteString(s)
}

func (l *live) flush() {
	if l.suspended {
		l.frame.Reset()
		if l.onSuspended != nil {
			l.onSuspended()
		}
		return
	}
	if l.frame.Len() == 0 {
		return
	}
	io.WriteString(l.w, syncOn+l.frame.String()+syncOff)
	l.frame.Reset()
}

func (l *live) guardWrap(line string) {
	w := WidthOf(line)
	if w > 0 && l.width > 0 && w%l.width == 0 {
		l.wf(toCol(1))
	}
}

func (l *live) redraw(newLines []string) {
	l.lines = newLines
	l.replaceRegion(newLines)
}

func (l *live) replaceRegion(rows []string) {
	l.norm()
	if l.paintedRows > 0 {
		l.wf(cursorUp(l.paintedRows - 1))
	}
	for i, line := range rows {
		l.wf(toCol(1))
		l.wf(line)
		l.wf(clearToEOL)
		if i < len(rows)-1 {
			l.wf(lineEnd)
		}
	}
	if l.paintedRows > 0 {
		l.wf(clearBelow)
	}
	if len(rows) > 0 {
		l.guardWrap(rows[len(rows)-1])
	}
	l.paintedRows = l.trackRows()
	l.paintedWidth = l.width
}

// trackRows records the region's span on screen: the visual rows the next
// repaint will redraw, capped at the viewport because a paint that
// overflowed the pane scrolled its own head into history.
func (l *live) trackRows() int {
	n := 0
	for _, line := range l.lines {
		n += l.visualRows(line)
	}
	if l.height > 0 && n > l.height {
		n = l.height
	}
	return n
}

func (l *live) draw(committed string, newLines []string, status string) {
	var all []string
	if committed != "" {
		cs := strings.Split(committed, "\n")
		if cs[len(cs)-1] == "" {
			cs = cs[:len(cs)-1]
		}
		cs = l.collapseBlanks(cs)
		l.record(cs)
		all = append(all, cs...)
		if len(cs) > 0 && WidthOf(cs[len(cs)-1]) == 0 &&
			len(newLines) > 0 && WidthOf(newLines[0]) == 0 {
			newLines = newLines[1:]
		}
	}
	all = append(all, withStatus(newLines, status)...)
	l.lines = withStatus(newLines, status)
	l.status = status
	l.replaceRegion(all)
	l.flush()
}

func (l *live) enter(fullLine, activity, inputLine, status string) {

	frozen := strings.Split(fullLine, "\n")
	l.record(append(append([]string(nil), frozen...), ""))
	hadSep := len(l.lines) > 0 && WidthOf(l.lines[0]) == 0
	l.lastBlank = true
	l.norm()

	// aim at the top of the live block above the input: menu or activity
	// rows are repainted away, but the separator blank between the
	// transcript and the region survives the submit. a blank first row can
	// only be that separator: the builder never renders a blank live row
	// above the input otherwise. with no live block this is the input
	// row's top, the way submit always painted.
	aim := 0
	if hadSep {
		aim = 1
	}
	up := 0
	for i := aim; i < len(l.lines); i++ {
		up += l.visualRows(l.lines[i])
	}
	if up > 0 {
		l.wf(cursorUp(up - 1))
	}

	rows := append([]string(nil), frozen...)
	rows = append(rows, "")
	if activity != "" {
		rows = append(rows, activity)
	}
	rows = append(rows, inputLine)
	srows := statusRows(status)
	rows = append(rows, srows...)
	for i, row := range rows {
		l.wf(toCol(1))
		l.wf(row)
		l.wf(clearToEOL)
		if i < len(rows)-1 {
			l.wf(lineEnd)
		}
	}
	l.wf(clearBelow)
	if activity != "" {
		l.lines = []string{activity, inputLine}
	} else {
		l.lines = []string{inputLine}
	}
	l.lines = append(l.lines, srows...)
	l.paintedRows = l.trackRows()
	l.paintedWidth = l.width
	l.status = status
	if len(srows) > 0 {
		l.guardWrap(srows[len(srows)-1])
	} else {
		l.guardWrap(inputLine)
	}
	l.flush()
}

func (l *live) insertActivity(activity string) {
	rest := make([]string, 0, len(l.lines)+1)
	rest = append(rest, activity)
	rest = append(rest, l.lines...)
	l.redraw(rest)
	l.flush()
}

func (l *live) setActivity(activity string) {
	if len(l.lines) < 2 {
		return
	}
	rest := make([]string, 0, len(l.lines))
	rest = append(rest, activity)
	rest = append(rest, l.lines[1:]...)
	l.redraw(rest)
	l.flush()
}

func (l *live) edit(inputLine string, cursorCol int, status string) {
	if len(l.lines) == 0 {
		return
	}
	l.norm()
	oldStatus := 0
	for _, sr := range statusRows(l.status) {
		oldStatus += l.visualRows(sr)
	}
	idx := len(l.lines) - 1 - len(statusRows(l.status))

	old := l.visualRows(l.lines[idx])
	upTop := old - 1 + oldStatus
	if upTop > 0 {
		l.wf(cursorUp(upTop))
	}
	l.wf(toCol(1))
	l.wf(inputLine)
	l.wf(clearToEOL)
	l.guardWrap(inputLine)

	l.parkAt(inputLine, cursorCol, status, false)
	l.lines[idx] = inputLine
	l.status = status
	l.flush()
}

func (l *live) parkAt(inputLine string, cursorCol int, status string, fromStatusRow bool) {

	newRows := l.visualRows(inputLine)
	row := (cursorCol - 1) / l.width
	col := (cursorCol-1)%l.width + 1
	up := newRows - 1 - row
	park := up
	if srows := statusRows(status); len(srows) > 0 {

		h := 0
		for _, sr := range srows {
			h += l.visualRows(sr)
		}
		park += h
		if fromStatusRow {
			up += h
		}
	}
	if up > 0 {
		l.wf(cursorUp(up))
	}
	l.parked = park
	l.wf(toCol(col))
}

func (l *live) editFull(newLines []string, cursorCol int, status string) {
	rows := withStatus(newLines, status)
	l.lines = rows
	l.status = status
	l.replaceRegion(rows)
	l.parkAt(newLines[len(newLines)-1], cursorCol, status, true)
	l.flush()
}

func statusRows(status string) []string {
	if status == "" {
		return nil
	}
	return strings.Split(status, "\n")
}

func withStatus(newLines []string, status string) []string {
	lines := append([]string(nil), newLines...)
	return append(lines, statusRows(status)...)
}
