package view

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func pageLine(page, title string) string {
	if title == "" {
		return "page " + page
	}
	return "page " + page + " — " + title
}

func list(els []element) string {
	var b strings.Builder
	shown := els
	if len(shown) > maxElements {
		shown = shown[:maxElements]
	}
	for i, e := range shown {
		fmt.Fprintf(&b, "[%d] %s \"%s\"\n", i+1, plain(e.Role, maxName), plain(e.Name, maxName))
	}
	switch {
	case len(els) == 0:
		b.WriteString("no interactive elements")
	case len(els) > maxElements:
		fmt.Fprintf(&b, "%d interactive elements, %d more not listed", len(els), len(els)-maxElements)
	default:
		fmt.Fprintf(&b, "%d interactive elements", len(els))
	}
	return b.String()
}

func plain(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		b.WriteRune(r)
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.ReplaceAll(out, "[[rig:", "[[ rig:")
	if utf8.RuneCountInString(out) > max {
		runes := []rune(out)
		out = string(runes[:max]) + "…"
	}
	return out
}
