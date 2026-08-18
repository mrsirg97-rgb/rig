package tui

import (
	"bytes"
	"strings"
	"testing"
)

// TestPagerFrameGeometry: the view is bottom-anchored, whole lines
// only, a wrapped line counts its rows, and the offset clamps at the
// document's edges.
func TestPagerFrameGeometry(t *testing.T) {
	th := oledTheme(t)
	p := newPager([]string{"one", "two", "three", "four", "five"}, 10, 4) // budget 3
	f := RemoveColor(p.frame(th))
	if !strings.Contains(f, "five") || !strings.Contains(f, "three") || strings.Contains(f, "two") {
		t.Fatalf("the tail frame = %q, want three..five", f)
	}
	if !p.move(2) {
		t.Fatal("move up must report the change")
	}
	f = RemoveColor(p.frame(th))
	if !strings.Contains(f, "three") || strings.Contains(f, "four") {
		t.Fatalf("the scrolled frame = %q, want one..three", f)
	}
	p.move(100)
	if p.offset != 4 {
		t.Fatalf("the offset clamps at %d, want 4 (the oldest line visible)", p.offset)
	}
	p.move(-100)
	if p.offset != 0 {
		t.Fatalf("the offset floor = %d, want 0", p.offset)
	}
	// a wrapped line: 20 cols at width 10 is two rows of the budget.
	p2 := newPager([]string{"aaaaaaaaaaaaaaaaaaaa", "tail"}, 10, 4)
	f = RemoveColor(p2.frame(th))
	if !strings.Contains(f, "aaaa") || !strings.Contains(f, "tail") {
		t.Fatalf("the wrapped frame = %q, want both lines (3 rows fit)", f)
	}
	// one row short: the wrapped line no longer fits above the tail.
	p3 := newPager([]string{"aaaaaaaaaaaaaaaaaaaa", "mid", "tail"}, 10, 4)
	f = RemoveColor(p3.frame(th))
	if strings.Contains(f, "aaaa") || !strings.Contains(f, "mid") {
		t.Fatalf("the tight frame = %q, want the wrapped line dropped whole", f)
	}
}

// TestLiveSuspendResume: a suspended region keeps its bookkeeping and
// writes nothing; the resume replays what committed meanwhile and
// re-emits the current region. The history records committed lines
// throughout.
func TestLiveSuspendResume(t *testing.T) {
	var buf bytes.Buffer
	l := newLive(&buf, 20)
	l.redraw([]string{"input"})
	l.draw("before", []string{"input"})
	l.suspend()
	pre := buf.Len()
	l.draw("during", []string{"input2"})
	l.edit("input2x", 3)
	if buf.Len() != pre {
		t.Fatalf("the suspended region wrote %q", buf.String()[pre:])
	}
	l.resume()
	post := buf.String()[pre:]
	if !strings.Contains(post, "during") || !strings.Contains(post, "input2x") {
		t.Fatalf("the resume = %q, want the queued commit and the current region", post)
	}
	if len(l.hist) != 2 || l.hist[0] != "before" || l.hist[1] != "during" {
		t.Fatalf("the history = %q, want [before during]", l.hist)
	}
}
