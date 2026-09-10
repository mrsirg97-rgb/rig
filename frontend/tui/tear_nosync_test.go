package tui

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/core"
)

type vtStream struct {
	width int
	rows  []string
	r, c  int
	err   string
	buf   []byte
}

func newVTStream(width int) *vtStream { return &vtStream{width: width} }

func (v *vtStream) fail(why string) {
	if v.err == "" {
		v.err = why
	}
}

func (v *vtStream) ensureRow(r int) {
	for len(v.rows) <= r {
		v.rows = append(v.rows, "")
	}
}

func (v *vtStream) writeRune(r rune) {
	if v.c > 0 && v.c == v.width {
		v.r++
		v.c = 0
	}
	v.ensureRow(v.r)
	rs := []rune(v.rows[v.r])
	if v.c < len(rs) {
		rs[v.c] = r
	} else {
		rs = append(rs, r)
	}
	v.rows[v.r] = string(rs)
	v.c++
}

func (v *vtStream) feed(b []byte) {
	v.buf = append(v.buf, b...)
	rest := 0
	incomplete := false
	i := 0
	for i < len(v.buf) {
		rest = i
		c := v.buf[i]
		if v.err != "" {
			return
		}
		switch {
		case c == '\n':
			v.r++
			v.ensureRow(v.r)
			if v.c > v.width {
				v.c = v.width
			}
			i++
		case c == 0x1b:
			if i+1 >= len(v.buf) {
				incomplete = true
				i = len(v.buf)
				continue
			}
			if v.buf[i+1] != '[' {
				v.fail("a bare escape: the vocabulary is CSI only")
				return
			}
			j := i + 2
			for j < len(v.buf) && !(v.buf[j] >= 0x40 && v.buf[j] <= 0x7e) {
				j++
			}
			if j >= len(v.buf) {
				incomplete = true
				i = len(v.buf)
				continue
			}
			params, term := string(v.buf[i+2:j]), v.buf[j]
			switch term {
			case 'A':
				n, aerr := atoi(params)
				if aerr != nil || n <= 0 {
					v.fail("cursor-up with n = " + params)
					return
				}
				if v.r < n {
					v.fail("cursor-up past the top of the screen")
					return
				}
				v.r -= n
			case 'B':
				n, aerr := atoi(params)
				if aerr != nil || n <= 0 {
					v.fail("cursor-down with n = " + params)
					return
				}
				v.r += n
				v.ensureRow(v.r)
			case 'G':
				n, aerr := atoi(params)
				if aerr != nil || n < 1 {
					v.fail("set-column with n = " + params)
					return
				}
				if n-1 > v.width {
					v.fail("set-column past the width")
					return
				}
				v.c = n - 1
			case 'K':
				v.ensureRow(v.r)
				switch params {
				case "2":
					v.rows[v.r] = ""
				case "":
					rs := []rune(v.rows[v.r])
					if v.c < len(rs) {
						v.rows[v.r] = string(rs[:v.c])
					}
				default:
					v.fail("an unknown clear mode: " + params + "K")
					return
				}
			case 'J':
				if params != "0" && params != "" {
					v.fail("an unknown erase mode: " + params + "J")
					return
				}
				v.ensureRow(v.r)
				rr := []rune(v.rows[v.r])
				if v.c < len(rr) {
					v.rows[v.r] = string(rr[:v.c])
				}
				v.rows = v.rows[:v.r+1]
			case 'm':
			case 'h', 'l':
				if params != "?2026" {
					v.fail("a mode outside the vocabulary: " + params + string(term))
					return
				}
			default:
				v.fail("an escape outside the vocabulary: " + string(term))
				return
			}
			i = j + 1
		default:
			if !utf8.FullRune(v.buf[i:]) {
				incomplete = true
				i = len(v.buf)
				continue
			}
			r, size := utf8.DecodeRune(v.buf[i:])
			if r == utf8.RuneError && size == 1 {
				v.fail("an invalid or orphaned UTF-8 byte")
				return
			}
			v.writeRune(r)
			i += size
		}
	}
	if incomplete {
		v.buf = append(v.buf[:0], v.buf[rest:]...)
	} else {
		v.buf = v.buf[:0]
	}
}

func TestTearNoSyncPromptNeverBlanks(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(24),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}

	runes := []rune("first reasoning line that wraps across the edge of the terminal and keeps going to force a wrap")
	for i, r := range runes {
		s.fe.Notify(core.ReasoningDelta{Text: string(r)})
		if i%7 == 0 {
			s.tick()
		}
	}
	s.fe.Notify(core.ReasoningDelta{Text: " second reasoning line that also wraps around\n"})
	s.tick()
	var big strings.Builder
	for i := 0; i < 40; i++ {
		big.WriteString("line ")
		big.WriteString(strings.Repeat("x", 60))
		big.WriteString(" ends here\n")
	}
	s.fe.Notify(core.ReasoningDelta{Text: big.String()})
	s.tick()
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	stable := 0
	for {
		n := len(s.out.Bytes())
		time.Sleep(2 * time.Millisecond)
		if len(s.out.Bytes()) == n {
			stable++
			if stable >= 25 {
				break
			}
		} else {
			stable = 0
		}
	}

	v := newVTStream(24)
	stream := s.out.Bytes()
	seen := false
	const split = 9
	for off := 0; off < len(stream); off += split {
		end := off + split
		if end > len(stream) {
			end = len(stream)
		}
		v.feed(stream[off:end])
		if v.err != "" {
			t.Fatalf("harness: %s\nstream:\n%s", v.err, s.out.String())
		}
		present := false
		for _, r := range v.rows {
			if strings.Contains(r, "❯") {
				present = true
				break
			}
		}
		if present {
			seen = true
		}
		if seen && !present {
			from := off - 60
			if from < 0 {
				from = 0
			}
			t.Fatalf("the prompt vanished at byte %d:\n%s\nstream tail:\n%q", off,
				strings.Join(v.rows, "\n"), stream[from:end])
		}
	}
}

func TestTearNoSyncPairIsOneWrite(t *testing.T) {
	th := oledTheme(t)
	out := &lockBuf{}
	l := newLive(out, 40)
	l.draw(th.Paint(SlotText, "hello"), []string{"tail"}, "")

	out.mu.Lock()
	defer out.mu.Unlock()
	if len(out.writes) != 1 {
		t.Fatalf("one flush must be one write: %d writes", len(out.writes))
	}
	chunk := out.b.String()
	if !strings.HasPrefix(chunk, syncOn) || !strings.HasSuffix(chunk, syncOff) {
		t.Fatalf("the sync pair must close the frame's write: %q", chunk)
	}
}
