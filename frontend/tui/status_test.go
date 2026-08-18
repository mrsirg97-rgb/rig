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
		Window: 262144,
		Up: 214000, Down: 3200, CacheRead: 187000,
	}
}

func TestStatusBlockExactBytes(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderStatus(th, statusInput())
	want := th.Paint("dim", "huihui3.8") + th.Paint("dim", " · ") + th.Paint("dim", "xhigh") + "\n" +
		th.Paint("dim", "up 214k down 3.2k · cache r 187k 87%") + "\n"
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
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	b := statusInput()
	b.Model, b.Effort = "local", ""
	got := tui.RenderStatus(th, b)
	if strings.Count(got, " · ") < 1 || strings.Contains(got, " ·  · ") {
		t.Fatalf("an empty effort must omit its segment without doubling the dot:\n%s", got)
	}
}

func TestStatusLineModelAloneBeforeFirstUsage(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderStatusLine(th, "huihui3.8", 0, 262144, false)
	if got != th.Paint("dim", "huihui3.8") {
		t.Fatalf("before the first usage the row is the model alone: %q", got)
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
		{84000, "dim"},   // 32%: quiet
		{140000, "warn"}, // 70%: the warn tier
		{180000, "error"}, // 90%: the error tier
	}
	for _, c := range cases {
		got := tui.RenderStatusLine(th, "huihui3.8", c.used, 200000, true)
		part := fmtTokens(c.used) + "/" + fmtTokens(200000)
		if !strings.Contains(got, th.Paint(c.want, part)) {
			t.Errorf("used=%d: the context part is not painted %s:\n%s", c.used, c.want, got)
		}
		if !strings.HasPrefix(got, th.Paint("dim", "huihui3.8 · ")) {
			t.Errorf("used=%d: the model part is not dim:\n%s", c.used, got)
		}
	}
}

func TestStatusLineEmptyModelIsEmpty(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := tui.RenderStatusLine(th, "", 100, 1000, true); got != "" {
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
