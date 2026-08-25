package tui_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/frontend/tui"
)

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
		th.Paint("dim", "session 2f9a1c0e77b3") + "\n" +
		th.Paint("dim", "workers: none") + "\n" +
		th.Paint("dim", "chat with your model, or type / for commands") + "\n"
	if got != want {
		t.Fatalf("the startup block:\ngot  %q\nwant %q", got, want)
	}

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
	b.Session = ""
	got := tui.RenderStatus(th, b)
	want := th.Paint("dim", "welcome to") + "\n" +
		th.Paint("ember", "█▀▄ █ █▀▀") + "\n" +
		th.Paint("ember", "█▀▄ █ █ █") + "\n" +
		th.Paint("ember", "▀ ▀ ▀ ▀▀▀") + "\n" +
		th.Paint("dim", "workers: none") + "\n" +
		th.Paint("dim", "chat with your model, or type / for commands") + "\n"
	if got != want {
		t.Fatalf("no session: the title, the fleet, the hint:\n%q", got)
	}
}

func TestStatusLineModelAloneBeforeFirstUsage(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderStatusLine(th, "huihui3.8", "", "", "", 0, 262144, false, 0, 0, 0)
	want := th.Paint("text", "huihui3.8") +
		"\n" + th.Paint("dim", "default") + th.Paint("dim", " · ") + th.Paint("warn", "auto") +
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
		want string
	}{
		{84000, "dim"},
		{140000, "warn"},
		{180000, "error"},
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
		want := th.Paint("dim", c.want) + th.Paint("dim", " · ") + th.Paint("warn", "auto")
		if len(rows) != 3 || rows[1] != want {
			t.Errorf("role %q: the stance row must be %q:\n%q", c.role, want, rows[1])
		}
	}
}

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
	if len(rows) != 3 || rows[1] != th.Paint("dim", "default")+th.Paint("dim", " · ")+th.Paint("warn", "auto") {
		t.Fatalf("an empty effort must drop the segment from the stance row:\n%q", got)
	}
}

func TestStatusLineNamesTheFleetModel(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderStatusLine(th, "huihui3.8", "", "", "", 41200, 262144, true, 214000, 3200, 187000)
	rows := strings.Split(got, "\n")
	want := th.Paint("dim", "default") + th.Paint("dim", " · ") + th.Paint("warn", "auto")
	if len(rows) != 3 || rows[1] != want {
		t.Fatalf("the stance row must name the fleet's model:\ngot  %q\nwant %q", rows[1], want)
	}
}
