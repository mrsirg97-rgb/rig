package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/core"
)

const sessionsListReply = "3 sessions · current 01j3c4x9ab12\n" +
	"  01j3c4x9ab12 [~] 3 turns · 0 tokens · started 2h ago · exit open\n" +
	"  01j3c2f7cd01 [x] 12 turns · 0 tokens · started 5h ago · exit ok\n" +
	"  01j3b19eaa55 [!] 1 turn · 0 tokens · started 1d ago · exit fault\n" +
	"· 8 more · sessions list all"

func TestListBlockPaintsTheSharedShape(t *testing.T) {
	th := oledTheme(t)
	got, ok := RenderListBlock(th, "OPEN", sessionsListReply)
	if !ok {
		t.Fatal("a head plus id rows is the list shape")
	}
	want := "OPEN\n" +
		th.Paint(SlotDim, "3 sessions · current 01j3c4x9ab12") + "\n" +
		th.Paint(SlotAccent, th.Glyph(GlyphActive)) + " " + th.Paint(SlotDim, "01j3c4x9ab12") + " " +
		th.Paint(SlotText, "3 turns") + th.Paint(SlotDim, " · 0 tokens") + th.Paint(SlotDim, " · started 2h ago") + th.Paint(SlotDim, " · exit open") + "\n" +
		th.Paint(SlotSuccess, th.Glyph(GlyphDone)) + " " + th.Paint(SlotDim, "01j3c2f7cd01") + " " +
		th.Paint(SlotText, "12 turns") + th.Paint(SlotDim, " · 0 tokens") + th.Paint(SlotDim, " · started 5h ago") + th.Paint(SlotDim, " · exit ok") + "\n" +
		th.Paint(SlotError, th.Glyph(GlyphFail)) + " " + th.Paint(SlotDim, "01j3b19eaa55") + " " +
		th.Paint(SlotText, "1 turn") + th.Paint(SlotDim, " · 0 tokens") + th.Paint(SlotDim, " · started 1d ago") + th.Paint(SlotDim, " · exit fault") + "\n" +
		th.Paint(SlotDim, "  · 8 more · sessions list all")
	if got != want {
		t.Fatalf("the list block must paint head, glyph rows, and the footer:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestListBlockRowsWithoutAMarkerAndPaddedIds(t *testing.T) {
	th := oledTheme(t)
	got, ok := RenderListBlock(th, "OPEN", "2 memories\n  m1 fact · 2h · 0.50 · a note\n  m12 fact · 1d · 0.25 · another")
	if !ok {
		t.Fatal("rem's rows carry no marker and are still the shape")
	}
	if !strings.Contains(got, "  "+th.Paint(SlotDim, "m1")+" "+th.Paint(SlotText, "fact")+th.Paint(SlotDim, " · 2h")) {
		t.Fatalf("a marker-less row keeps the glyph column blank and dims the id:\n%s", got)
	}
	got, _ = RenderListBlock(th, "OPEN", "2 models · active local\n  local           [~] interactive · window 65536\n  qwen3.8-workers [ ] worker · window 65536")
	if !strings.Contains(got, th.Paint(SlotDim, "local")+"           "+th.Paint(SlotText, "interactive")) {
		t.Fatalf("the id padding survives the paint so the markers line up:\n%s", got)
	}
	if !strings.Contains(got, th.Paint(SlotDim, th.Glyph(GlyphPending))+" "+th.Paint(SlotDim, "qwen3.8-workers")) {
		t.Fatalf("an idle row paints the pending glyph dim:\n%s", got)
	}
}

func TestListBlockLeavesProseAlone(t *testing.T) {
	th := oledTheme(t)
	for _, reply := range []string{
		"models: active is now local",
		"[1] user: fix the flaky test\n[2] assistant: let me look\n    thinking: the guard test",
		"vitals: 2 sessions, 2 turns\nmodels: local 0.16.1",
	} {
		if _, ok := RenderListBlock(th, "OPEN", reply); ok {
			t.Fatalf("a reply with no id rows is not a list: %q", reply)
		}
	}
}

func TestCommandListRepliesCommitTheOpeningAndTheShape(t *testing.T) {
	th := oledTheme(t)
	sess := &fakeCmd{name: "sessions", out: sessionsListReply}
	s := newScriptedSession(t, th, WithWidth(80),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{sess}, nil),
	)
	in := make(chan string, 1)
	go func() {
		l, _ := s.input()
		in <- l
	}()
	s.await(promptMark(th))
	s.si.feed("/sessions\n")
	s.await(th.Paint(SlotEmber, "/sessions"))
	s.await(th.Paint(SlotDim, "  · 8 more · sessions list all"))
	s.si.feed("bye\n")
	if l := <-in; l != "bye" {
		t.Fatalf("input after the command = %q", l)
	}
	rows := screenLines(t, s, 80)
	joined := ""
	for _, r := range rows {
		joined += paintFree(r) + "\n"
	}
	want := "/sessions\n3 sessions · current 01j3c4x9ab12\n" +
		th.Glyph(GlyphActive) + " 01j3c4x9ab12 3 turns · 0 tokens · started 2h ago · exit open\n" +
		th.Glyph(GlyphDone) + " 01j3c2f7cd01 12 turns · 0 tokens · started 5h ago · exit ok\n" +
		th.Glyph(GlyphFail) + " 01j3b19eaa55 1 turn · 0 tokens · started 1d ago · exit fault\n" +
		"  · 8 more · sessions list all\n"
	if !strings.Contains(joined, want) {
		t.Fatalf("the command path commits the opening, the head, then the glyph rows:\n%s", joined)
	}
}

func TestTheEnvLearnsTheScreenRows(t *testing.T) {
	th := oledTheme(t)
	env := &command.Env{}
	s := newScriptedSession(t, th, WithWidth(80), WithSize(sizeFixture(80, 30)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{&fakeCmd{name: "sessions", out: "x"}}, env),
	)
	if env.Lines == nil {
		t.Fatal("the TUI must hand the command Env its row budget")
	}
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10}})
	s.tick()
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	status := strings.Count(s.fe.statusLineLocked(), "\n")
	if status < 1 {
		t.Fatal("the fixture's status must occupy at least one row for the arithmetic to mean anything")
	}
	if got := env.Lines(); got != 30-status-openingRows-inputRows {
		t.Fatalf("Lines() = %d, want the height minus the status rows, the opening and the input (%d)", got, 30-status-openingRows-inputRows)
	}
}

