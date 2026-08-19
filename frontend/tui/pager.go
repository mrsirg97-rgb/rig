package tui

import (
	"io"
	"strconv"
	"strings"
)

// pager is the copy-mode (decision 2's one alternate-screen exception):
// a modal view over the committed history for terminals that give the
// operator no way up the scrollback (a web tty, a phone). PgUp opens
// it, PgUp/PgDn page, the arrows step a line, Home/End jump, and q,
// Esc, or Enter return to the live screen. The main UI stays
// scrollback-native; the pager borrows the alt screen the way less
// does, and leaving it restores the terminal exactly.
//
// The document is live.hist: whole painted logical lines, newest last.
// The view is bottom-anchored — offset counts lines scrolled up from
// the tail — and each frame fits lines to the height budget from the
// bottom, whole lines only (a wrapped line's rows count against the
// budget; one too tall for what remains leaves blank rows above).
type pager struct {
	lines  []string
	width  int
	height int
	offset int // lines above the tail (0 = the tail is visible)
	// footer: the live region's rows (the loader, the menu, the input,
	// the status), pinned under the history — the operator types and
	// steers while paging; the history scrolls above (the amended
	// pager: the controls locked, the content scrollable).
	footer []string
}

func newPager(lines []string, width, height int) *pager {
	if width < 1 {
		width = 1
	}
	if height < 3 {
		height = 3
	}
	return &pager{lines: lines, width: width, height: height}
}

// rows a painted line wraps to at the pager's width.
func (p *pager) rows(s string) int {
	n := (WidthOf(s) + p.width - 1) / p.width
	if n < 1 {
		n = 1
	}
	return n
}

// page is the lines one PgUp/PgDn moves: a screen minus one for
// context, at least one.
func (p *pager) page() int {
	footerRows := 0
	for _, f := range p.footer {
		footerRows += p.rows(f)
	}
	if n := p.height - 2 - footerRows; n > 1 {
		return n
	}
	return 1
}

func (p *pager) clamp() {
	if max := len(p.lines) - 1; p.offset > max {
		p.offset = max
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

// move scrolls by delta lines (negative = toward the tail) and reports
// whether the view changed.
func (p *pager) move(delta int) bool {
	old := p.offset
	p.offset += delta
	p.clamp()
	return p.offset != old
}

// frame is the full repaint: clear, the visible lines, the status row.
// One string, one write (a torn pager frame would be the tear bug in
// miniature).
func (p *pager) frame(th Theme) string {
	end := len(p.lines) - p.offset // exclusive
	footerRows := 0
	for _, f := range p.footer {
		footerRows += p.rows(f)
	}
	budget := p.height - 1 - footerRows // the pager's status row and the footer keep the last rows
	if budget < 1 {
		budget = 1
	}
	var view []string
	used := 0
	for i := end - 1; i >= 0; i-- {
		r := p.rows(p.lines[i])
		if used+r > budget {
			break
		}
		view = append([]string{p.lines[i]}, view...)
		used += r
	}
	var b strings.Builder
	b.WriteString(clearAll)
	b.WriteString(cursorHome)
	for _, line := range view {
		b.WriteString(line)
		b.WriteString(lineEnd)
	}
	// the blank rows between the content and the status row.
	for r := used; r < budget; r++ {
		b.WriteString("\n")
	}
	status := "history"
	if p.offset > 0 {
		status += " · " + strconv.Itoa(p.offset) + " up"
	}
	status += " · pgup/pgdn · q returns"
	if WidthOf(status) > p.width {
		status = truncateWidth(th, status, p.width)
	}
	b.WriteString(th.Paint(SlotDim, status))
	// the footer: the live region's rows, pinned under the history.
	for _, f := range p.footer {
		b.WriteString(lineEnd)
		b.WriteString(f)
	}
	return b.String()
}

// render writes one frame.
func (p *pager) render(w io.Writer, th Theme) {
	io.WriteString(w, p.frame(th))
}
