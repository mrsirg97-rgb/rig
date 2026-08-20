package diff

import (
	"strconv"
	"strings"
)

const contextLines = 3

type line struct {
	text string
	eof  bool
}

func lines(s string) []line {
	if s == "" {
		return nil
	}
	eof := !strings.HasSuffix(s, "\n")
	texts := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	out := make([]line, len(texts))
	for i, t := range texts {
		out[i] = line{text: t, eof: eof && i == len(texts)-1}
	}
	return out
}

type tok struct {
	kind byte
	idx  int
}

func script(a, b []line) []tok {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = 1 + dp[i+1][j+1]
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []tok
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, tok{'m', i})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, tok{'d', i})
			i++
		default:
			out = append(out, tok{'i', j})
			j++
		}
	}
	for i < n {
		out = append(out, tok{'d', i})
		i++
	}
	for j < m {
		out = append(out, tok{'i', j})
		j++
	}
	return out
}

type hunk struct{ from, to int }

func hunks(toks []tok) []hunk {
	var out []hunk
	for k := 0; k < len(toks); {
		for k < len(toks) && toks[k].kind == 'm' {
			k++
		}
		if k == len(toks) {
			break
		}
		to := k
		for to < len(toks) && toks[to].kind != 'm' {
			to++
		}
		from := k
		for c := 0; c < contextLines && from > 0 && toks[from-1].kind == 'm'; c++ {
			from--
		}
		for c := 0; c < contextLines && to < len(toks) && toks[to].kind == 'm'; c++ {
			to++
		}
		if len(out) > 0 && out[len(out)-1].to >= from {
			out[len(out)-1].to = to
		} else {
			out = append(out, hunk{from, to})
		}
		k = to
	}
	return out
}

func side(start, count int) string {
	if count == 0 {
		return strconv.Itoa(start) + ",0"
	}
	if count == 1 {
		return strconv.Itoa(start + 1)
	}
	return strconv.Itoa(start+1) + "," + strconv.Itoa(count)
}

func Diff(old, new string, oldLabel, newLabel string) string {
	a, b := lines(old), lines(new)
	toks := script(a, b)
	hs := hunks(toks)
	if len(hs) == 0 {
		return ""
	}
	oldc := make([]int, len(toks)+1)
	newc := make([]int, len(toks)+1)
	for k, tk := range toks {
		oldc[k+1], newc[k+1] = oldc[k], newc[k]
		if tk.kind == 'm' || tk.kind == 'd' {
			oldc[k+1]++
		}
		if tk.kind == 'm' || tk.kind == 'i' {
			newc[k+1]++
		}
	}
	var sb strings.Builder
	sb.WriteString("--- " + oldLabel + "\n")
	sb.WriteString("+++ " + newLabel + "\n")
	for _, h := range hs {
		sb.WriteString("@@ -" + side(oldc[h.from], oldc[h.to]-oldc[h.from]) +
			" +" + side(newc[h.from], newc[h.to]-newc[h.from]) + " @@\n")
		for k := h.from; k < h.to; k++ {
			prefix, ln := "", line{}
			switch toks[k].kind {
			case 'd':
				prefix, ln = "-", a[toks[k].idx]
			case 'i':
				prefix, ln = "+", b[toks[k].idx]
			default:
				prefix, ln = " ", a[toks[k].idx]
			}
			sb.WriteString(prefix + ln.text + "\n")
			if ln.eof {
				sb.WriteString("\\ No newline at end of file\n")
			}
		}
	}
	return strings.TrimSuffix(sb.String(), "\n")
}
