// The engine (SPEC_DIFF decision 2): two strings in, a unified diff out
// in git's layout (context 3), the empty string when identical. Pure.
//
// A plain LCS table: dp[i][j] is the LCS of the suffixes a[i:] and b[j:],
// filled in O(N*M) and walked back into the edit script. O(N*M) is
// deliberate: the inputs are tool results (KBs) and the reply is capped
// at 100 lines, so the table is bounded by the bound the cap already
// imposes.
package diff

import (
	"strconv"
	"strings"
)

const contextLines = 3

// line is one record: its text plus whether it ends the string without a
// trailing newline. The absence of the newline is part of the record, so
// "foo" and "foo\n" are different lines.
type line struct {
	text string
	eof  bool
}

// lines splits a string into records; a trailing newline is not a record.
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

// tok is one step of the edit script: 'm' match, 'd' delete (a record of
// the old), 'i' insert (a record of the new); idx indexes the side the
// step consumes.
type tok struct {
	kind byte
	idx  int
}

// script fills the LCS table and walks it back into the edit script. Ties
// break to the delete (the move that keeps the old index), which is what
// puts deletions before insertions in the output — git's order.
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

// hunk is a contiguous slice of the script, expanded by the context.
type hunk struct{ from, to int }

// hunks groups the script's edits: each maximal run holding an edit,
// expanded by contextLines records on each side, and merged with the next
// when the two contexts touch (a gap of at most 2*contextLines records).
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

// side renders one side of a @@ header: the 1-based start and the count,
// the count omitted when 1, a zero-count side keeping ",0" with the start
// the record before the edit (git's rule).
func side(start, count int) string {
	if count == 0 {
		return strconv.Itoa(start) + ",0"
	}
	if count == 1 {
		return strconv.Itoa(start + 1)
	}
	return strconv.Itoa(start+1) + "," + strconv.Itoa(count)
}

// Diff is the engine's public face (decision 7): two strings, two labels,
// a unified diff — the empty string when identical.
func Diff(old, new string, oldLabel, newLabel string) string {
	a, b := lines(old), lines(new)
	toks := script(a, b)
	hs := hunks(toks)
	if len(hs) == 0 {
		return ""
	}
	oldc := make([]int, len(toks)+1) // old records consumed before each token
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
