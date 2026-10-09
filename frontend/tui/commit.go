package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/imagemarker"
)

func formatTokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1000000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
}

func RenderUsage(t Theme, up, down, cacheRead int) string {
	hit := 0
	if up > 0 {
		hit = int(int64(cacheRead) * 100 / int64(up))
	}
	return t.Paint(SlotDim, fmt.Sprintf("up %s down %s · cache r %s %d%%",
		formatTokens(up), formatTokens(down), formatTokens(cacheRead), hit))
}

func RenderCompacted(t Theme, ev core.Compacted) string {
	return t.Paint(SlotEmber, t.Glyph(GlyphCompact)) + " " +
		t.Paint(SlotDim, fmt.Sprintf("compact: -%s kept %s · up %s down %s",
			formatTokens(ev.Dropped), formatTokens(ev.Kept),
			formatTokens(ev.Usage.Prompt), formatTokens(ev.Usage.Completion)))
}

func RenderPhaseEnd(t Theme, p core.Phase) string {
	glyph, slot := t.Glyph(GlyphOK), SlotSuccess
	if !p.Ok {
		glyph, slot = t.Glyph(GlyphFail), SlotError
	}
	line := t.Paint(slot, glyph) + " " + t.Paint(SlotDim, p.Name)
	if p.Note != "" {
		line += t.Paint(SlotDim, " \u00b7 "+p.Note)
	}
	return line
}

func RenderFault(t Theme, err error) string {
	return t.Paint(SlotError, t.Glyph(GlyphFail)+" fault: "+err.Error())
}

func RenderEmptyTurn(t Theme, ev core.EmptyTurn) string {
	return t.Paint(SlotDim, fmt.Sprintf("empty turn, resampling (%d/%d)", ev.Resample, ev.Limit))
}

const (
	previewHead = 6
	previewTail = 2
)

const elideSentinel = "\x00hidden\x00"

func RenderToolBlock(t Theme, width int, name string, args json.RawMessage, content string, failed bool, dur time.Duration) string {
	if name == "todo" || name == "scheduler" {
		open := t.Paint(SlotEmber, t.Glyph(GlyphDone)) + " " + t.Paint(SlotEmber, name)
		if d := verbDetail(args); d != "" {
			open += t.Paint(SlotDim, " · ") + t.Paint(SlotText, d)
		}
		if name == "todo" {
			return RenderTodoBlock(t, open, content)
		}
		return RenderSchedulerBlock(t, open, content)
	}
	var b strings.Builder
	b.WriteString(t.Paint(SlotEmber, t.Glyph(GlyphDone)))
	b.WriteString(" ")
	b.WriteString(t.Paint(SlotEmber, name))
	if d := toolDetail(name, args, content); d != "" {
		b.WriteString(t.Paint(SlotDim, " · "))
		b.WriteString(t.Paint(SlotText, d))
	}
	b.WriteString("\n")
	if ap := argsPreview(t, width, name, args); ap != "" {
		b.WriteString(ap)
		b.WriteString("\n")
	}
	if name != "view" {
		if p := preview(t, width, content); p != "" {
			b.WriteString(p)
			b.WriteString("\n")
		}
	}
	outcome, slot := t.Glyph(GlyphOK), SlotSuccess
	if failed {
		outcome, slot = t.Glyph(GlyphFail), SlotError
	}
	b.WriteString(t.Paint(SlotDim, name))
	b.WriteString(" ")
	b.WriteString(t.Paint(slot, outcome))
	b.WriteString(" ")
	b.WriteString(t.Paint(SlotDim, fmt.Sprintf("%.1fs", dur.Seconds())))
	return b.String()
}