func TestReplyBlockDimsTheAckUnderTheOpening(t *testing.T) {
	th := oledTheme(t)
	got := RenderReplyBlock(th, "OPEN", "theme", "theme: cool")
	if got != "OPEN\n"+th.Paint(SlotDim, "cool") {
		t.Fatalf("a one-line ack drops the command's own name and reads dim under the opening:\n%s", got)
	}
	got = RenderReplyBlock(th, "OPEN", "models", "models: active is now local\neffort: \"xhigh\" is not a level for local")
	want := "OPEN\n" + th.Paint(SlotDim, "active is now local") + "\n" + th.Paint(SlotText, "effort: \"xhigh\" is not a level for local")
	if got != want {
		t.Fatalf("the ack is dim, the note that rides it stays text:\ngot:\n%s\nwant:\n%s", got, want)
	}
	got = RenderReplyBlock(th, "OPEN", "sessions", "[1] user: fix the flaky test\n[2] assistant: let me look")
	want = "OPEN\n" + th.Paint(SlotText, "[1] user: fix the flaky test") + "\n" + th.Paint(SlotText, "[2] assistant: let me look")
	if got != want {
		t.Fatalf("a reply that is not an ack keeps every line in text:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestEveryCommandReplyAndRefusalCarriesTheOpening(t *testing.T) {
	th := oledTheme(t)
	role := &fakeCmd{name: "role", out: "role: architect (next turn)"}
	theme := &fakeCmd{name: "theme", err: errors.New("theme: \"neon\" is not a preset (warm, cool, custom)")}
	s := newScriptedSession(t, th, WithWidth(80),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{role, theme}, nil),
	)
	in := make(chan string, 1)
	go func() {
		l, _ := s.input()
		in <- l
	}()
	s.await(promptMark(th))
	s.si.feed("/role architect\n")
	s.await(th.Paint(SlotDim, "architect (next turn)"))
	s.si.feed("/theme neon\n")
	s.await(th.Paint(SlotError, "theme: \"neon\" is not a preset (warm, cool, custom)"))
	s.si.feed("bye\n")
	if l := <-in; l != "bye" {
		t.Fatalf("input after the commands = %q", l)
	}
	joined := ""
	for _, r := range screenLines(t, s, 80) {
		joined += paintFree(r) + "\n"
	}
	for _, want := range []string{"/role · architect\narchitect (next turn)\n", "/theme · neon\ntheme: \"neon\" is not a preset (warm, cool, custom)\n"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the screen must carry %q:\n%s", want, joined)
		}
	}
}
