package tui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestPagerFrameGeometry(t *testing.T) {
	th := oledTheme(t)
	p := newPager([]string{"one", "two", "three", "four", "five"}, 10, 4)
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

	p2 := newPager([]string{"aaaaaaaaaaaaaaaaaaaa", "tail"}, 10, 4)
	f = RemoveColor(p2.frame(th))
	if !strings.Contains(f, "aaaa") || !strings.Contains(f, "tail") {
		t.Fatalf("the wrapped frame = %q, want both lines (3 rows fit)", f)
	}

	p3 := newPager([]string{"aaaaaaaaaaaaaaaaaaaa", "mid", "tail"}, 10, 4)
	f = RemoveColor(p3.frame(th))
	if strings.Contains(f, "aaaa") || !strings.Contains(f, "mid") {
		t.Fatalf("the tight frame = %q, want the wrapped line dropped whole", f)
	}
}

func TestLiveSuspendResume(t *testing.T) {
	var buf bytes.Buffer
	l := newLive(&buf, 20)
	l.redraw([]string{"input"})
	l.draw("before", []string{"input"}, "")
	l.suspend()
	pre := buf.Len()
	l.draw("during", []string{"input2"}, "")
	l.edit("input2x", 3, "")
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

func TestPagerPagesCoverEveryLine(t *testing.T) {
	th := oledTheme(t)
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i+1)
	}
	for i := 19; i <= 26; i++ {
		lines[i] = lines[i] + strings.Repeat("x", 70-len(lines[i]))
	}
	p := newPager(lines, 52, 24)
	p.footer = []string{"f1", "f2", "f3", "f4", "f5"}

	var pages [][]string
	for {
		page := p.frameLines()
		pages = append(pages, page)
		f := RemoveColor(p.frame(th))
		for _, line := range page {
			if !strings.Contains(f, line) {
				t.Fatalf("page %d renders %q without %s", len(pages)-1, f, line)
			}
		}
		for _, line := range lines {
			if !containsLine(page, line) && strings.Contains(f, line) {
				t.Fatalf("page %d renders %s outside its frame", len(pages)-1, line)
			}
		}
		if p.offset == len(p.lines)-1 {
			break
		}
		p.move(p.pageUp())
	}

	seen := map[string]bool{}
	for _, page := range pages {
		for _, line := range page {
			seen[line] = true
		}
	}
	if len(seen) != len(lines) {
		t.Fatalf("the pages cover %d lines, want all %d", len(seen), len(lines))
	}
	for i := 0; i+1 < len(pages); i++ {
		shared := []string{}
		for _, line := range pages[i] {
			if containsLine(pages[i+1], line) {
				shared = append(shared, line)
			}
		}
		if len(shared) != 1 {
			t.Fatalf("pages %d and %d share %v, want exactly one line", i, i+1, shared)
		}
	}
}

func containsLine(lines []string, s string) bool {
	for _, line := range lines {
		if line == s {
			return true
		}
	}
	return false
}