func RenderReturnBlock(t Theme, width int, done core.WorkerDone) string {
	var b strings.Builder
	b.WriteString(t.Paint(SlotEmber, t.Glyph(GlyphDone)))
	b.WriteString(" ")
	b.WriteString(t.Paint(SlotEmber, "delegate #"+strconv.Itoa(done.N)))
	if task := firstLineOf(done.Task); task != "" {
		b.WriteString(t.Paint(SlotDim, " · "))
		b.WriteString(t.Paint(SlotText, cutAt(task, width-WidthOf(t.Glyph(GlyphDone))-len("delegate #"+strconv.Itoa(done.N))-4)))
	}
	b.WriteString("\n")
	if p := preview(t, width, done.Content); p != "" {
		b.WriteString(p)
		b.WriteString("\n")
	}
	outcome, slot := t.Glyph(GlyphOK), SlotSuccess
	if done.Exit != 0 {
		outcome, slot = t.Glyph(GlyphFail), SlotError
	}
	b.WriteString(t.Paint(SlotDim, "delegate"))
	b.WriteString(" ")
	b.WriteString(t.Paint(slot, outcome))
	b.WriteString(" ")
	b.WriteString(t.Paint(SlotDim, fmt.Sprintf("%.1fs", done.Duration.Seconds())))
	if done.Exit != 0 {
		b.WriteString(t.Paint(SlotDim, " · exit "+strconv.Itoa(done.Exit)))
	}
	b.WriteString(t.Paint(SlotDim, " · session "+cutAt(done.Session, 8)))
	return b.String()
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func cutAt(s string, width int) string {
	if width <= 0 {
		return ""
	}
	col := 0
	for i, r := range s {
		w := runeWidth(r)
		if col+w > width {
			return s[:i]
		}
		col += w
	}
	return s
}

func toolDetail(name string, args json.RawMessage, content string) string {
	var v map[string]any
	if err := json.Unmarshal(args, &v); err != nil {
		return ""
	}
	s := func(k string) string {
		x, _ := v[k].(string)
		return x
	}
	first := func(x string) string {
		if i := strings.IndexByte(x, '\n'); i >= 0 {
			return x[:i]
		}
		return x
	}
	switch name {
	case "bash":
		if c := s("command"); c != "" {
			return "$ " + first(c)
		}
	case "read", "write", "edit":
		if p := s("path"); p != "" {
			return p
		}
	case "view":
		d := s("path")
		if img := imageDetail(content); img != "" {
			if d == "" {
				return img
			}
			return d + " · " + img
		}
		return d
	case "python":
		if c := s("code"); c != "" {
			return first(c)
		}
	case "web":
		switch s("action") {
		case "search":
			if q := s("target"); q != "" {
				return q
			}
		case "fetch":
			if u := s("target"); u != "" {
				return u
			}
		}
	}
	return ""
}

func imageDetail(content string) string {
	ref, ok := imagemarker.Find(content)
	if !ok {
		return ""
	}
	sent := strconv.Itoa(ref.W) + "x" + strconv.Itoa(ref.H)
	if ref.OrigW != ref.W || ref.OrigH != ref.H {
		sent = strconv.Itoa(ref.OrigW) + "x" + strconv.Itoa(ref.OrigH) + " -> " + sent
	}
	if ref.Bytes <= 0 {
		return sent
	}
	return sent + " · " + humanBytes(ref.Bytes)
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return strconv.Itoa(n/1024) + " KB"
	default:
		return strconv.Itoa(n) + " B"
	}
}

func verbDetail(args json.RawMessage) string {
	var v map[string]any
	if err := json.Unmarshal(args, &v); err != nil {
		return ""
	}
	act, _ := v["action"].(string)
	if act == "" {
		return ""
	}
	if id, _ := v["id"].(string); id != "" {
		return act + " " + id
	}
	return act
}

func argsPreview(t Theme, width int, name string, args json.RawMessage) string {
	if name != "write" && name != "edit" {
		return ""
	}
	var v map[string]any
	if err := json.Unmarshal(args, &v); err != nil {
		return ""
	}
	s := func(k string) string {
		x, _ := v[k].(string)
		return x
	}
	side := func(k, prefix, slot string) string {
		if v := s(k); v != "" {
			return previewWith(t, width, v, prefix, slot)
		}
		return ""
	}
	if name == "write" {
		return side("content", "  ", SlotDim)
	}
	var rows []string
	for _, pair := range [][3]string{{"old", "- ", SlotError}, {"new", "+ ", SlotSuccess}} {
		if row := side(pair[0], pair[1], pair[2]); row != "" {
			rows = append(rows, row)
		}
	}
	return strings.Join(rows, "\n")
}

func preview(t Theme, width int, content string) string {
	return previewWith(t, width, content, "  ", SlotDim)
}

func previewWith(t Theme, width int, content, prefix, slot string) string {
	lines := strings.Split(content, "\n")
	if n := len(lines) - 1; n > 0 && lines[n] == "" {
		lines = lines[:n]
	}
	lines = screenRows(lines, width-WidthOf(prefix))
	if len(lines) == 0 {
		return ""
	}
	elided := 0
	if len(lines) > previewHead+previewTail {
		elided = len(lines) - previewHead - previewTail
		kept := make([]string, 0, previewHead+previewTail+1)
		kept = append(kept, lines[:previewHead]...)
		kept = append(kept, elideSentinel)
		kept = append(kept, lines[len(lines)-previewTail:]...)
		lines = kept
	}
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		if l == elideSentinel {
			b.WriteString(t.Paint(SlotDim, "  · "+strconv.Itoa(elided)+" lines hidden ·"))
			continue
		}
		b.WriteString(t.Paint(slot, prefix+l))
	}
	return b.String()
}

func screenRows(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	var rows []string
	for _, line := range lines {
		row := make([]rune, 0, width)
		used := 0
		for _, r := range line {
			w := runeWidth(r)
			if used+w > width && used > 0 {
				rows = append(rows, string(row))
				row, used = row[:0], 0
			}
			row = append(row, r)
			used += w
		}
		rows = append(rows, string(row))
	}
	return rows
}
