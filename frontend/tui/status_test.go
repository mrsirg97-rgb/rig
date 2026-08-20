package tui_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/frontend/tui"
)

// The status line and the startup block (decision 3, amended): the
// block's two rows, dim, no rules; the live row's format and 70/90
// marks.
func statusInput() tui.StatusIn {
	return tui.StatusIn{
		Model: "huihui3.8", Effort: "xhigh",
		Window:  262144,
		Session: "2f9a1c0e77b34455deadbeef",
		Up:      214000, Down: 3200, CacheRead: 187000,
	}
}

func TestStatusBlockExactBytes(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderStatus(th, statusInput())
	want := th.Paint("dim", "welcome to") + "\n" +
		th.Paint("ember", "█▀▄ █ █▀▀") + "\n" +
		th.Paint("ember", "█▀▄ █ █ █") + "\n" +
		th.Paint("ember", "▀ ▀ ▀ ▀▀▀") + "\n" +
		th.Paint("dim", "session 2f9a1c0e77b3") + "\n" // the first twelve
	if got != want {
		t.Fatalf("the startup block:\ngot  %q\nwant %q", got, want)
	}
	// no dotted rules (they enclosed the deleted banner).
	if strings.Contains(got, "··") || strings.Contains(got, "···") {
		t.Fatalf("the block must not draw rules:\n%q", got)
	}
}

func TestStatusBlockRowsFitTheWidth(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderStatus(th, statusInput())
	for i, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if w := tui.WidthOf(line); w > 100 {
			t.Fatalf("block row %d overflows 100: %d: %q", i, w, line)
		}
	}
}

func TestStatusBlockNoEffortOmitsTheSegment(t *testing.T) {
	// the block carries no model row now (the status line does); the
	// case is the block without a session: the greeting alone, one row.
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	b := statusInput()
	b.Session = ""
	got := tui.RenderStatus(th, b)
	want := th.Paint("dim", "welcome to") + "\n" +
		th.Paint("ember", "█▀▄ █ █▀▀") + "\n" +
		th.Paint("ember", "█▀▄ █ █ █") + "\n" +
		th.Paint("ember", "▀ ▀ ▀ ▀▀▀") + "\n"
	if got != want {
		t.Fatalf("no session: the title alone:\n%q", got)
	}
}

func TestStatusLineModelAloneBeforeFirstUsage(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderStatusLine(th, "huihui3.8", "", "", "", 0, 262144, false, 0, 0, 0)
	want := th.Paint("text", "huihui3.8") +
		"\n" + th.Paint("dim", "default") + th.Paint("dim", " · ") + th.Paint("dim", "auto") +
		"\n" + th.Paint("dim", "up 0 down 0 · cache r 0 0%")
	if got != want {
		t.Fatalf("before the first usage: the model alone, the stance row, the zero totals:\ngot  %q\nwant %q", got, want)
	}
}

func TestStatusLineFormatAndMarks(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		used int
		want string // the slot the context part is painted with
	}{
		{84000, "dim"},    // 32%: quiet
		{140000, "warn"},  // 70%: the warn tier
		{180000, "error"}, // 90%: the error tier
	}
	for _, c := range cases {
		got := tui.RenderStatusLine(th, "huihui3.8", "", "", "", c.used, 200000, true, 214000, 3200, 187000)
		part := fmtTokens(c.used) + "/" + fmtTokens(200000)
		if !strings.Contains(got, th.Paint(c.want, part)) {
			t.Errorf("used=%d: the context part is not painted %s:\n%s", c.used, c.want, got)
		}
		if !strings.HasPrefix(got, th.Paint("text", "huihui3.8")+th.Paint("dim", " · ")) {
			t.Errorf("used=%d: the model part is not text (white), the dot dim:\n%s", c.used, got)
		}
		// the second row: the session's totals and the hit rate.
		if !strings.HasSuffix(got, "\n"+th.Paint("dim", "up 214k down 3.2k · cache r 187k 87%")) {
			t.Errorf("used=%d: the usage row is not the second row:\n%s", c.used, got)
		}
	}
}

