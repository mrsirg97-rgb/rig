package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

// formatTokens: the CLI's shaping, byte-identical (the CLI is the piped
// reference; the TUI restyles, never re-shapes). Raw under 1000,
// one-decimal k under 10k, rounded k under 1M, else M.
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

// RenderUsage is the turn-end usage line (decision 2): the turn's
// session-scoped totals, pane's shaping, all dim. The hit rate is the
// CLI's (cache read over prompt; 0 on an empty turn).
func RenderUsage(t Theme, up, down, cacheRead int) string {
	hit := 0
	if up > 0 {
		hit = cacheRead * 100 / up
	}
	return t.Paint(SlotDim, fmt.Sprintf("up %s down %s · cache r %s %d%%",
		formatTokens(up), formatTokens(down), formatTokens(cacheRead), hit))
}

// RenderCompacted is the compaction line (decision 2; the CLI's voice,
// pane's shaping): the compact glyph, the drop and keep in the
// trigger's units, the summary call's usage.
func RenderCompacted(t Theme, ev core.Compacted) string {
	return t.Paint(SlotAccent, t.Glyph(GlyphCompact)) + " " +
		t.Paint(SlotDim, fmt.Sprintf("compact: -%s kept %s · summary up %s down %s",
			formatTokens(ev.Dropped), formatTokens(ev.Kept),
			formatTokens(ev.Usage.Prompt), formatTokens(ev.Usage.Completion)))
}

// RenderFault is the fault line (the CLI's voice: [fault] err), the
// fail glyph and the error color.
func RenderFault(t Theme, err error) string {
	return t.Paint(SlotError, t.Glyph(GlyphFail)+" fault: "+err.Error())
}

// previewHead and previewTail are the tool preview's caps (decision 4's
// v1 values): the TUI's, and the runtime's own output caps apply first.
const (
	previewHead = 6
	previewTail = 2
)

const elideSentinel = "\x00elided\x00"

// RenderToolBlock is the committed tool block (decision 4): the
// accent-glyph start line with the detail, the head-six/tail-two body
// with the loud elided marker between, and the close line with the
// name, the outcome, and the plain duration. A fed-back failure keeps
// the content visible: the refusal is the interesting part. todo and
// scheduler delegate to tools_render's shared renderer (decision 6).
func RenderToolBlock(t Theme, name string, args json.RawMessage, content string, failed bool, dur time.Duration) string {
	if name == "todo" || name == "scheduler" {
		open := t.Paint(SlotAccent, t.Glyph(GlyphDone)) + " " + t.Paint(SlotAccent, name)
		if d := verbDetail(args); d != "" {
			open += t.Paint(SlotDim, " · ") + t.Paint(SlotText, d)
		}
		if name == "todo" {
			return RenderTodoBlock(t, open, content)
		}
		return RenderSchedulerBlock(t, open, content)
	}
	var b strings.Builder
	b.WriteString(t.Paint(SlotAccent, t.Glyph(GlyphDone)))
	b.WriteString(" ")
	b.WriteString(t.Paint(SlotAccent, name))
	if d := toolDetail(name, args); d != "" {
		b.WriteString(t.Paint(SlotDim, " · "))
		b.WriteString(t.Paint(SlotText, d))
	}
	b.WriteString("\n")
	if p := preview(t, content); p != "" {
		b.WriteString(p)
		b.WriteString("\n")
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

// toolDetail is decision 4's table, one line per tool. Unknown tools
// (a future registration) render name-only: the table is a nicety, not
// a contract.
func toolDetail(name string, args json.RawMessage) string {
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
	case "read", "write", "edit", "ls":
		if p := s("path"); p != "" {
			return p
		}
	case "find", "grep":
		if p := s("pattern"); p != "" {
			return p
		}
	case "python":
		if c := s("code"); c != "" {
			return first(c)
		}
	case "web_search":
		if q := s("query"); q != "" {
			return q
		}
	case "web_fetch":
		if u := s("url"); u != "" {
			return u
		}
	case "diff":
		if m := s("mode"); m != "" {
			return m
		}
	}
	return ""
}

// verbDetail is the todo/scheduler opening's detail: the action, with
// the id when the verb takes one (start t3, runs j2).
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

// preview is the head/tail body (decision 4): first six and last two
// lines with the dim elided marker between, two-space indented, all dim.
// Eight lines or fewer commit in full; a trailing newline is not a line.
func preview(t Theme, content string) string {
	lines := strings.Split(content, "\n")
	if n := len(lines) - 1; n > 0 && lines[n] == "" {
		lines = lines[:n]
	}
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
			b.WriteString(t.Paint(SlotDim, "  · "+strconv.Itoa(elided)+" lines elided ·"))
			continue
		}
		b.WriteString(t.Paint(SlotDim, "  "+l))
	}
	return b.String()
}
