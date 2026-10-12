package tui

import "strings"

func printable(s string) string {
	clean := true
	for _, r := range s {
		if control(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == 0x1b {
			i = escapeEnd(rs, i)
			continue
		}
		if !control(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func control(r rune) bool {
	return (r < 0x20 && r != '\n' && r != '\t') || (r >= 0x7f && r < 0xa0)
}

func escapeEnd(rs []rune, i int) int {
	j := i + 1
	if j >= len(rs) {
		return i
	}
	switch rs[j] {
	case '[':
		for j++; j < len(rs) && rs[j] >= 0x20 && rs[j] <= 0x3f; j++ {
		}
		if j < len(rs) && rs[j] >= 0x40 && rs[j] <= 0x7e {
			return j
		}
		return j - 1
	case ']', 'P', 'X', '^', '_':
		for j++; j < len(rs); j++ {
			if rs[j] == 0x07 {
				return j
			}
			if rs[j] == 0x1b && j+1 < len(rs) && rs[j+1] == '\\' {
				return j + 1
			}
			if rs[j] == '\n' {
				return j - 1
			}
		}
		return len(rs) - 1
	}
	for ; j < len(rs) && rs[j] >= 0x20 && rs[j] <= 0x2f; j++ {
	}
	if j < len(rs) && rs[j] >= 0x30 && rs[j] <= 0x7e {
		return j
	}
	return j - 1
}
