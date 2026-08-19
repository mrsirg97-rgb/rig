package tui

import "strings"

// The markdown pass (SPEC_TUI, amended: a decision of its own): the
// model's text renders with inline markdown decorated for the human,
// on the committed-line path, per closed line, without buffering — the
// streaming and the scrollback-native design are not renegotiated. The
// subset is exactly this: `**bold**` -> bold (the text color, SGR bold), `*em*` -> dim,
// `` `code` `` -> ember, `# `..`### ` headings -> accent with the marks
// dropped, `- `/`* `/`1. ` list items -> the dot glyph for the bullet,
// `> ` quotes -> dim, and fenced code (a line that is ``` with an
// optional info string) toggles a code mode: lines inside commit
// preformatted (dim, indented two, never word-wrapped), the fence lines
// drop, the info string shows dim once. Everything else renders raw:
// tables, links, images, HTML, nested lists. Reasoning is never
// decorated (the margin notes stay raw); tool results and command
// output are the renderers' own.

// mdLine decorates one closed prose line: the segments in, the
// segments out (the paint per piece), and whether the line is a fence
// (the caller toggles code mode and drops the line). Inside code mode
// the caller does not call this.
func mdLine(th Theme, segs []seg) (out []seg, fence bool, info string) {
	// only text-slot lines are markdown; a mixed line (reasoning
	// segments) stays raw.
	plain := true
	var b strings.Builder
	for _, s := range segs {
		if s.slot != SlotText && s.slot != "" {
			plain = false
		}
		b.WriteString(s.text)
	}
	if !plain {
		return segs, false, ""
	}
	line := b.String()
	trim := strings.TrimSpace(line)
	if strings.HasPrefix(trim, "```") {
		return nil, true, strings.TrimSpace(strings.TrimPrefix(trim, "```"))
	}
	// block-level: headings, quotes, list bullets (the leading mark
	// decides; the rest of the line is inline-decorated).
	switch {
	case strings.HasPrefix(line, "### "):
		return []seg{{slot: SlotAccent, text: line[4:]}}, false, ""
	case strings.HasPrefix(line, "## "):
		return []seg{{slot: SlotAccent, text: line[3:]}}, false, ""
	case strings.HasPrefix(line, "# "):
		return []seg{{slot: SlotAccent, text: line[2:]}}, false, ""
	case strings.HasPrefix(line, "> "):
		return append([]seg{{slot: SlotDim, text: th.Glyph(GlyphBarOn) + " "}}, mdInline(line[2:], SlotDim)...), false, ""
	}
	// list items: "- ", "* ", "N. " (leading spaces kept as indent)
	lead := len(line) - len(strings.TrimLeft(line, " "))
	rest := line[lead:]
	if strings.HasPrefix(rest, "- ") || strings.HasPrefix(rest, "* ") {
		return append([]seg{{slot: "", text: line[:lead]}, {slot: SlotDim, text: th.Glyph(GlyphDot) + " "}}, mdInline(rest[2:], SlotText)...), false, ""
	}
	if n := numberedPrefix(rest); n > 0 {
		return append([]seg{{slot: "", text: line[:lead]}, {slot: SlotDim, text: rest[:n]}}, mdInline(rest[n:], SlotText)...), false, ""
	}
	return mdInline(line, SlotText), false, ""
}

// numberedPrefix is the length of a "N. " list mark at the start of s,
// or 0.
func numberedPrefix(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i > 3 || i+1 >= len(s) || s[i] != '.' || s[i+1] != ' ' {
		return 0
	}
	return i + 2
}

// mdInline decorates the inline marks in one line: **bold**, *em* /
// _em_, `code`; an unclosed mark renders raw; \* escapes; a backtick
// run inside code is text. base is the slot for undecorated text.
func mdInline(line string, base string) []seg {
	var out []seg
	var cur strings.Builder
	flush := func(slot string) {
		if cur.Len() > 0 {
			out = append(out, seg{slot: slot, text: cur.String()})
			cur.Reset()
		}
	}
	rs := []rune(line)
	i := 0
	for i < len(rs) {
		r := rs[i]
		switch {
		case r == '\\' && i+1 < len(rs) && strings.ContainsRune("*_`\\", rs[i+1]):
			cur.WriteRune(rs[i+1])
			i += 2
		case r == '`':
			// code: to the next backtick on the line, else raw
			j := i + 1
			for j < len(rs) && rs[j] != '`' {
				j++
			}
			if j < len(rs) && j > i+1 {
				flush(base)
				out = append(out, seg{slot: SlotEmber, text: string(rs[i+1 : j])})
				i = j + 1
			} else {
				cur.WriteRune(r)
				i++
			}
		case r == '*' && i+1 < len(rs) && rs[i+1] == '*':
			// bold: to the next ** on the line, else raw
			j := i + 2
			for j+1 < len(rs) && !(rs[j] == '*' && rs[j+1] == '*') {
				j++
			}
			if j+1 < len(rs) && j > i+2 {
				flush(base)
				out = append(out, seg{slot: SlotBold, text: string(rs[i+2 : j])})
				i = j + 2
			} else {
				cur.WriteString("**")
				i += 2
			}
		case (r == '*' || r == '_') && i+1 < len(rs) && rs[i+1] != ' ' && rs[i+1] != r:
			// emphasis: to the matching mark on the line, else raw
			j := i + 1
			for j < len(rs) && rs[j] != r {
				j++
			}
			if j < len(rs) && j > i+1 && rs[j-1] != ' ' {
				flush(base)
				out = append(out, seg{slot: SlotDim, text: string(rs[i+1 : j])})
				i = j + 1
			} else {
				cur.WriteRune(r)
				i++
			}
		default:
			cur.WriteRune(r)
			i++
		}
	}
	flush(base)
	if len(out) == 0 {
		return []seg{{slot: base, text: ""}}
	}
	return out
}
