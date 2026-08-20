package tui

import (
	"io"
	"strings"
)

type live struct {
	w      io.Writer
	lines  []string
	width  int
	parked int

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
	l.replaceRegion(all)
	l.lines = cur
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
	return &live{w: w, width: width}
}

func (l *live) setWidth(w int) {
	if w >= 1 {
		l.width = w
	}
}

func (l *live) visualRows(s string) int {
	n := (WidthOf(s) + l.width - 1) / l.width
	if n < 1 {
		n = 1
	}
	return n
}

func (l *live) regionRows() int {
	n := 0
	for _, line := range l.lines {
		n += l.visualRows(line)
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
	io.WriteString(l.w, l.frame.String())
	l.frame.Reset()
}

func (l *live) guardWrap(line string) {
	w := WidthOf(line)
	if w > 0 && l.width > 0 && w%l.width == 0 {
		l.wf(toCol(1))
	}
}

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

func (l *live) redraw(newLines []string) {
	l.replaceRegion(newLines)
	l.lines = newLines
}

func (l *live) replaceRegion(rows []string) {
	l.clearRegion()
	if old := l.regionRows(); old > 0 {
		l.wf(cursorUp(old - 1))
	}
	for i, line := range rows {
		l.wf(toCol(1))
		if i == len(rows)-1 {

			l.wf(clearBelow)
		}
		l.wf(line)
		if i < len(rows)-1 {
			l.wf(lineEnd)
		}
	}
	if len(rows) > 0 {
		l.guardWrap(rows[len(rows)-1])
	}
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
	}
	all = append(all, withStatus(newLines, status)...)
	l.replaceRegion(all)
	l.lines = withStatus(newLines, status)
	l.status = status
	l.flush()
}

func (l *live) enter(fullLine, activity, inputLine, status string) {

	frozen := strings.Split(fullLine, "\n")
	l.record(append(append([]string(nil), frozen...), ""))
	l.lastBlank = true
	l.norm()

	oldStatusRows := statusRows(l.status)
	oldStatusHeight := 0
	for _, sr := range oldStatusRows {
		oldStatusHeight += l.visualRows(sr)
	}
	if len(l.lines) > 0 {
		idx := len(l.lines) - 1 - len(oldStatusRows)
		in := l.visualRows(l.lines[idx])
		upTop := in - 1 + oldStatusHeight
		if upTop > 0 {
			l.wf(cursorUp(upTop))
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
	for i, fr := range frozen {
		if i > 0 {
			l.wf(clearLine)
		}
		l.wf(fr)
		l.wf(lineEnd)
	}

	l.wf(clearLine)
	l.wf(lineEnd)

	if activity != "" {
		l.wf(clearLine)
		l.wf(activity)
		l.wf(lineEnd)
	}
	l.wf(clearLine)
	l.wf(inputLine)
	srows := statusRows(status)
	for _, sr := range srows {
		l.wf(lineEnd)
		l.wf(clearLine)
		l.wf(sr)
	}
	if activity != "" {
		l.lines = []string{activity, inputLine}
	} else {
		l.lines = []string{inputLine}
	}
	l.lines = append(l.lines, srows...)
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
	l.replaceRegion(rows)
	l.lines = rows
	l.status = status
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
