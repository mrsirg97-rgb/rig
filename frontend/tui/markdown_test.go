package tui

import (
	"strings"
	"testing"
)

// TestWrapSegsAtWords (decision 2, amended): prose breaks at spaces,
// the paint survives the break, a word wider than the width breaks at
// the edge, and an empty line is one empty row.
func TestWrapSegsAtWords(t *testing.T) {
	th := oledTheme(t)
	rows := wrapSegs(th, 12, []seg{{slot: SlotText, text: "streaming reasoning that wraps"}})
	want := []string{"streaming", "reasoning", "that wraps"}
	if len(rows) != len(want) {
		t.Fatalf("rows = %q, want %q", rows, want)
	}
	for i := range want {
		if RemoveColor(rows[i]) != want[i] {
			t.Fatalf("row %d = %q, want %q", i, RemoveColor(rows[i]), want[i])
		}
	}
	// the paint per piece survives: a mixed line keeps its slots.
	rows = wrapSegs(th, 10, []seg{{slot: SlotText, text: "plain "}, {slot: SlotEmber, text: "code word"}})
	if len(rows) != 2 || !strings.Contains(rows[1], th.Paint(SlotEmber, "word")) {
		t.Fatalf("the paint must survive the break: %q", rows)
	}
	// a word wider than the width: the edge break, the terminal's rule.
	rows = wrapSegs(th, 5, []seg{{slot: SlotText, text: "abcdefgh"}})
	if len(rows) != 2 || RemoveColor(rows[0]) != "abcde" || RemoveColor(rows[1]) != "fgh" {
		t.Fatalf("the wide word must break at the edge: %q", rows)
	}
	if rows := wrapSegs(th, 10, nil); len(rows) != 1 || rows[0] != "" {
		t.Fatalf("an empty line is one empty row: %q", rows)
	}
	// every row fits the width.
	for _, r := range wrapSegs(th, 7, []seg{{slot: SlotText, text: "one two three four five six"}}) {
		if displayWidth(RemoveColor(r)) > 7 {
			t.Fatalf("a row overflows the width: %q", r)
		}
	}
}

// TestMarkdownInline: the inline subset, exactly, and the raw cases.
func TestMarkdownInline(t *testing.T) {
	th := oledTheme(t)
	paint := func(segs []seg) string { return paintSegs(th, segs) }
	cases := []struct {
		in   string
		want string
	}{
		{"a **bold** word", th.Paint(SlotText, "a ") + th.Paint(SlotBold, "bold") + th.Paint(SlotText, " word")},
		{"an *em* word", th.Paint(SlotText, "an ") + th.Paint(SlotDim, "em") + th.Paint(SlotText, " word")},
		{"a `code` word", th.Paint(SlotText, "a ") + th.Paint(SlotEmber, "code") + th.Paint(SlotText, " word")},
		{"unclosed **bold", th.Paint(SlotText, "unclosed **bold")},
		{"unclosed `code", th.Paint(SlotText, "unclosed `code")},
		{"escaped \\*star\\*", th.Paint(SlotText, "escaped *star*")},
		{"2 * 3 * 4", th.Paint(SlotText, "2 * 3 * 4")},                                                      // spaces around: not emphasis
		{"a `b*c*d` e", th.Paint(SlotText, "a ") + th.Paint(SlotEmber, "b*c*d") + th.Paint(SlotText, " e")}, // marks inside code are text
	}
	for _, c := range cases {
		out, fence, _ := mdLine(th, []seg{{slot: SlotText, text: c.in}})
		if fence {
			t.Fatalf("%q: not a fence", c.in)
		}
		if got := paint(out); got != c.want {
			t.Errorf("%q:\n got %q\nwant %q", c.in, RemoveColor(got)+" | "+got, RemoveColor(c.want)+" | "+c.want)
		}
	}
}

// TestMarkdownBlocks: headings, quotes, lists, the fence toggle, and
// the width invariant (decoration changes paint, not width, except the
// list bullet and the quote bar which are one glyph for one mark).
func TestMarkdownBlocks(t *testing.T) {
	th := oledTheme(t)
	out, _, _ := mdLine(th, []seg{{slot: SlotText, text: "## Title here"}})
	if got := paintSegs(th, out); got != th.Paint(SlotAccent, "Title here") {
		t.Fatalf("heading = %q", got)
	}
	out, _, _ = mdLine(th, []seg{{slot: SlotText, text: "- an item"}})
	if got := RemoveColor(paintSegs(th, out)); got != th.Glyph(GlyphDot)+" an item" {
		t.Fatalf("bullet = %q", got)
	}
	out, _, _ = mdLine(th, []seg{{slot: SlotText, text: "  2. nested number"}})
	if got := RemoveColor(paintSegs(th, out)); got != "  2. nested number" {
		t.Fatalf("numbered = %q", got)
	}
	out, _, _ = mdLine(th, []seg{{slot: SlotText, text: "> a quote"}})
	if got := RemoveColor(paintSegs(th, out)); got != th.Glyph(GlyphBarOn)+" a quote" {
		t.Fatalf("quote = %q", got)
	}
	_, fence, info := mdLine(th, []seg{{slot: SlotText, text: "```go"}})
	if !fence || info != "go" {
		t.Fatalf("fence = %v %q", fence, info)
	}
	// reasoning is never decorated.
	out, _, _ = mdLine(th, []seg{{slot: SlotReasoning, text: "**not bold**"}})
	if got := paintSegs(th, out); got != th.Paint(SlotReasoning, "**not bold**") {
		t.Fatalf("reasoning must stay raw: %q", got)
	}
}
