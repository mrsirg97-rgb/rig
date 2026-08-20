package tui

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	todoHeadRe   = regexp.MustCompile(`^(\d+)/(\d+) done( · next: (\S+))?( · (\d+) failed)?$`)
	todoTaskRe   = regexp.MustCompile(`^  (t\d+) \[([x!~ ])\] (.+)$`)
	schedRunsRe  = regexp.MustCompile(`^(j\d+) · (\d+) runs? \(oldest first\):$`)
	schedJobHead = regexp.MustCompile(`^(j\d+)(?: (.*))?$`)
)

type todoTask struct {
	ID     string
	Status string
	Text   string
	Waits  string
	Claim  string
}

type todoParsed struct {
	Done   int
	Total  int
	Active int
	Next   string
	Failed int
	Tasks  []todoTask
	Footer string
}

func parseTodo(reply string) (todoParsed, bool) {
	lines := strings.Split(strings.TrimRight(reply, "\n"), "\n")
	p := todoParsed{}
	i := 0
	if i < len(lines) && strings.HasPrefix(lines[i], "→ ") {
		i++
	}
	if i >= len(lines) {
		return p, false
	}
	m := todoHeadRe.FindStringSubmatch(lines[i])
	if m == nil {
		return p, false
	}
	var err error
	if p.Done, err = atoi(m[1]); err != nil {
		return p, false
	}
	if p.Total, err = atoi(m[2]); err != nil {
		return p, false
	}
	p.Next = m[4]
	if m[6] != "" {
		p.Failed, err = atoi(m[6])
		if err != nil {
			return p, false
		}
	}
	i++
	for i < len(lines) {
		line := lines[i]
		i++
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "· ") {
			if p.Footer != "" {
				return p, false
			}
			p.Footer = line
			continue
		}
		tm := todoTaskRe.FindStringSubmatch(line)
		if tm == nil {
			return p, false
		}
		task := todoTask{ID: tm[1]}
		switch tm[2] {
		case "x":
			task.Status = "done"
		case "!":
			task.Status = "failed"
		case "~":
			task.Status = "active"
			p.Active++
		default:
			task.Status = "pending"
		}
		rest := tm[3]
		if j := strings.LastIndex(rest, " · claimed by "); j >= 0 {
			task.Claim = rest[j+len(" · claimed by "):]
			rest = rest[:j]
		}
		if j := strings.LastIndex(rest, " · waits on "); j >= 0 {
			task.Waits = rest[j+len(" · waits on "):]
			rest = rest[:j]
		}
		task.Text = rest
		p.Tasks = append(p.Tasks, task)
	}
	return p, true
}