func TestStatusLineEmptyModelIsEmpty(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := tui.RenderStatusLine(th, "", "", "", "", 100, 1000, true, 1, 1, 1); got != "" {
		t.Fatalf("no model, no row: %q", got)
	}
}

// fmtTokens: raw under 1000, one-decimal k under 10k, rounded k under
// 1M, else M — the CLI's formatTokens, mirrored here.
func fmtTokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1000000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
}

// TestStatusThreeRowShape (SPEC_MODES 3 and 4, amended): the status is
// three rows — model · context / effort · role · approve / usage — the
// effort in its ramp color, the role abbreviated, manual in the warn.
func TestStatusThreeRowShape(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderStatusLine(th, "huihui3.8", "xhigh", "architect", "manual", 41200, 262144, true, 214000, 3200, 187000)
	sep := th.Paint("dim", " · ")
	rows := strings.Split(got, "\n")
	if len(rows) != 3 {
		t.Fatalf("three rows, got %d:\n%q", len(rows), got)
	}
	if want := th.Paint("text", "huihui3.8") + sep + th.Paint("dim", "41k/262k"); rows[0] != want {
		t.Fatalf("row1 (identity):\ngot  %q\nwant %q", rows[0], want)
	}
	if want := th.Paint("effortXhigh", "xhigh") + sep + th.Paint("dim", "arch") + sep + th.Paint("warn", "manual"); rows[1] != want {
		t.Fatalf("row2 (stance):\ngot  %q\nwant %q", rows[1], want)
	}
	if !strings.Contains(rows[2], "up 214k") {
		t.Fatalf("row3 (usage):\n%q", rows[2])
	}
}

// TestStatusLineRoleAbbreviations (SPEC_MODES 3, amended): architect ->
// arch, reviewer -> rev, default (and the empty state) -> default — the
// stance always shows, in the stance row; auto rides dim beside it.
func TestStatusLineRoleAbbreviations(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ role, want string }{
		{"architect", "arch"}, {"reviewer", "rev"}, {"default", "default"}, {"", "default"},
	}
	for _, c := range cases {
		got := tui.RenderStatusLine(th, "huihui3.8", "", c.role, "", 41200, 262144, true, 214000, 3200, 187000)
		rows := strings.Split(got, "\n")
		want := th.Paint("dim", c.want) + th.Paint("dim", " · ") + th.Paint("dim", "auto")
		if len(rows) != 3 || rows[1] != want {
			t.Errorf("role %q: the stance row must be %q:\n%q", c.role, want, rows[1])
		}
	}
}

// TestStatusLineEffortColorsAndFallback (SPEC_MODES 3, amended): each
// ramp level paints its own slot (pane's footer colors); a level the
// ramp does not name paints accent; an empty effort drops the segment.
func TestStatusLineEffortColorsAndFallback(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ level, slot string }{
		{"off", "effortOff"}, {"minimal", "effortMinimal"}, {"low", "effortLow"},
		{"medium", "effortMedium"}, {"high", "effortHigh"}, {"xhigh", "effortXhigh"},
		{"max", "effortMax"}, {"galactic", "accent"},
	} {
		got := tui.RenderStatusLine(th, "huihui3.8", c.level, "", "", 41200, 262144, true, 214000, 3200, 187000)
		if !strings.Contains(got, th.Paint(c.slot, c.level)) {
			t.Errorf("level %q must paint %s:\n%s", c.level, c.slot, got)
		}
	}
	got := tui.RenderStatusLine(th, "huihui3.8", "", "", "", 41200, 262144, true, 214000, 3200, 187000)
	rows := strings.Split(got, "\n")
	if len(rows) != 3 || rows[1] != th.Paint("dim", "default")+th.Paint("dim", " · ")+th.Paint("dim", "auto") {
		t.Fatalf("an empty effort must drop the segment from the stance row:\n%q", got)
	}
}
