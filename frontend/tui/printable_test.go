package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v2/core"
)

const editorNoise = "\x1b[?2004h\x1b(B\x1b)0\x1b[?1049h\x1b[1;24r\x1b[m\x0f\x1b[4l\x1b[39;49m\x1b[?1h\x1b=\x1b[?25l\x1b[H\x1b[J\x1b[22;34H\x1b[0;7m\x0f[ Reading... ]\x1b[m\x0f\x1b[34h\x1b[?25h\x1b[24;1H\x1b[?1049l\r\x1b[?1l\x1b>\x1b[?2004lStandard input is not a terminal\nerror: There was a problem with the editor 'editor'.\n"

func TestPrintableKeepsTextAndDropsEveryTerminalControl(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"plain text is returned as it is", "go build ./...\n\tok — 12 tests ✓", "go build ./...\n\tok — 12 tests ✓"},
		{"a colour sequence leaves its text", "\x1b[31mFAIL\x1b[0m pkg", "FAIL pkg"},
		{"the alternate screen and a scroll region leave nothing", "\x1b[?1049h\x1b[1;24r\x1b[H\x1b[Jtext", "text"},
		{"a charset shift and a keypad mode leave nothing", "\x1b(B\x1b)0\x0f\x1b=\x1b>text\x0e", "text"},
		{"a window title leaves nothing", "\x1b]0;title\x07after", "after"},
		{"a title closed by the string terminator leaves nothing", "\x1b]8;;http://x\x1b\\link", "link"},
		{"an unclosed title stops at the line's end", "\x1b]0;title\nnext", "\nnext"},
		{"a carriage return, a bell and a backspace leave nothing", "50%\r100%\a\b done", "50%100% done"},
		{"an escape at the very end leaves nothing", "text\x1b", "text"},
		{"a cut sequence leaves nothing", "text\x1b[12;", "text"},
		{"a one-byte control from the upper range leaves nothing", "a\u009bb\u0085c", "abc"},
	} {
		if got := printable(c.in); got != c.want {
			t.Fatalf("%s: %q -> %q, want %q", c.name, c.in, got, c.want)
		}
	}
	got := printable(editorNoise)
	if strings.ContainsAny(got, "\x1b\r\x0f") {
		t.Fatalf("an editor's start-up bytes carry no control out: %q", got)
	}
	if !strings.Contains(got, "[ Reading... ]Standard input is not a terminal\nerror: There was a problem") {
		t.Fatalf("the words the editor printed stay: %q", got)
	}
}

func TestAToolBlockCarriesNoTerminalControlFromItsResultOrItsCall(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	args, _ := json.Marshal(map[string]string{"command": "printf '\x1b[2J\x1b[1;24r' && git rebase --continue"})
	block := RenderToolBlock(th, 52, "bash", args, editorNoise, false, time.Second)
	plain := stripANSI(block)
	if strings.ContainsAny(plain, "\x1b\r\x0f") {
		t.Fatalf("the block writes only its own colours to the terminal: %q", plain)
	}
	for _, seq := range []string{"\x1b[?1049h", "\x1b[1;24r", "\x1b[H", "\x1b[J", "\x1b[2J", "\x1b[22;34H"} {
		if strings.Contains(block, seq) {
			t.Fatalf("the block lets %q through", seq)
		}
	}
	if !strings.Contains(plain, "Standard input is not a terminal") || !strings.Contains(plain, "git rebase --continue") {
		t.Fatalf("the words stay:\n%s", plain)
	}
	for _, row := range strings.Split(plain, "\n") {
		if WidthOf(row) > 52 {
			t.Fatalf("a row is wider than the terminal once the controls are gone: %q", row)
		}
	}
}

func TestAToolResultWithAnEditorsBytesLeavesTheTerminalsModesAlone(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	s := newScriptedSession(t, th, WithWidth(52))
	if line := s.inputWhile("continue the rebase\n"); line != "continue the rebase" {
		t.Fatalf("the operator's text opens the turn: %q", line)
	}
	s.tail = len(s.out.Bytes())
	call := core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"git rebase --continue"}`)}
	s.fe.Notify(core.ReasoningDelta{Text: "the editor may open\x1b[2J\n"})
	s.fe.Notify(core.ToolStart{Call: call})
	s.fe.Notify(core.ToolResult{ID: "c1", Content: editorNoise, Duration: time.Second})
	s.fe.Notify(evt.Notice{Source: "swarm", Text: "a worker said \x1b[1;24r this"})
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{{ID: 1, Role: "delegate", Task: "task\x1b[?1049h", State: "running", Heartbeat: time.Now(), Tool: "bash \x1b[H\x1b[J", ToolAt: time.Now()}}})
	s.await("Standard input is not a terminal")
	time.Sleep(200 * time.Millisecond)
	out := s.since()
	for _, seq := range []string{"\x1b[?1049h", "\x1b[?1049l", "\x1b[1;24r", "\x1b[22;34H", "\x1b[24;1H", "\x1b[2J", "\x1b(B", "\x1b)0", "\x0f", "\x1b[?1h", "\x1b[4l"} {
		if strings.Contains(out, seq) {
			t.Fatalf("the terminal was sent %q", seq)
		}
	}
}
