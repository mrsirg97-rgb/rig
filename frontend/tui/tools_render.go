package tui

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	todoHeadRe   = regexp.MustCompile(`^(\[[^\]]+\] )?(\d+) open · (\d+) of (\d+) finished shown( · next: (\S+))?$`)
	todoTaskRe   = regexp.MustCompile(`^  (t\d+) \[([xr!~ ])\] (.+)$`)
	todoNotesRe  = regexp.MustCompile(`^    · (\d+) notes?( \(.*\))?$`)
	schedRunsRe  = regexp.MustCompile(`^(j\d+) · (\d+) runs? \(oldest first\):$`)
	schedJobHead = regexp.MustCompile(`^(j\d+)(?: (.*))?$`)
)

type todoTask struct {
	ID          string
	Status      string
	Text        string
	Links       string
	Waits       string
	Claim       string
	ReviewClaim bool
	NoteCount   string
}

type todoParsed struct {
	Scope    string
	Open     int
	Finished int
	Shown    int
	Next     string
	Note     string
	Echo     bool
	Tasks    []todoTask
	Footers  []string
}

func parseTodo(reply string) (todoParsed, bool) {
	lines := strings.Split(strings.TrimRight(reply, "\n"), "\n")
	p := todoParsed{}
	i := 0
	if i < len(lines) && strings.HasPrefix(lines[i], "→ ") {
		p.Note = strings.TrimPrefix(lines[i], "→ ")
		i++
	}
	head := -1
	for j := i; j < len(lines); j++ {
		if todoHeadRe.MatchString(lines[j]) {
			head = j
			break
		}
	}
	if head < 0 {
		return p, false
	}
	m := todoHeadRe.FindStringSubmatch(lines[head])
	p.Scope = m[1]
	var err error
	if p.Open, err = atoi(m[2]); err != nil {
		return p, false
	}
	if p.Shown, err = atoi(m[3]); err != nil {
		return p, false
	}
	if p.Finished, err = atoi(m[4]); err != nil {
		return p, false
	}
	p.Next = m[6]
	for j := i; j < len(lines); j++ {
		if j == head {
			continue
		}
		line := lines[j]
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "· ") {
			p.Footers = append(p.Footers, line)
			continue
		}
		if todoNotesRe.MatchString(line) {
			if len(p.Tasks) == 0 {
				return p, false
			}
			p.Tasks[len(p.Tasks)-1].NoteCount = line
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
		case "r":
			task.Status = "review"
		default:
			task.Status = "pending"
		}
		rest := tm[3]
		for _, verb := range []string{" · claimed for review by ", " · claimed by "} {
			if j := strings.LastIndex(rest, verb); j >= 0 {
				task.Claim = rest[j+len(verb):]
				task.ReviewClaim = verb == " · claimed for review by "
				rest = rest[:j]
				break
			}
		}
		if j := strings.LastIndex(rest, " · waits for "); j >= 0 {
			task.Waits = rest[j+len(" · waits for "):]
			rest = rest[:j]
		}
		for _, sep := range []string{" · blocks ", " · requires "} {
			if j := strings.LastIndex(rest, sep); j >= 0 {
				tail := rest[j+len(" · "):]
				if task.Links != "" {
					task.Links = tail + " · " + task.Links
				} else {
					task.Links = tail
				}
				rest = rest[:j]
			}
		}
		task.Text = rest
		p.Tasks = append(p.Tasks, task)
	}
	p.Echo = false
	for j := i; j < head; j++ {
		if todoTaskRe.MatchString(lines[j]) {
			p.Echo = true
			break
		}
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
	if !strings.Contains(reply, "\n") && strings.HasPrefix(reply, "queue: ") {
		return t.Paint(SlotDim, reply)
	}
	p, ok := parseTodo(reply)
	if !ok {
		var b strings.Builder
		b.WriteString(opening)
		b.WriteString("\n")
		for _, line := range strings.Split(strings.TrimRight(reply, "\n"), "\n") {
			b.WriteString(t.Paint(SlotDim, line))
			b.WriteString("\n")
		}
		return strings.TrimSuffix(b.String(), "\n")
	}
	var b strings.Builder
	b.WriteString(opening)
	b.WriteString("\n")

	total := p.Open + p.Finished
	segs := total
	if segs > 8 {
		segs = 8
	}
	if segs < 1 {
		segs = 1
	}
	filled := 0
	if total > 0 {
		filled = p.Finished * segs / total
		if rem := p.Finished * segs % total; rem*2 >= total {
			filled++
		}
	}
	if filled > segs {
		filled = segs
	}
	if p.Scope != "" {
		b.WriteString(t.Paint(SlotDim, p.Scope))
	}
	b.WriteString(t.Paint(SlotEmber, strings.Repeat(t.Glyph(GlyphBarOn), filled)))
	b.WriteString(t.Paint(SlotDim, strings.Repeat(t.Glyph(GlyphBarOff), segs-filled)))
	head := fmt.Sprintf(" %d open · %d of %d finished shown", p.Open, p.Shown, p.Finished)
	if p.Next != "" {
		head += " · next " + p.Next
	}
	b.WriteString(t.Paint(SlotDim, head))
	b.WriteString("\n")
	if p.Echo && p.Note != "" {
		b.WriteString(t.Paint(SlotDim, "→ "+p.Note))
		b.WriteString("\n")
	}
	for _, task := range p.Tasks {
		glyph, slot := t.todoStatusGlyph(task.Status)
		b.WriteString(t.Paint(slot, glyph))
		b.WriteString(" ")
		b.WriteString(t.Paint(SlotDim, task.ID))
		b.WriteString(" ")
		b.WriteString(t.Paint(SlotText, task.Text))
		if task.Links != "" {
			b.WriteString(t.Paint(SlotDim, " · "+task.Links))
		}
		if task.Waits != "" {
			b.WriteString(t.Paint(SlotDim, " · waits for "+task.Waits))
		}
		if task.Claim != "" {
			verb := "claimed by"
			if task.ReviewClaim {
				verb = "claimed for review by"
			}
			b.WriteString(t.Paint(SlotDim, " · "+verb+" "+task.Claim))
		}
		b.WriteString("\n")
		if task.NoteCount != "" {
			b.WriteString(t.Paint(SlotDim, task.NoteCount))
			b.WriteString("\n")
		}
	}
	for _, footer := range p.Footers {
		b.WriteString(t.Paint(SlotDim, "  "+footer))
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
	case "review":
		return t.Glyph(GlyphReview), SlotWarn
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
		if strings.HasSuffix(line, ":") && !schedJobHead.MatchString(line) {
			flushJob()
			p.Sections = append(p.Sections, schedSection{Name: strings.TrimSuffix(line, ":")})
			cur = &p.Sections[len(p.Sections)-1]
			continue
		}
		if strings.Contains(line, ": no jobs") {
			flushJob()
			name := line
			if i := strings.Index(name, ": no jobs"); i >= 0 {
				name = name[:i]
			}
			p.Sections = append(p.Sections, schedSection{Name: name, Empty: true})
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
