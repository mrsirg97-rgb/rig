package tui_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/frontend/tui"
)

// The banner (decision 3): two rows, enclosed in dotted rules, lowercase.
func bannerInput() tui.BannerIn {
	return tui.BannerIn{
		Model: "huihui3.8", Effort: "xhigh",
		Used: 84300, Window: 262144,
		Up: 214000, Down: 3200, CacheRead: 187000,
	}
}

func TestBannerExactBytesAtFifty(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderBanner(th, bannerInput(), 50)
	rule := th.Paint("rule", strings.Repeat("·", 50))
	want := rule + "\n" +
		th.Paint("accent", "huihui3.8") + th.Paint("dim", " · ") + th.Paint("dim", "xhigh") + th.Paint("dim", " · ") + th.Paint("dim", "84k/262k 32%") + "\n" +
		th.Paint("dim", "up 214k down 3.2k · cache r 187k 87%") + "\n" +
		rule + "\n"
	if got != want {
		t.Fatalf("banner at 50:\ngot  %q\nwant %q", got, want)
	}
}

func TestBannerExactBytesAtOneHundred(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderBanner(th, bannerInput(), 100)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("the banner is four lines (rule, two rows, rule), got %d", len(lines))
	}
	// the rules span the full width; the rows are the content, at most
	// the width (the terminal's wrap is not ours to own).
	for i, line := range []string{lines[0], lines[3]} {
		if width := tui.WidthOf(line); width != 100 {
			t.Fatalf("rule line %d width = %d, want 100: %q", i, width, line)
		}
	}
	for _, line := range lines[1:3] {
		if width := tui.WidthOf(line); width > 100 {
			t.Fatalf("banner row overflows the width: %d: %q", width, line)
		}
	}
}

func TestBannerColorsAtTheSeventyNinetyMarks(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		used int
		want string // the slot the context part is painted with
	}{
		{100000, "dim"},   // 50%: quiet
		{140000, "warn"},  // 70%: the warn tier
		{180000, "error"}, // 90%: the error tier
	}
	for _, c := range cases {
		b := bannerInput()
		b.Used, b.Window = c.used, 200000
		got := tui.RenderBanner(th, b, 50)
		if !strings.Contains(got, th.Paint(c.want, contextPart(b))) {
			t.Errorf("used=%d: the context part is not painted %s:\n%s", c.used, c.want, got)
		}
	}
}

// contextPart is the row-1 context segment, the test's mirror of the
// renderer's format (formatTokens is the CLI's shaping, mirrored here).
func contextPart(b tui.BannerIn) string {
	return fmtTokens(b.Used) + "/" + fmtTokens(b.Window) + " " + strconv.Itoa(b.Used*100/b.Window) + "%"
}

// fmtTokens: raw under 1000, one-decimal k under 10k, rounded k under
// 1M, else M — the CLI's formatTokens, byte-identical.
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

func TestBannerNoEffortOmitsTheSegment(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	b := bannerInput()
	b.Model, b.Effort = "local", ""
	got := tui.RenderBanner(th, b, 50)
	if strings.Count(got, " · ") < 1 || strings.Contains(got, " ·  · ") {
		t.Fatalf("an empty effort must omit its segment without doubling the dot:\n%s", got)
	}
}
