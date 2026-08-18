package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

// fakeCmd is a command for the dispatch tests: a fixed reply or error,
// with a call counter.
type fakeCmd struct {
	name  string
	desc  string
	out   string
	err   error
	calls int
}

func (f *fakeCmd) Name() string { return f.name }
func (f *fakeCmd) Description() string {
	if f.desc != "" {
		return f.desc
	}
	return "test"
}
func (f *fakeCmd) Schema() json.RawMessage { return []byte(`{}`) }
func (f *fakeCmd) Run(ctx context.Context, args string, env any) (string, error) {
	f.calls++
	return f.out, f.err
}

func oledTheme(t *testing.T) Theme {
	t.Helper()
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func bannerFixture() BannerIn {
	return BannerIn{
		Model: "huihui3.8", Effort: "xhigh", Used: 84300, Window: 262144,
		Up: 214000, Down: 18200, CacheRead: 187000,
	}
}

// promptMark is the input line's painted prompt — the marker a test
// awaits before it feeds.
func promptMark(th Theme) string {
	return th.Paint(SlotAccent, th.Glyph(GlyphPrompt))
}

// awaitCtxDone waits for the interrupt to land (the reader is async;
// the byte must have crossed the state machine before the assertion).
func (s *scriptedSession) awaitCtxDone(ctx context.Context) {
	s.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for ctx.Err() == nil {
		if time.Now().After(deadline) {
			s.t.Fatal("timed out awaiting the interrupt")
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitReasoning waits for the reasoning toggle to land (same race).
func (s *scriptedSession) awaitReasoning(on bool) {
	s.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.fe.mu.Lock()
		got := s.fe.showReasoning
		s.fe.mu.Unlock()
		if got == on {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out awaiting reasoning on=%v", on)
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitCount polls the stream until the marker occurs n times.
func (s *scriptedSession) awaitCount(marker string, n int) {
	s.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for bytes.Count(s.out.Bytes(), []byte(marker)) < n {
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out awaiting %d× %q in the stream", n, marker)
		}
		time.Sleep(time.Millisecond)
	}
}

// prompt awaits the input line, feeds the text, and returns what Input
// delivered (the shared session ctx drives the turn).
func (s *scriptedSession) prompt(marker, feed string) string {
	s.t.Helper()
	in := make(chan string, 1)
	go func() {
		l, err := s.input()
		if err != nil && !errors.Is(err, io.EOF) {
			s.t.Errorf("prompt: %v", err)
		}
		in <- l
	}()
	s.await(marker)
	s.si.feed(feed)
	select {
	case l := <-in:
		return l
	case <-time.After(3 * time.Second):
		s.t.Fatal("timed out on the prompt")
		return ""
	}
}

// TestBannerReprintTriggers is the named banner case: the triggers fire
// it exactly once — start, new, sessions resume, models switch, and
// the Compacted event — and no other event reprints it.
func TestBannerReprintTriggers(t *testing.T) {
	th := oledTheme(t)
	row1Line := strings.Split(RenderBanner(th, bannerFixture(), 50), "\n")[1]

	newC := &fakeCmd{name: "new", out: "new session: s2"}
	sessC := &fakeCmd{name: "sessions", out: "resumed s3"}
	modelsC := &fakeCmd{name: "models", out: "switched"}
	compactC := &fakeCmd{name: "compact", out: "nothing to drop"}
	s := newScriptedSession(t,
		WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithCommands([]core.Command{newC, sessC, modelsC, compactC}, nil),
		WithTicks(make(chan time.Time)),
	)
	count := func() int { return bytes.Count(s.out.Bytes(), []byte(row1Line)) }

	// the session start: exactly once.
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt one = %q, want go", got)
	}
	if got := count(); got != 1 {
		t.Fatalf("the session start reprinted the banner %d times, want 1", got)
	}

	// a plain event stream: no reprint.
	s.fe.Notify(core.TextDelta{Text: "hi\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10}})
	if got := count(); got != 1 {
		t.Fatalf("a plain turn reprinted the banner: %d, want 1", got)
	}
	// the Compacted event: one more.
	s.fe.Notify(core.Compacted{Dropped: 100, Kept: 10})
	s.awaitCount(row1Line, 2)
	if got := count(); got != 2 {
		t.Fatalf("the Compacted event reprinted %d times, want 1 more (2 total)", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	if got := count(); got != 2 {
		t.Fatalf("the turn end reprinted the banner: %d, want 2", got)
	}

	// the command triggers, each exactly once. The dispatch runs inside
	// the Input that keeps pulling after each command.
	in := make(chan string, 1)
	go func() {
		l, err := s.input()
		if err != nil {
			s.t.Errorf("prompt: %v", err)
		}
		in <- l
	}()
	s.await(promptMark(th))
	s.si.feed("/new\n")
	s.awaitCount(row1Line, 3)
	s.si.feed("/sessions resume s3\n")
	s.awaitCount(row1Line, 4)
	s.si.feed("/models m2\n")
	s.awaitCount(row1Line, 5)
	// the compact command is not a trigger (its event would be): its
	// output commits, the banner must not.
	s.si.feed("/compact\n")
	s.await("nothing to drop")
	if got := count(); got != 5 {
		t.Fatalf("the compact command reprinted the banner: %d, want 5", got)
	}
	// a models list (no args) is not a switch.
	s.si.feed("/models\n")
	deadline := time.Now().Add(3 * time.Second)
	for strings.Count(s.out.String(), "switched") < 2 {
		if time.Now().After(deadline) {
			t.Fatal("timed out on the models list output")
			return
		}
		time.Sleep(time.Millisecond)
	}
	if got := count(); got != 5 {
		t.Fatalf("the models list reprinted the banner: %d, want 5", got)
	}
	// the turn after the commands.
	s.si.feed("go2\n")
	select {
	case l := <-in:
		if l != "go2" {
			t.Fatalf("prompt after the commands = %q, want go2", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out on go2")
	}
	if got := count(); got != 5 {
		t.Fatalf("the banner count at the end = %d, want 5", got)
	}
}

// TestBothDoorsThroughFrontend is decision 6 through the real doors:
// the tool-result path (the model called the tool) and the command
// path (the operator typed the line) commit byte-equal blocks, minus
// the opening line.
func TestBothDoorsThroughFrontend(t *testing.T) {
	th := oledTheme(t)
	const reply = "→ t3 started\n" +
		"2/5 done · next: t4\n" +
		"  t1 [x] wire the models table\n" +
		"  t2 [x] the switch seam\n" +
		"  t3 [~] steer verb\n" +
		"  t4 [ ] policy test · waits on t3\n" +
		"  t5 [ ] rem check\n"

	screen := func(s *scriptedSession) []string {
		s.t.Helper()
		v := newVT(50)
		v.feed(s.out.Bytes())
		if v.err != "" {
			s.t.Fatalf("harness: %s", v.err)
		}
		return v.rows
	}
	// block extracts the rendered block from the screen, by opening.
	block := func(s *scriptedSession, rows []string, opening string) []string {
		s.t.Helper()
		n := len(strings.Split(RemoveColor(RenderTodoBlock(th, opening, reply)), "\n"))
		for i, r := range rows {
			if r == opening {
				if i+n > len(rows) {
					s.t.Fatalf("the block overruns the screen")
				}
				return rows[i : i+n]
			}
		}
		s.t.Fatalf("the opening line %q is not on the screen:\n%q", opening, rows)
		return nil
	}

	// the tool-result door.
	tool := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	if got := tool.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q, want go", got)
	}
	tool.fe.Notify(core.ToolStart{Call: core.ToolCall{
		Name: "todo",
		Args: []byte(`{"action":"start","id":"t3"}`),
	}})
	tool.fe.Notify(core.ToolResult{Content: reply, Duration: 5 * time.Millisecond})
	tool.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	toolBlock := block(tool, screen(tool), "● todo · start t3")

	// the command door.
	todo := &fakeCmd{name: "todo", out: reply}
	cmdS := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithCommands([]core.Command{todo}, nil),
		WithTicks(make(chan time.Time)))
	in := make(chan string, 1)
	go func() {
		l, err := cmdS.input()
		if err != nil {
			cmdS.t.Errorf("prompt: %v", err)
		}
		in <- l
	}()
	cmdS.await(promptMark(th))
	cmdS.si.feed("/todo start t3\n")
	// the dispatch commits the block before the next line is read.
	cmdS.await(th.Paint(SlotAccent, "/todo"))
	cmdS.si.feed("bye\n")
	select {
	case l := <-in:
		if l != "bye" {
			t.Fatalf("prompt after the command = %q, want bye", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out")
	}
	cmdBlock := block(cmdS, screen(cmdS), "/todo · start t3")

	// the openings are the doors; the bodies are the bytes.
	if len(toolBlock) != len(cmdBlock) {
		t.Fatalf("the door blocks differ in length: %d vs %d", len(toolBlock), len(cmdBlock))
	}
	if toolBlock[0] == cmdBlock[0] {
		t.Fatalf("the opening lines are identical — the door is the difference: %q", toolBlock[0])
	}
	for i := 1; i < len(toolBlock); i++ {
		if toolBlock[i] != cmdBlock[i] {
			t.Fatalf("line %d differs between the doors:\n[tool] %q\n[cmd]  %q", i, toolBlock[i], cmdBlock[i])
		}
	}
}

// TestPasteBurstThreePrompts is the named input case: a pasted line is
// a separate ordered prompt — three lines, three prompts, in order —
// and a line that lands after the turn's first event steers instead
// (the slot, the interrupt, the slot's line on the next Input).
func TestPasteBurstThreePrompts(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	mark := promptMark(th)

	// the burst at the quiet prompt: all three lines are prompts in
	// order (no event has crossed the turn when b and c land).
	if got := s.prompt(mark, "a\n"); got != "a" {
		t.Fatalf("prompt one = %q, want a", got)
	}
	s.si.feed("b\nc\n")
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	line, err := s.input()
	if line != "b" || err != nil {
		t.Fatalf("prompt two = (%q, %v), want b (the burst is in order)", line, err)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	line, err = s.input()
	if line != "c" || err != nil {
		t.Fatalf("prompt three = (%q, %v), want c (the burst is in order)", line, err)
	}

	// the steering side of the rule: a line that lands after the
	// turn's first event goes to the slot and interrupts.
	ctx, cancel := context.WithCancel(context.Background())
	ctx = core.WithInterrupt(ctx, cancel)
	saved := s.ctx
	s.ctx = ctx
	defer func() { s.ctx = saved }()
	if got := s.prompt(mark, "go\n"); got != "go" {
		t.Fatalf("prompt four = %q, want go", got)
	}
	s.fe.Notify(core.TextDelta{Text: "working\n"}) // the turn is established
	s.si.feed("steer me\n")                        // the line steers
	s.awaitCtxDone(ctx)
	s.fe.Notify(core.TurnEnd{Reason: core.TurnInterrupt})
	// the loop's next turn is a fresh ctx (the interrupted one is spent).
	s.ctx = saved
	line, err = s.input()
	if line != "steer me" || err != nil {
		t.Fatalf("the steering line = (%q, %v), want the queued slot line", line, err)
	}
}

// TestCtrlTogglesReasoning is the named input case: Ctrl-T toggles the
// rendering of subsequent reasoning only — committed history is
// immutable, the transcript untouched.
func TestCtrlTogglesReasoning(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q, want go", got)
	}

	s.fe.Notify(core.ReasoningDelta{Text: "first thought"})
	s.fe.Notify(core.ReasoningDelta{Text: "second thought"})
	s.si.feed(string(byte(0x14))) // Ctrl-T: off
	s.awaitReasoning(false)
	s.fe.Notify(core.ReasoningDelta{Text: "third thought"})
	s.si.feed(string(byte(0x14))) // Ctrl-T: on
	s.awaitReasoning(true)
	s.fe.Notify(core.ReasoningDelta{Text: "fourth thought"})

	out := s.out.String()
	for _, want := range []string{"first thought", "second thought", "fourth thought"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the stream is missing %q (reasoning should render)", want)
		}
	}
	if strings.Contains(out, "third thought") {
		t.Fatal("the toggled-off reasoning rendered")
	}
}

// TestSchedulerNewsLine is the named news case: news since the last
// session renders one dim line after the banner, exactly once; no news
// renders nothing. The read is the root's (the closure); the TUI
// renders it and nothing else.
func TestSchedulerNewsLine(t *testing.T) {
	th := oledTheme(t)
	news := "· j5 failed 14:30 · scheduler runs j5"

	withNews := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithNews(func(ctx context.Context) string { return news }),
		WithTicks(make(chan time.Time)))
	withNews.prompt(promptMark(th), "go\n")
	out := withNews.out.String()
	if got := strings.Count(out, th.Paint(SlotDim, news)); got != 1 {
		t.Fatalf("the news line occurs %d times, want exactly one, dim", got)
	}
	// after the banner: the news line follows the banner (its model
	// line precedes it in the stream).
	if iM := strings.Index(out, "huihui3.8"); iM < 0 {
		t.Fatalf("the banner is not in the stream")
	} else if iN := strings.Index(out, news); iN < iM {
		t.Fatalf("the news line is before the banner")
	}

	noNews := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithNews(func(ctx context.Context) string { return "" }),
		WithTicks(make(chan time.Time)))
	noNews.prompt(promptMark(th), "go\n")
	if strings.Contains(noNews.out.String(), "j5 failed") {
		t.Fatal("absent news rendered a line")
	}
}

// TestCtrlCEndSession is decision 9's exit: Ctrl-C ends the session —
// the live turn is interrupted, and the next Input EOFs.
func TestCtrlCEndSession(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	ctx, interrupt := context.WithCancel(context.Background())
	ctx = core.WithInterrupt(ctx, interrupt)
	saved := s.ctx
	s.ctx = ctx
	defer func() { s.ctx = saved }()
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q, want go", got)
	}
	// the live turn: Ctrl-C interrupts it and ends the session.
	s.si.feed(string(byte(0x03)))
	s.awaitCtxDone(ctx)
	s.fe.Notify(core.TurnEnd{Reason: core.TurnInterrupt})
	// the loop's next Input is a fresh ctx (the interrupted one is spent).
	s.ctx = saved
	line, err := s.input()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("after Ctrl-C the input = (%q, %v), want io.EOF", line, err)
	}
}

// TestCtrlDEmptyExits: Ctrl-D at the empty prompt between turns exits.
func TestCtrlDEmptyExits(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q, want go", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	// between turns: the empty-prompt exit.
	s.si.feed(string(byte(0x04)))
	line, err := s.input()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Ctrl-D at the empty prompt: (%q, %v), want io.EOF", line, err)
	}
	_ = line
}

// TestCtrlDNonBlankKept: Ctrl-D on a non-blank line keeps it; Enter
// submits it.
func TestCtrlDNonBlankKept(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "keep me\x04\n"); got != "keep me" {
		t.Fatalf("Ctrl-D on a non-blank line = %q, want keep me (kept)", got)
	}
}

// TestDispatchVoice is the command path's voice (decision 5): no
// separate dim echo (the committed prompt line is the echo), the
// output the CLI's bytes, the unknown command loud and naming the
// known set, and the // escape the prompt's.
func TestDispatchVoice(t *testing.T) {
	th := oledTheme(t)
	newC := &fakeCmd{name: "new", out: "new session: s2"}
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithCommands([]core.Command{newC}, nil),
		WithTicks(make(chan time.Time)))
	in := make(chan string, 1)
	go func() {
		l, err := s.input()
		if err != nil {
			s.t.Errorf("prompt: %v", err)
		}
		in <- l
	}()
	s.await(promptMark(th))
	// the unknown command: the loud refusal; the committed prompt line
	// is the only echo (no separate dim copy of the line).
	s.si.feed("/nope\n")
	s.await("unknown command: nope")
	out := s.out.String()
	if strings.Contains(out, th.Paint(SlotDim, "/nope")) {
		t.Fatalf("the command line must not echo a second dim copy")
	}
	if !strings.Contains(out, th.Paint(SlotDim, "unknown command: nope (known: new)")) {
		t.Fatalf("the unknown command is not loud with the known set:\n%s", out)
	}
	// the known command: the output (its banner trigger follows the
	// output in the dispatch).
	s.si.feed("/new\n")
	s.await(th.Paint(SlotText, "new session: s2"))
	if !strings.Contains(s.out.String(), th.Paint(SlotText, "new session: s2")) {
		t.Fatalf("the command output is not committed as the CLI's bytes")
	}
	// the // escape: the prompt, one slash consumed.
	s.si.feed("///models\n")
	select {
	case l := <-in:
		if l != "//models" {
			t.Fatalf("the escaped line = %q, want //models", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out on the escaped line")
	}
}

// TestSteerSeam is the Steerer contract (SPEC_COMMANDS 2) on the TUI:
// Steer queues (latest wins) and reports the interrupt; the slot
// delivers on the next Input; a cleared slot delivers nothing.
func TestSteerSeam(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	fe, ok := interface{}(s.fe).(interface {
		Steer(string) bool
		Interrupt() bool
		ClearSlot()
		LiveTurn() bool
	})
	if !ok {
		t.Fatal("the TUI is not a Steerer")
	}

	// between turns: the slot queues (no interrupt to report).
	if got := fe.Steer("queued"); got {
		t.Fatal("Steer between turns reported an interrupt, want false")
	}
	in := make(chan string, 1)
	go func() {
		l, _ := s.input()
		in <- l
	}()
	if line := <-in; line != "queued" {
		t.Fatalf("the slot line = %q, want queued", line)
	}

	// during a live turn: the interrupt lands, the slot replaces.
	ctx, cancel := context.WithCancel(context.Background())
	ctx = core.WithInterrupt(ctx, cancel)
	saved := s.ctx
	s.ctx = ctx
	defer func() { s.ctx = saved }()
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q, want go", got)
	}
	if got := fe.Steer("first"); !got {
		t.Fatal("Steer on a live turn did not report the interrupt")
	}
	if ctx.Err() == nil {
		t.Fatal("Steer did not interrupt the live turn")
	}
	fe.Steer("second") // latest wins
	s.fe.Notify(core.TurnEnd{Reason: core.TurnInterrupt})
	// the loop's next turn is a fresh ctx (the interrupted one is spent).
	s.ctx = saved
	line, err := s.input()
	if line != "second" || err != nil {
		t.Fatalf("after the steering: (%q, %v), want second (latest wins)", line, err)
	}
	// the delivered line started the loop's next turn: live.
	if !fe.LiveTurn() {
		t.Fatal("LiveTurn after the delivered line, want true (the next turn has started)")
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	if fe.LiveTurn() {
		t.Fatal("LiveTurn after the turn end, want false")
	}
	// Interrupt alone: the fact, no slot line.
	ctx2, cancel2 := context.WithCancel(context.Background())
	ctx2 = core.WithInterrupt(ctx2, cancel2)
	s.ctx = ctx2
	if got := s.prompt(promptMark(th), "go2\n"); got != "go2" {
		t.Fatalf("prompt = %q, want go2", got)
	}
	if got := fe.Interrupt(); !got {
		t.Fatal("Interrupt on a live turn did not report the fact")
	}
	if ctx2.Err() == nil {
		t.Fatal("Interrupt did not cancel the turn")
	}
	// a cleared slot must not deliver: the next line is the operator's
	// (a fresh ctx — the interrupted one is spent).
	fe.Steer("x")
	fe.ClearSlot()
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.ctx = saved
	inZ := make(chan string, 1)
	go func() {
		l, _ := s.input()
		inZ <- l
	}()
	s.si.feed("z\n")
	select {
	case l := <-inZ:
		if l != "z" {
			t.Fatalf("after the cleared slot the line = %q, want z (not the slot's x)", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out after the cleared slot")
	}
}

// TestTextFlowsAsTheCLIDoes is the streaming-text rule made a test
// (decision 2: "committed as flowed: the terminal wraps, rig never
// hand-wraps committed prose"; the spec's testing section: the TUI
// adds, never changes, the CLI's bytes): a delta that does not end on
// a line does not close it — the next delta, whatever its slot,
// continues on the same line, and only a newline commits.
func TestTextFlowsAsTheCLIDoes(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(100),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	in := make(chan string, 1)
	go func() {
		line, _ := s.input()
		in <- line
	}()
	s.await(promptMark(th))
	s.si.feed("go\n")
	if line := <-in; line != "go" {
		t.Fatalf("the prompt = %q, want go", line)
	}
	// the deltas, the way a decoder delivers them: mid-word, then the
	// line's close.
	s.fe.Notify(core.TextDelta{Text: "hel"})
	s.fe.Notify(core.TextDelta{Text: "lo\n"})
	// a mixed line: reasoning butts against text the way the CLI's raw
	// bytes do (the CLI has no line of its own; the bytes are the line).
	s.fe.Notify(core.ReasoningDelta{Text: "r"})
	s.fe.Notify(core.TextDelta{Text: "t\n"})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	v := newVT(100)
	v.feed([]byte(s.out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%s", v.err, s.out.String())
	}
	idx := func(want string) int {
		for i, l := range v.rows {
			if l == want {
				return i
			}
		}
		return -1
	}
	goIdx, helloIdx, rtIdx, usageIdx := idx("❯ go"), idx("hello"), idx("rt"), idx("up 0 down 0 · cache r 0 0%")
	if helloIdx < 0 {
		t.Fatalf("the deltas did not flow into one line (the CLI's bytes):\n%q", v.rows)
	}
	if rtIdx < 0 {
		t.Fatalf("the mixed reasoning/text line did not flow as the CLI's bytes:\n%q", v.rows)
	}
	if !(goIdx >= 0 && goIdx < helloIdx && helloIdx < rtIdx && rtIdx < usageIdx) {
		t.Fatalf("the committed lines are out of order (go, hello, rt, usage):\n%q", v.rows)
	}
}

// TestWidePendingLineWrapsClean is the wrap boundary the cursor
// arithmetic must honor: the terminal wraps a wide pending line across
// as many terminal rows as it needs (decision 2: the terminal wraps, rig
// never hand-wraps), and the live region's redraws must count those
// terminal rows — not the logical line — when they clear and reprint.
// The cursor-up that lands short by the wrapped rows leaves the
// activity row's frame repeating in the scrollback (the live-region
// ghost); the replayed rows catch it exactly.
func TestWidePendingLineWrapsClean(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(20),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	in := make(chan string, 1)
	go func() {
		line, _ := s.input()
		in <- line
	}()
	s.await(promptMark(th))
	s.si.feed("go\n")
	if line := <-in; line != "go" {
		t.Fatalf("the prompt = %q, want go", line)
	}
	// 24 runes at a 20-column terminal: the pending line wraps to two
	// terminal rows while it is live, and the close must commit the whole
	// wrapped line without the activity row's frame repeating.
	s.fe.Notify(core.TextDelta{Text: "aaaaaaaaaaaaaaaaaaaaaaaa"})
	s.fe.Notify(core.TextDelta{Text: "\n"})
	s.fe.Notify(core.TextDelta{Text: "bb"})
	s.fe.Notify(core.TextDelta{Text: "\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	v := newVT(20)
	v.feed([]byte(s.out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%q", v.err, s.out.String())
	}
	rule := strings.Repeat(th.Glyph(GlyphDot), 20)
	want := []string{
		rule,
		"huihui3.8 · xhigh · ", // the wide banner lines wrap at 20 too
		"84k/262k 32%",
		"up 214k down 18k · c",
		"ache r 187k 87%",
		rule,
		"❯ go",
		"aaaaaaaaaaaaaaaaaaaa",
		"aaaa",
		"bb",
		"", // the CLI's Done newline: unconditional, so a blank line
		"up 10 down 2 · cache",
		" r 0 0%",
		"❯ ",
	}
	if len(v.rows) != len(want) {
		t.Fatalf("%d rows, want %d:\n%q", len(v.rows), len(want), v.rows)
	}
	for i := range want {
		if v.rows[i] != want[i] {
			t.Fatalf("row %d = %q, want %q", i, v.rows[i], want[i])
		}
	}
}

// TestDoneNewlineIsTheCLIs is the boundary parity made a test: the
// CLI's Done newline is unconditional, and so is its ToolStart
// separator — the TUI adds, it does not remove (a text turn that ends
// on a line still gets the blank line before the usage line; a
// tool-first turn gets the separator before the block).
func TestDoneNewlineIsTheCLIs(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(100),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	in := make(chan string, 1)
	go func() {
		line, _ := s.input()
		in <- line
	}()
	s.await(promptMark(th))
	s.si.feed("go\n")
	<-in
	s.fe.Notify(core.TextDelta{Text: "done\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	v := newVT(100)
	v.feed([]byte(s.out.String()))
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%s", v.err, s.out.String())
	}
	usage := "up 10 down 2 · cache r 0 0%"
	idx := -1
	for i, l := range v.rows {
		if l == usage {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("the usage line is missing:\n%q", v.rows)
	}
	if idx == 0 || v.rows[idx-1] != "" {
		t.Fatalf("the CLI's Done newline did not land as a blank line before the usage line:\n%q", v.rows)
	}

	// the tool-first turn: the separator line stands before the block.
	s2 := newScriptedSession(t, WithTheme(th), WithWidth(100),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	in2 := make(chan string, 1)
	go func() {
		line, _ := s2.input()
		in2 <- line
	}()
	s2.await(promptMark(th))
	s2.si.feed("go\n")
	<-in2
	s2.fe.Notify(core.ToolStart{Call: core.ToolCall{Name: "bash", Args: []byte(`{"command":"echo hi"}`)}})
	s2.fe.Notify(core.ToolResult{Content: "hi\n", Duration: 40 * time.Millisecond})
	s2.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
	s2.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	v2 := newVT(100)
	v2.feed([]byte(s2.out.String()))
	if v2.err != "" {
		t.Fatalf("harness: %s\nstream:\n%s", v2.err, s2.out.String())
	}
	block := "● bash · $ echo hi"
	idx2 := -1
	for i, l := range v2.rows {
		if l == block {
			idx2 = i
		}
	}
	if idx2 < 0 {
		t.Fatalf("the tool block's opening line is missing:\n%q", v2.rows)
	}
	if v2.rows[idx2-1] != "" {
		t.Fatalf("the CLI's ToolStart separator did not land before the block:\n%q", v2.rows)
	}
}

// The inline hint (decision 9): fish-style ghost text on the input row.
// Typing a command prefix shows the first match's remainder plus the
// candidates; a known name plus a space shows its description; plain
// prompts and the // escape show nothing.
func TestInlineHintForCommands(t *testing.T) {
	th := oledTheme(t)
	models := &fakeCmd{name: "models", desc: "the per-model table"}
	moveC := &fakeCmd{name: "move", desc: "move a thing"}
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithCommands([]core.Command{models, moveC}, nil),
		WithTicks(make(chan time.Time)))
	go func() { _, _ = s.input() }()
	s.await(promptMark(th))
	s.si.feed("/mo")
	s.await(th.Paint(SlotDim, "dels  · models move"))
	s.si.feed("d")
	s.await(th.Paint(SlotDim, "els"))
	// a known name + space: the description
	s.si.feed("els ")
	s.await(th.Paint(SlotDim, "the per-model table"))
}

// Tab completion (decision 9): the longest common prefix, the trailing
// space when unique; a no-op on plain prompts.
func TestTabCompletesCommandNames(t *testing.T) {
	th := oledTheme(t)
	models := &fakeCmd{name: "models", desc: ""}
	moveC := &fakeCmd{name: "move", desc: ""}
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithCommands([]core.Command{models, moveC}, nil),
		WithTicks(make(chan time.Time)))
	go func() { _, _ = s.input() }()
	s.await(promptMark(th))
	s.si.feed("/m\t") // lcp of models/move: "mo"
	s.await(th.Paint(SlotText, " /mo"))
	s.si.feed("d\t") // unique: models + trailing space
	s.await(th.Paint(SlotText, " /models "))
}

// TestInputWrapsAndScrolls (decision 9's five-row input): the input
// wraps across terminal rows as it grows; past maxInputRows a window
// scrolls with the cursor (end shows the tail, home the head with the
// prompt glyph); a wrapped-row edit parks the cursor mid-region and
// the next repaint still lands clean (live's norm), and the Enter
// commits the full text.
func TestInputWrapsAndScrolls(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(10),
		WithBanner(func(ctx context.Context) BannerIn { return bannerFixture() }),
		WithTicks(make(chan time.Time)))
	in := make(chan string, 1)
	go func() { l, _ := s.input(); in <- l }()
	s.await(promptMark(th))
	screenLast := func(n int) []string {
		t.Helper()
		v := newVT(10)
		v.feed(s.out.Bytes())
		if v.err != "" {
			t.Fatalf("harness: %s", v.err)
		}
		if len(v.rows) < n {
			t.Fatalf("%d rows on the screen, want at least %d:\n%q", len(v.rows), n, v.rows)
		}
		return v.rows[len(v.rows)-n:]
	}
	// the text is digits with a unique tail: the digit run alone is
	// 10-periodic, and an await marker that repeats across windows
	// races the snapshot against the keystrokes still in flight.
	digits := strings.Repeat("0123456789", 5) + "01234567XY"

	// 17 runes at width 10: the input wraps to two terminal rows.
	s.si.feed(digits[:17])
	s.await(th.Paint(SlotText, " "+digits[:17]))
	rows := screenLast(2)
	if rows[0] != "❯ 01234567" || rows[1] != "890123456" {
		t.Fatalf("wrapped input rows = %q", rows)
	}

	// 60 runes: seven logical rows; the five-row window follows the
	// cursor to the tail, the prompt glyph scrolled off.
	s.si.feed(digits[17:])
	s.await(th.Paint(SlotText, digits[18:]))
	rows = screenLast(5)
	if rows[0] != "8901234567" || rows[4] != "XY" {
		t.Fatalf("tail window rows = %q", rows)
	}

	// home: the window scrolls back to the head, the glyph visible.
	s.si.feed("\x01")
	s.await(th.Paint(SlotText, " "+digits[:48]))
	rows = screenLast(5)
	if rows[0] != "❯ 01234567" {
		t.Fatalf("head window row = %q, want the prompt row", rows[0])
	}

	// the park: the cursor sits rows above the region's bottom; typing
	// there repaints clean, and the Enter commits the full text.
	s.si.feed("Z")
	s.await(th.Paint(SlotText, " Z"+digits[:47]))
	s.si.feed("\n")
	if line := <-in; line != "Z"+digits {
		t.Fatalf("the committed line = %q, want the full text", line)
	}
}
