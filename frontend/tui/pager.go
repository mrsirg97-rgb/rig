package tui

import (
	"io"
	"strconv"
	"strings"
)

type pager struct {
	lines  []string
	width  int
	height int
	offset int

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

func (p *pager) rows(s string) int {
	n := (WidthOf(s) + p.width - 1) / p.width
	if n < 1 {
		n = 1
	}
	return n
}

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

func (p *pager) move(delta int) bool {
	old := p.offset
	p.offset += delta
	p.clamp()
	return p.offset != old
}

func (p *pager) frame(th Theme) string {
	end := len(p.lines) - p.offset
	footerRows := 0
	for _, f := range p.footer {
		footerRows += p.rows(f)
	}
	budget := p.height - 1 - footerRows
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

	for _, f := range p.footer {
		b.WriteString(lineEnd)
		b.WriteString(f)
	}
	return b.String()
}

func (p *pager) render(w io.Writer, th Theme) {
	io.WriteString(w, p.frame(th))
}