func atoi(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%s: not a number", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func RenderTodoBlock(t Theme, opening, reply string) string {
	p, ok := parseTodo(reply)
	if !ok {
		return reply
	}
	var b strings.Builder
	b.WriteString(opening)
	b.WriteString("\n")

	segs := p.Total
	if segs > 8 {
		segs = 8
	}
	if segs < 1 {
		segs = 1
	}
	filled := 0
	if p.Total > 0 {
		filled = (p.Done + p.Active) * segs / p.Total
		if rem := (p.Done + p.Active) * segs % p.Total; rem*2 >= p.Total {
			filled++
		}
	}
	if filled > segs {
		filled = segs
	}
	b.WriteString(t.Paint(SlotAccent, strings.Repeat(t.Glyph(GlyphBarOn), filled)))
	b.WriteString(t.Paint(SlotDim, strings.Repeat(t.Glyph(GlyphBarOff), segs-filled)))
	head := fmt.Sprintf(" %d/%d", p.Done, p.Total)
	if p.Next != "" {
		head += " · next " + p.Next
	}
	if p.Failed > 0 {
		head += fmt.Sprintf(" · %d failed", p.Failed)
	}
	b.WriteString(t.Paint(SlotDim, head))
	b.WriteString("\n")
	for _, task := range p.Tasks {
		glyph, slot := t.todoStatusGlyph(task.Status)
		b.WriteString(t.Paint(slot, glyph))
		b.WriteString(" ")
		b.WriteString(t.Paint(SlotDim, task.ID))
		b.WriteString(" ")
		b.WriteString(t.Paint(SlotText, task.Text))
		if task.Waits != "" {
			b.WriteString(t.Paint(SlotDim, " · waits on "+task.Waits))
		}
		if task.Claim != "" {
			b.WriteString(t.Paint(SlotDim, " · claimed by "+task.Claim))
		}
		b.WriteString("\n")
	}
	if p.Footer != "" {
		b.WriteString(t.Paint(SlotDim, "  "+p.Footer))
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (t Theme) todoStatusGlyph(status string) (string, string) {
	switch status {
	case "done":
		return t.Glyph(GlyphDone), SlotSuccess
	case "active":
		return t.Glyph(GlyphActive), SlotAccent
	case "failed":
		return t.Glyph(GlyphFail), SlotError
	default:
		return t.Glyph(GlyphPending), SlotDim
	}
}

type schedParsed struct {
	IsList   bool
	Sections []schedSection

	Runs []string
}

type schedSection struct {
	Name  string
	Empty bool
	Jobs  []schedJob
}

type schedJob struct {
	Head   string
	Detail []string
}

func parseScheduler(reply string) (schedParsed, bool) {
	lines := strings.Split(strings.TrimRight(reply, "\n"), "\n")
	if len(lines) == 0 {
		return schedParsed{}, false
	}
	p := schedParsed{}
	if m := schedRunsRe.FindStringSubmatch(lines[0]); m != nil {
		p.Runs = append([]string{lines[0]}, lines[1:]...)
		return p, true
	}
	p.IsList = true
	var cur *schedSection
	var job *schedJob
	flushJob := func() { job = nil }
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "global:") || strings.HasPrefix(line, "cwd:") {
			flushJob()
			name := strings.TrimSuffix(strings.TrimSuffix(line, ": no jobs"), ":")
			empty := strings.HasSuffix(line, ": no jobs")
			p.Sections = append(p.Sections, schedSection{Name: name, Empty: empty})
			cur = &p.Sections[len(p.Sections)-1]
			continue
		}
		if cur == nil {
			return p, false
		}
		if strings.HasPrefix(line, "  ") {
			if job == nil {
				return p, false
			}
			job.Detail = append(job.Detail, line[2:])
			continue
		}
		if !schedJobHead.MatchString(line) || cur.Empty {
			return p, false
		}
		flushJob()
		cur.Jobs = append(cur.Jobs, schedJob{Head: line})
		job = &cur.Jobs[len(cur.Jobs)-1]
	}
	return p, true
}

func RenderSchedulerBlock(t Theme, opening, reply string) string {
	p, ok := parseScheduler(reply)
	if !ok {
		return reply
	}
	var b strings.Builder
	b.WriteString(opening)
	b.WriteString("\n")
	if !p.IsList {
		for _, line := range p.Runs {
			b.WriteString(t.Paint(SlotDim, "  "+line))
			b.WriteString("\n")
		}
		return strings.TrimSuffix(b.String(), "\n")
	}
	for _, sec := range p.Sections {
		if sec.Empty {
			b.WriteString(t.Paint(SlotDim, "  "+sec.Name+": no jobs"))
			b.WriteString("\n")
			continue
		}
		b.WriteString(t.Paint(SlotDim, "  "+sec.Name+":"))
		b.WriteString("\n")
		for _, job := range sec.Jobs {
			glyph, slot := t.schedStateGlyph(job.Head)
			b.WriteString(t.Paint(slot, glyph))
			b.WriteString(" ")
			b.WriteString(t.Paint(SlotText, job.Head))
			b.WriteString("\n")
			for _, d := range job.Detail {
				if strings.HasPrefix(d, "drift: ") {
					b.WriteString(t.Paint(SlotWarn, "    "+d))
				} else {
					b.WriteString(t.Paint(SlotDim, "    "+d))
				}
				b.WriteString("\n")
			}
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (t Theme) schedStateGlyph(head string) (string, string) {
	fields := strings.Fields(head)
	for _, f := range fields {
		switch f {
		case "active":
			return t.Glyph(GlyphDone), SlotAccent
		case "paused":
			return t.Glyph(GlyphPending), SlotDim
		case "removed":
			return t.Glyph(GlyphFail), SlotError
		}
	}
	return t.Glyph(GlyphPending), SlotDim
}
