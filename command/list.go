package command

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	markIdle   = "[ ]"
	markActive = "[~]"
	markDone   = "[x]"
	markFailed = "[!]"
	headRow    = 1
	footerRow  = 1
	listAll    = "all"
)

func row(id string, width int, mark, text string, details ...string) string {
	var b strings.Builder
	b.WriteString("  ")
	if width > len(id) {
		b.WriteString(id + strings.Repeat(" ", width-len(id)))
	} else {
		b.WriteString(id)
	}
	if mark != "" {
		b.WriteString(" " + mark)
	}
	if text != "" {
		b.WriteString(" " + text)
	}
	for _, d := range details {
		if d != "" {
			b.WriteString(" · " + d)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	if strings.HasSuffix(one, "y") {
		return strconv.Itoa(n) + " " + strings.TrimSuffix(one, "y") + "ies"
	}
	return strconv.Itoa(n) + " " + one + "s"
}

func listLimit(e *Env, fields []string) (int, error) {
	if len(fields) > 0 {
		if fields[0] == listAll {
			return 0, nil
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil || n < 1 {
			return 0, fmt.Errorf("%q is not a count (all, or a positive number)", fields[0])
		}
		return n, nil
	}
	if e == nil || e.Lines == nil {
		return 0, nil
	}
	n := e.Lines() - headRow - footerRow
	if n < 1 {
		n = 1
	}
	return n, nil
}

func fit(total, limit int) (shown int, hidden int) {
	if limit <= 0 || total <= limit {
		return total, 0
	}
	return limit, total - limit
}

func moreFooter(hidden int, verb string) string {
	if hidden <= 0 {
		return ""
	}
	return fmt.Sprintf("\n· %d more · %s", hidden, verb)
}

func choices(head, active string, names []string, descs map[string]string) string {
	w := 0
	for _, n := range names {
		if len(n) > w {
			w = len(n)
		}
	}
	var b strings.Builder
	b.WriteString(head + " \u00b7 active " + active)
	for _, n := range names {
		mark := ""
		if n == active {
			mark = markActive
		}
		b.WriteString("\n" + row(n, w, mark, descs[n]))
	}
	return b.String()
}

func detail(head string, facts [][2]string) string {
	w := 0
	for _, f := range facts {
		if len(f[0]) > w {
			w = len(f[0])
		}
	}
	var b strings.Builder
	b.WriteString(head)
	for _, f := range facts {
		b.WriteString("\n" + row(f[0], w, "", f[1]))
	}
	return b.String()
}
