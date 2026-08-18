package tui_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/frontend/tui"
)

// --- the usage line (decision 2: TurnEnd, pane's shaping) ---

func TestUsageLineExact(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderUsage(th, 3200, 136, 918)
	want := th.Paint("dim", "up 3.2k down 136 · cache r 918 28%")
	if got != want {
		t.Fatalf("usage line = %q, want %q", got, want)
	}
	// zero usage: the line still commits (the CLI's TurnEnd discipline),
	// hit 0, no division by zero.
	if got := tui.RenderUsage(th, 0, 0, 0); got != th.Paint("dim", "up 0 down 0 · cache r 0 0%") {
		t.Fatalf("zero usage = %q", got)
	}
}

// --- the compact line (decision 2; the CLI's voice, pane's shaping) ---

func TestCompactedLineExact(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	ev := core.Compacted{Dropped: 1200, Kept: 40000, Usage: core.Usage{Prompt: 2000, Completion: 1000}}
	got := tui.RenderCompacted(th, ev)
	want := th.Paint("accent", "⧉") + " " + th.Paint("dim", "compact: -1.2k kept 40k · summary up 2.0k down 1.0k")
	if got != want {
		t.Fatalf("compacted line = %q, want %q", got, want)
	}
	as, _ := tui.ResolveTheme("oled", json.RawMessage(`{"base":"oled","glyphs":"ascii"}`), true)
	if got := tui.RenderCompacted(as, ev); !strings.HasPrefix(got, as.Paint("accent", "=")+" ") {
		t.Fatalf("the ascii compact glyph is the = set: %q", got)
	}
}

// --- the fault line ---

func TestFaultLine(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderFault(th, errorsNew("context length exceeded"))
	want := th.Paint("error", "✕ fault: context length exceeded")
	if got != want {
		t.Fatalf("fault line = %q, want %q", got, want)
	}
}

func errorsNew(s string) error { return errString(s) }

type errString string

func (e errString) Error() string { return string(e) }

// --- the tool block (decision 4) ---

func bashArgs() json.RawMessage {
	return json.RawMessage(`{"command":"go test ./middleware/\n-v"}`)
}

func TestToolBlockHeadTailElided(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for i := 1; i <= 10; i++ {
		if i > 1 {
			body.WriteString("\n")
		}
		body.WriteString("line" + itoa(i))
	}
	got := tui.RenderToolBlock(th, "bash", bashArgs(), body.String()+"\n", false, 400*time.Millisecond)
	lines := strings.Split(got, "\n")
	// head six, the loud elided marker, tail two, then the close line.
	want := []string{
		th.Paint("accent", "●") + " " + th.Paint("accent", "bash") + th.Paint("dim", " · ") + th.Paint("text", "$ go test ./middleware/"),
		th.Paint("dim", "  line1"), th.Paint("dim", "  line2"), th.Paint("dim", "  line3"),
		th.Paint("dim", "  line4"), th.Paint("dim", "  line5"), th.Paint("dim", "  line6"),
		th.Paint("dim", "  · 2 lines elided ·"),
		th.Paint("dim", "  line9"), th.Paint("dim", "  line10"),
		th.Paint("dim", "bash") + " " + th.Paint("success", "✓") + " " + th.Paint("dim", "0.4s"),
	}
	if len(lines) != len(want) {
		t.Fatalf("block = %d lines, want %d:\n%s", len(lines), len(want), got)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestToolBlockFailureKeepsTheContent(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderToolBlock(th, "bash", bashArgs(), "command not found: flarn", true, 1*time.Second)
	if !strings.Contains(got, th.Paint("error", "✕")) {
		t.Fatalf("a fed-back failure renders the fail glyph:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("dim", "  command not found: flarn")) {
		t.Fatalf("the refusal is the interesting part and stays visible:\n%s", got)
	}
}

func TestToolBlockShortBodyUnelided(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderToolBlock(th, "bash", bashArgs(), "a\nb\nc", false, 100*time.Millisecond)
	if strings.Contains(got, "elided") {
		t.Fatalf("eight lines or fewer are not elided:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("dim", "  a")) || !strings.Contains(got, th.Paint("dim", "  c")) {
		t.Fatalf("the short body commits in full:\n%s", got)
	}
}

// decision 4's detail table: one line per tool, name-only when unknown.
func TestToolDetailTable(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args string
		want string
	}{
		{"bash", `{"command":"ls -la\nmore"}`, "$ ls -la"},
		{"read", `{"path":"/home/ng/x.go"}`, "/home/ng/x.go"},
		{"write", `{"path":"src/a.go","content":"x"}`, "src/a.go"},
		{"edit", `{"path":"src/a.go","old":"x","new":"y"}`, "src/a.go"},
		{"ls", `{"path":"src"}`, "src"},
		{"find", `{"pattern":"*.go","root":"src"}`, "*.go"},
		{"grep", `{"pattern":"panic"}`, "panic"},
		{"python", `{"code":"import os\nprint(os)"}`, "import os"},
		{"web_search", `{"query":"golang tui"}`, "golang tui"},
		{"web_fetch", `{"url":"https://example.com/x"}`, "https://example.com/x"},
		{"rem", `{"action":"recall","query":"pty"}`, ""}, // unknown to the table: name-only
	}
	for _, c := range cases {
		got := tui.RenderToolBlock(th, c.name, json.RawMessage(c.args), "body", false, time.Second)
		open := th.Paint("accent", "●") + " " + th.Paint("accent", c.name)
		if c.want != "" {
			open += th.Paint("dim", " · ") + th.Paint("text", c.want)
		}
		if !strings.HasPrefix(got, open+"\n") {
			t.Errorf("%s: opening = %q, want %q", c.name, firstLine(got), open)
		}
	}
}

func firstLine(s string) string {
	i := strings.IndexByte(s, '\n')
	if i < 0 {
		return s
	}
	return s[:i]
}
