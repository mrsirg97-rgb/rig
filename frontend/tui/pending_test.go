package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

func TestPendingTailScrollsByRows(t *testing.T) {
	th := oledTheme(t)
	const width, height = 30, 14
	s := newScriptedSession(t, th, WithWidth(width), WithSize(sizeFixture(width, height)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt returned %q", got)
	}

	v := newVTScreen(width, height)
	painted := 0
	feed := func() {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			chunks := s.out.writeChunks()
			if len(chunks) > painted {
				for ; painted < len(chunks); painted++ {
					v.feed([]byte(chunks[painted]))
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("a frame never landed")
			}
			time.Sleep(time.Millisecond)
		}
		if v.err != "" {
			t.Fatalf("harness: %s", v.err)
		}
		if v.clamped > 0 {
			t.Fatalf("the protocol relied on %d cursor clamps", v.clamped)
		}
	}
	feed()

	words := []string{"alpha", "beta", "gamma", "delta"}
	known := map[string]bool{}
	for _, w := range words {
		known[w] = true
	}
	para := strings.TrimSpace(strings.Repeat(strings.Join(words, " ")+" ", 60))
	if len(para) < 640 {
		t.Fatalf("the fixture paragraph is %d chars, want 640+", len(para))
	}

	type frame struct {
		hidden int
		tail   []string
	}
	tailOf := func() frame {
		t.Helper()
		rows := v.rows
		marker := -1
		for i, r := range rows {
			if strings.Contains(paintFree(r), "lines hidden") {
				marker = i
				break
			}
		}
		if marker < 0 {
			t.Fatalf("the pending block is not capped:\n%q", rows)
		}
		m := paintFree(rows[marker])
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(m, "· "), " lines hidden ·"))
		if err != nil {
			t.Fatalf("the marker is not parseable: %q", m)
		}
		var tail []string
		for i := marker + 1; i < len(rows) && rows[i] != ""; i++ {
			tail = append(tail, paintFree(rows[i]))
		}
		return frame{hidden: n, tail: tail}
	}
	wrapped := func(text string) []string {
		rows := wrapSegs(th, width, []seg{{slot: SlotText, text: text}})
		for i, r := range rows {
			rows[i] = paintFree(r)
		}
		return rows
	}
	checkFrame := func(label, text string, f frame) []string {
		t.Helper()
		want := wrapped(text)
		if f.hidden+len(f.tail) != len(want) {
			t.Fatalf("%s: %d hidden + %d visible = %d rows, want the %d-row wrap", label,
				f.hidden, len(f.tail), f.hidden+len(f.tail), len(want))
		}
		if !slices.Equal(f.tail, want[len(want)-len(f.tail):]) {
			t.Fatalf("%s: the tail is not the wrap's last rows:\n%q\nwant %q", label, f.tail, want[len(want)-len(f.tail):])
		}
		for i, row := range f.tail {
			tokens := strings.Fields(row)
			last := len(tokens)
			if i == len(f.tail)-1 {
				last--
			}
			for j := 0; j < last; j++ {
				if !known[tokens[j]] {
					t.Fatalf("%s: row %d breaks inside a word: %q (tokens %q)", label, i, row, tokens)
				}
			}
		}
		return want
	}

	head := para[:600]
	s.fe.Notify(core.TextDelta{Text: head})
	s.tick()
	feed()
	prev := tailOf()
	checkFrame("the head frame", head, prev)

	for off := 600; off < len(para); off += 4 {
		end := off + 4
		if end > len(para) {
			end = len(para)
		}
		s.fe.Notify(core.TextDelta{Text: para[off:end]})
		s.tick()
		feed()
		text := para[:end]
		cur := tailOf()
		checkFrame("offset "+strconv.Itoa(off), text, cur)
		for i := 0; i < len(cur.tail)-1; i++ {
			if cur.tail[i] == prev.tail[i] {
				continue
			}
			if i+1 < len(prev.tail) && cur.tail[i] == prev.tail[i+1] {
				continue
			}
			if i+1 == len(prev.tail)-1 &&
				(strings.HasPrefix(cur.tail[i], prev.tail[i+1]) ||
					strings.HasPrefix(prev.tail[i+1], cur.tail[i])) {
				continue
			}
			t.Fatalf("offset %d: row %d changed without a scroll: %q -> %q\nprev %q\ncur %q",
				off, i, prev.tail[i], cur.tail[i], prev.tail, cur.tail)
		}
		prev = cur
	}
}

func TestPendingTailGuardsExactWidthRows(t *testing.T) {
	th := oledTheme(t)
	const width, height = 20, 14
	s := newScriptedSession(t, th, WithWidth(width), WithSize(sizeFixture(width, height)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt returned %q", got)
	}
	v := newVTScreen(width, height)
	painted := 0
	feed := func() {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			chunks := s.out.writeChunks()
			if len(chunks) > painted {
				for ; painted < len(chunks); painted++ {
					v.feed([]byte(chunks[painted]))
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("a frame never landed")
			}
			time.Sleep(time.Millisecond)
		}
		if v.err != "" {
			t.Fatalf("harness: %s", v.err)
		}
		if v.clamped > 0 {
			t.Fatalf("the protocol relied on %d cursor clamps", v.clamped)
		}
	}
	feed()

	// a no-newline stream of a wide word: every wrapped row is exactly
	// width wide, so the tail's rows end at the last column.
	const word = "abcdefghij"
	para := strings.Repeat(word, 90)
	s.fe.Notify(core.TextDelta{Text: para[:400]})
	s.tick()
	feed()
	for off := 400; off < len(para); off += 5 {
		s.fe.Notify(core.TextDelta{Text: para[off : off+5]})
		s.tick()
		feed()
		rows := v.rows
		marker := -1
		for i, r := range rows {
			if strings.Contains(paintFree(r), "lines hidden") {
				marker = i
				break
			}
		}
		if marker < 0 {
			t.Fatalf("the pending block is not capped:\n%q", rows)
		}
		var tail []string
		for i := marker + 1; i < len(rows) && rows[i] != ""; i++ {
			tail = append(tail, paintFree(rows[i]))
		}
		for i, row := range tail {
			if displayWidth(row) > width {
				t.Fatalf("offset %d: row %d overflows the width: %q", off, i, row)
			}
			if i < len(tail)-1 && displayWidth(row) != width {
				t.Fatalf("offset %d: a laid row %d is %d wide, want the full %d (the exact-width guard)",
					off, i, displayWidth(row), width)
			}
			if !strings.Contains(strings.Repeat(word, len(row)/len(word)+2), row) {
				t.Fatalf("offset %d: row %d is not a clean slice of the stream: %q", off, i, row)
			}
		}
	}
}
