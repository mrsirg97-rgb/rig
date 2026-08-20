package tui

import "strings"

func mdLine(th Theme, segs []seg) (out []seg, fence bool, info string) {

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
