package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/command"
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

func statusFixture() StatusIn {
	return StatusIn{
		Model: "huihui3.8", Effort: "xhigh", Window: 262144,
		Session: "2f9a1c0e77b34455",
		Up:      214000, Down: 18200, CacheRead: 187000,
	}
}

// promptMark is the input line's painted prompt — the marker a test
// awaits before it feeds.
func promptMark(th Theme) string {
	return th.Paint(SlotEmber, th.Glyph(GlyphPrompt))
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

// awaitPending waits for n lines in the delivery queue (the reader is
// async): the burst must have crossed the state machine, in order,
// before the test sends the boundary event it races with.
func (s *scriptedSession) awaitPending(n int) {
	s.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.fe.mu.Lock()
		got := len(s.fe.pending)
		s.fe.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out awaiting %d queued line(s), have %d", n, got)
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
// TestStatusLineRefresh is decision 3's contract through the frontend:
// the startup block commits exactly once (the session start), the
// events move the status line's numbers (the last Done's usage, the
// compact's Kept), and the refresh points (new, sessions resume, a
// models switch) re-snapshot the row — never the block.
func TestStatusLineRefresh(t *testing.T) {
	th := oledTheme(t)
	// the block's greeting row: the block's marker (it commits at the
	// session start and the fresh refresh points, never per turn).
	blockRow := strings.Split(RenderStatus(th, statusFixture()), "\n")[0]
	// the status line's door: the first call (the session start) takes
	// the s1 numbers; every later call (a refresh point) returns the
	// s2 context — a different model, so a refresh is visible in the
	// stream and the call count is the door's.
	model, window := "huihui3.8", 262144
	var calls atomic.Int32
	statusIn := func(ctx context.Context) StatusIn {
		if calls.Add(1) == 1 {
			return StatusIn{
				Model: model, Effort: "xhigh", Window: window,
				Up: 214000, Down: 18200, CacheRead: 187000,
			}
		}
		return StatusIn{Model: "model2", Effort: "low", Window: 131072}
	}

	newC := &fakeCmd{name: "new", out: "new session: s2"}
	sessC := &fakeCmd{name: "sessions", out: "resumed s3"}
	modelsC := &fakeCmd{name: "models", out: "switched"}
	compactC := &fakeCmd{name: "compact", out: "nothing to drop"}
	s := newScriptedSession(t,
		WithTheme(th), WithWidth(50),
		WithStatus(statusIn),
		WithCommands([]core.Command{newC, sessC, modelsC, compactC}, nil),
		WithTicks(make(chan time.Time)),
	)
	blockCount := func() int { return bytes.Count(s.out.Bytes(), []byte(blockRow)) }

	// the session start: the block exactly once.
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt one = %q, want go", got)
	}
	if got := blockCount(); got != 1 {
		t.Fatalf("the session start committed the block %d times, want 1", got)
	}

	// a plain event stream: no block; the status line takes the Done's
	// usage.
	s.fe.Notify(core.TextDelta{Text: "hi\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10}})
	s.await(th.Paint(SlotText, "huihui3.8") + th.Paint(SlotDim, " · ") + th.Paint(SlotDim, "10/262k"))
	if got := blockCount(); got != 1 {
		t.Fatalf("a plain turn reprinted the block: %d, want 1", got)
	}
	// the Compacted event: the compact line commits, the status line
	// takes the Kept — the block stands.
	s.fe.Notify(core.Compacted{Dropped: 100, Kept: 3400})
	s.await("3.4k/262k")
	if got := blockCount(); got != 1 {
		t.Fatalf("the Compacted event reprinted the block: %d, want 1", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	if got := int(calls.Load()); got != 1 {
		t.Fatalf("a turn moved the status door: %d calls, want 1", got)
	}

	// the refresh points, each exactly once. The dispatch runs inside
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
	s.await("model2")
	if got := int(calls.Load()); got != 2 {
		t.Fatalf("/new refreshed %d times, want 2 total", got)
	}
	s.si.feed("/sessions resume s3\n")
	s.await("resumed s3")
	if got := int(calls.Load()); got != 3 {
		t.Fatalf("/sessions resume refreshed %d times, want 3 total", got)
	}
	s.si.feed("/models m2\n")
	s.await("switched")
	if got := int(calls.Load()); got != 4 {
		t.Fatalf("/models m2 refreshed %d times, want 4 total", got)
	}
	// the compact command is not a refresh point (its event would
	// move the row's number): its output commits, the door does not.
	s.si.feed("/compact\n")
	s.await("nothing to drop")
	if got := int(calls.Load()); got != 4 {
		t.Fatalf("the compact command refreshed the status: %d, want 4", got)
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
	if got := int(calls.Load()); got != 4 {
		t.Fatalf("the models list refreshed the status: %d, want 4", got)
	}
	// the turn after the commands: the fresh context's row takes the
	// new Done's usage.
	s.si.feed("go2\n")
	select {
	case l := <-in:
		if l != "go2" {
			t.Fatalf("prompt after the commands = %q, want go2", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out on go2")
	}
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 20}})
	s.await(th.Paint(SlotText, "model2") + th.Paint(SlotDim, " · ") + th.Paint(SlotDim, "20/131k"))
	if got := blockCount(); got != 1 {
		t.Fatalf("the block count at the end = %d, want 1", got)
	}
	if got := int(calls.Load()); got != 4 {
		t.Fatalf("the status door moved at the end: %d calls, want 4", got)
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
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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

// TestMidTurnLinesSteer is the amended input case (decision 9, twice):
// a line typed during a live turn STEERS — established or not. The
// first-event gate silently queued a steer typed during the prefill
// (minutes, at depth); bracketed paste retired the gate's reason (a
// paste is one input). Latest wins: two quick mid-turn lines deliver
// as one steer, the last.
func TestMidTurnLinesSteer(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	mark := promptMark(th)

	// the turn starts; NO event has crossed (the prefill window).
	ctx, cancel := context.WithCancel(context.Background())
	ctx = core.WithInterrupt(ctx, cancel)
	saved := s.ctx
	s.ctx = ctx
	if got := s.prompt(mark, "a\n"); got != "a" {
		t.Fatalf("prompt one = %q, want a", got)
	}
	// two lines mid-prefill: both steer, latest wins, the turn is
	// interrupted.
	s.si.feed("b\nc\n")
	deadline := time.Now().Add(3 * time.Second)
	for ctx.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("the mid-prefill line did not interrupt the turn")
		}
		time.Sleep(time.Millisecond)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnInterrupt})
	s.ctx = saved
	line, err := s.input()
	if line != "c" || err != nil {
		t.Fatalf("the steer = (%q, %v), want c (latest wins)", line, err)
	}
}
func TestCtrlTogglesReasoning(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithNews(func(ctx context.Context) string { return news }),
		WithTicks(make(chan time.Time)))
	withNews.prompt(promptMark(th), "go\n")
	out := withNews.out.String()
	if got := strings.Count(out, th.Paint(SlotDim, news)); got != 1 {
		t.Fatalf("the news line occurs %d times, want exactly one, dim", got)
	}
	// after the block: the news line follows the startup block (its
	// greeting precedes it in the stream).
	if iM := strings.Index(out, "welcome to"); iM < 0 {
		t.Fatalf("the block is not in the stream")
	} else if iN := strings.Index(out, news); iN < iM {
		t.Fatalf("the news line is before the block")
	}

	noNews := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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
	// the slot's delivery started a turn (the TUI's view); end it —
	// under the amended rule a line typed into a live turn steers,
	// established or not, so the next prompt needs a quiet boundary.
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

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
// hand-wraps committed prose"): a delta that does not end on a line
// does not close it — the next delta of the same kind continues on
// the same line, and only a newline commits. The one departure from
// the CLI's bytes is the spacing rule (decision 2, amended): a
// reasoning block that ends and text that begins get one blank row
// between them, whether or not the model emitted a newline there.
func TestTextFlowsAsTheCLIDoes(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(100),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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
	// reasoning, then text with no newline between from the model: the
	// spacing rule closes the reasoning line and puts one blank row
	// before the text (the CLI would butt them: "rt").
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
	goIdx, helloIdx, rIdx, tIdx, usageIdx := idx("❯ go"), idx("hello"), idx("r"), idx("t"), idx("up 0 down 0 · cache r 0 0%")
	if helloIdx < 0 {
		t.Fatalf("the deltas did not flow into one line (the CLI's bytes):\n%q", v.rows)
	}
	if rIdx < 0 || tIdx < 0 {
		t.Fatalf("the reasoning and the text must land on their own lines (the spacing rule):\n%q", v.rows)
	}
	if tIdx != rIdx+2 || v.rows[rIdx+1] != "" {
		t.Fatalf("exactly one blank row between the reasoning and the text (r at %d, t at %d):\n%q", rIdx, tIdx, v.rows)
	}
	if !(goIdx >= 0 && goIdx < helloIdx && helloIdx < rIdx && tIdx < usageIdx) {
		t.Fatalf("the committed lines are out of order (go, hello, r, t, usage):\n%q", v.rows)
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
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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
	want := []string{
		"welcome to",
		"█▀▄ █ █▀▀",
		"█▀▄ █ █ █",
		"▀ ▀ ▀ ▀▀▀",
		"session 2f9a1c0e77b3",
		"", // the margin above the input, frozen with the prompt
		"❯ go",
		"", // the prompt's margin
		"aaaaaaaaaaaaaaaaaaaa",
		"aaaa",
		"bb",
		"", // the CLI's Done newline: unconditional, so a blank line
		"❯ ",
		"",                    // the margin under the input
		"huihui3.8 · 12/262k", // the status's first row, fed by the Done's usage
		// the second: the turn's usage (no committed usage line),
		// wrapping at 20 like everything else here.
		"up 10 down 2 · cache",
		" r 0 0%",
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
// on a line still gets the blank line after it, before the input; a
// tool-first turn gets the separator before the block). The turn's
// usage is the status's second row (decision 3, amended), not a
// committed line.
func TestDoneNewlineIsTheCLIs(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(100),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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
	// the turn's usage: the status's second row, the screen's last.
	usage := "up 10 down 2 · cache r 0 0%"
	if last := v.rows[len(v.rows)-1]; last != usage {
		t.Fatalf("the status's second row must carry the turn's usage: %q\n%q", last, v.rows)
	}
	// the Done newline: the blank row after "done", before the input.
	doneIdx := -1
	for i, l := range v.rows {
		if l == "done" {
			doneIdx = i
		}
	}
	if doneIdx < 0 || v.rows[doneIdx+1] != "" || !strings.HasPrefix(v.rows[doneIdx+2], "❯") {
		t.Fatalf("the CLI's Done newline did not land as a blank line after the text, before the input:\n%q", v.rows)
	}

	// the tool-first turn: the separator line stands before the block.
	s2 := newScriptedSession(t, WithTheme(th), WithWidth(100),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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

// subCmd is the Subber door on the fake set: a command with argument
// hints (the todo verbs).
type subCmd struct {
	fakeCmd
}

func (subCmd) Sub() []command.Sub {
	return []command.Sub{
		{Name: "read", Desc: "the queue"},
		{Name: "create", Desc: "the queue, the task's text"},
		{Name: "done", Desc: "a task's id"},
	}
}

// screenLines is the vt harness's replay of the stream (paint-free),
// with the trailing cleared rows trimmed (the harness's buffer grows
// but never shrinks; the screen's bottom is the last non-blank row).
func screenLines(t *testing.T, s *scriptedSession, width int) []string {
	t.Helper()
	v := newVT(width)
	v.feed(s.out.Bytes())
	if v.err != "" {
		t.Fatalf("harness: %s", v.err)
	}
	rows := v.rows
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

// awaitScreen blocks until the screen is exactly total rows and its
// last len(want) equal want (the reader and the paint are async; a
// lone Esc's grace window must expire before the next keystroke, so
// the test orders on the settled screen, not the byte it just fed).
func (s *scriptedSession) awaitScreen(width int, total int, want []string) {
	s.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		rows := screenLines(s.t, s, width)
		if len(rows) == total && len(rows) >= len(want) {
			tail := rows[len(rows)-len(want):]
			same := true
			for i := range want {
				if tail[i] != want[i] {
					same = false
					break
				}
			}
			if same {
				return
			}
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out awaiting %d screen rows ending %q:\n%q", total, want, rows)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestCompletionMenu is the amended decision 9 (SPEC_UX 5): two or
// more candidates show the menu (the ghost does not) with the
// selection inverted and the hint row naming the rule; Tab steps it
// down, Shift-Tab up, the arrows too — and that navigation is the
// pick: the Enter after it accepts the selection into the input,
// never dispatching. Without navigation the typed line is the intent:
// a complete command dispatches (the "accept and type again" loop,
// closed). Esc closes the menu (the input keeps its text); a single
// candidate shows the ghost's remainder — a name or a sub — and a
// known name plus a space shows the description when the command has
// no Sub() hints.
func TestCompletionMenu(t *testing.T) {
	th := oledTheme(t)
	models := &fakeCmd{name: "models", desc: "the per-model table"}
	moveC := &fakeCmd{name: "move", desc: "move a thing"}
	todo := &subCmd{fakeCmd: fakeCmd{name: "todo", out: "queue reply"}}
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{models, moveC, todo}, nil),
		WithTicks(make(chan time.Time)))
	go func() { _, _ = s.input() }()
	s.await(promptMark(th))
	row := func(name, desc string) string {
		return th.Paint(SlotAccent, name) + th.Paint(SlotText, "  "+desc)
	}
	status := "huihui3.8"
	usage := "up 214k down 18k · cache r 187k 87%"
	hint := "tab/↓ pick · enter runs"

	// two candidates: the menu shows, the selection inverted, first,
	// the hint row naming the rule. the screen: the block's two rows,
	// the two menu rows, the hint, the input, the status rows.
	s.si.feed("/mo")
	s.awaitScreen(50, 13, []string{"models  the per-model table", "move  move a thing", hint, "❯ /mo", "", status, usage})
	s.await(th.Invert(row("models", "the per-model table")))
	// Tab steps the selection down; Shift-Tab (CSI Z) steps it up.
	s.si.feed("\t")
	s.await(th.Invert(row("move", "move a thing")))
	s.si.feed("\x1b[Z")
	s.awaitScreen(50, 13, []string{"models  the per-model table", "move  move a thing", hint, "❯ /mo", "", status, usage})
	// Esc closes the menu; the input keeps its text. The lone Esc's
	// grace window must settle before the next keystroke (the screen
	// says when it has: the menu's rows are gone, the row count down).
	s.si.feed("\x1b")
	s.awaitScreen(50, 10, []string{"❯ /mo", "", status, usage})
	// a single candidate: the ghost — its remainder.
	s.si.feed("d")
	s.await(th.Paint(SlotDim, "els"))
	// Esc again: the prompt clears (the menu is already closed).
	s.si.feed("\x1b")
	s.awaitScreen(50, 10, []string{"❯ ", "", status, usage})

	// SPEC_UX 5: the navigation-intent rule — no navigation (no Tab,
	// no arrow): Enter dispatches the complete command, it does not
	// accept the first candidate (the "accept and type again" loop,
	// closed).
	s.si.feed("/todo ")
	s.awaitScreen(50, 14, []string{
		"read  the queue",
		"create  the queue, the task's text",
		"done  a task's id",
		hint,
		"❯ /todo ", "", status, usage,
	})
	s.si.feed("\n")
	// the dispatch commits the reply (the todo door's renderer falls
	// back to the raw reply when it is not queue text).
	s.await("queue reply")
	if todo.calls != 1 {
		t.Fatalf("the no-nav Enter dispatched %d times, want 1 (the complete command runs)", todo.calls)
	}

	// the Sub() hints (the argument phase): the menu over the verbs —
	// over the dispatch's committed lines, the buffer stands at 22.
	s.si.feed("/todo ")
	s.awaitScreen(50, 22, []string{
		"read  the queue",
		"create  the queue, the task's text",
		"done  a task's id",
		hint,
		"❯ /todo ", "", status, usage,
	})
	// Tab to create, Shift-Tab twice wraps to done (the cycle).
	s.si.feed("\t")
	s.await(th.Invert(row("create", "the queue, the task's text")))
	s.si.feed("\x1b[Z\x1b[Z")
	s.await(th.Invert(row("done", "a task's id")))
	// the navigation's Enter accepts the selection into the input —
	// never dispatching.
	s.si.feed("\n")
	s.awaitScreen(50, 18, []string{"❯ /todo done ", "", status, usage})
	if todo.calls != 1 {
		t.Fatalf("the accepted line dispatched: %d calls, want 1 (Enter after navigation accepts, it does not run)", todo.calls)
	}
	// a unique sub: the ghost — its remainder.
	for i := 0; i < 5; i++ {
		s.si.feed("\x7f")
	}
	s.si.feed("d")
	s.await(th.Paint(SlotDim, "one"))
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
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
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

	// the status rows stand below the input rows (the region's last
	// rows): the screen's tail is the input rows, then the status. Its
	// height at this width is the renderer's business; the test
	// measures it and slices above.
	statusRows := 1 // the margin under the input
	for _, r := range strings.Split(RemoveColor(RenderStatusLine(th, "huihui3.8", 0, 262144, false,
		214000, 18200, 187000)), "\n") {
		statusRows += (displayWidth(r) + 9) / 10
	}
	above := func(n int) []string {
		t.Helper()
		all := screenLast(n + statusRows)
		return all[:n]
	}
	// 17 runes at width 10: the input wraps to two terminal rows.
	s.si.feed(digits[:17])
	s.await(th.Paint(SlotText, " "+digits[:17]))
	rows := above(2)
	if rows[0] != "❯ 01234567" || rows[1] != "890123456" {
		t.Fatalf("wrapped input rows = %q", rows)
	}

	// 60 runes: seven logical rows; the five-row window follows the
	// cursor to the tail, the prompt glyph scrolled off.
	s.si.feed(digits[17:])
	s.await(th.Paint(SlotText, digits[18:]))
	rows = above(5)
	if rows[0] != "8901234567" || rows[4] != "XY" {
		t.Fatalf("tail window rows = %q", rows)
	}

	// home: the window scrolls back to the head, the glyph visible.
	s.si.feed("\x01")
	s.await(th.Paint(SlotText, " "+digits[:48]))
	rows = above(5)
	if rows[0] != "❯ 01234567" {
		t.Fatalf("head window rows = %q, want the prompt row first", rows)
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

// TestPasteAndEscKeybinds (decision 9's bracketed paste and Esc): a
// bracketed paste is ONE input — its newlines are text, shown as the
// return mark on the row and committed as real newlines — and Esc
// cancels the prompt whole. Un-bracketed input keeps the burst rule.
func TestPasteAndEscKeybinds(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	in := make(chan string, 1)
	go func() { l, _ := s.input(); in <- l }()
	s.await(promptMark(th))

	// a paste with a blank line and a numbered block: one row, marks.
	s.si.feed("\x1b[200~do the thing\n\n1) first\x1b[201~")
	s.await(th.Paint(SlotText, " do the thing⏎⏎1) first"))

	// Esc cancels it whole; what follows is a fresh prompt. The clear
	// lands after the grace window (the lone-Esc debounce), so the test
	// awaits the cancelled state before typing — bytes on the Esc's
	// heels would read as a sequence, exactly as at a real terminal.
	s.si.feed("\x1b")
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.fe.mu.Lock()
		cleared := s.fe.inputText == ""
		s.fe.mu.Unlock()
		if cleared {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out awaiting the Esc clear")
		}
		time.Sleep(time.Millisecond)
	}
	s.si.feed("ok\n")
	if line := <-in; line != "ok" {
		t.Fatalf("after Esc the line = %q, want ok (the paste cancelled)", line)
	}

	// a pasted Enter submits nothing; the typed Enter after it commits
	// the real newlines.
	go func() { l, _ := s.input(); in <- l }()
	s.si.feed("\x1b[200~two\nlines\x1b[201~\n")
	if line := <-in; line != "two\nlines" {
		t.Fatalf("the pasted line = %q, want the newline kept", line)
	}
}

// TestPagerCopyMode (the copy-mode, amended): PgUp opens the alt
// screen over the committed history with the live region pinned under
// it as a footer; the history follows the session while it is up (a
// commit lands in the pager's frame, the footer follows the region);
// typing goes to the input; q with an empty input returns, and the
// main screen resumes with the queued commit.
func TestPagerCopyMode(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}
	s.fe.Notify(core.TextDelta{Text: "the early content\n"})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.await("the early content")

	base := len(s.out.String())
	s.si.feed("\x1b[5~")
	s.await(altOn)
	after := s.out.String()[base:]
	if !strings.Contains(after, "the early content") {
		t.Fatalf("the pager frame must render the history: %q", after)
	}
	// the footer: the input and the status rows, under the pager's
	// status row.
	if !strings.Contains(after, "history · pgup/pgdn · q returns") || !strings.Contains(after, promptMark(th)) {
		t.Fatalf("the pager frame must pin the live region under the history: %q", after)
	}

	// an event during the pager: the frame follows (the commit lands
	// in the history, the pager repaints).
	mark := len(s.out.String())
	s.fe.Notify(core.TextDelta{Text: "while paging\n"})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.await("while paging")
	if got := s.out.String()[mark:]; !strings.Contains(got, altOn[:3]) && !strings.Contains(got, clearAll) {
		t.Fatalf("the pager must repaint on the commit: %q", got)
	}

	// typing goes to the input (q is a letter once something is typed).
	s.si.feed("aq")
	s.await(th.Paint(SlotText, " aq"))
	if strings.Contains(s.out.String()[mark:], altOff) {
		t.Fatalf("q with text typed must not exit the pager")
	}
	// clear it, then q returns: the alt screen closes.
	s.si.feed("\x15") // ^U
	s.si.feed("q")
	s.await(altOff)
}

// TestCompactingLoader (SPEC_COMPACT 5, amended): the Compacting cue
// places the loader. During a live turn the phase changes; on the
// verb's door (no turn) an activity row appears for the duration and
// leaves with the Compacted commit.
func TestCompactingLoader(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	go func() { _, _ = s.input() }()
	s.await(promptMark(th))

	// the verb door: no live turn. The cue places the loader row.
	s.fe.Notify(core.Compacting{})
	s.await("compacting")
	screen := func() []string {
		t.Helper()
		v := newVT(50)
		v.feed(s.out.Bytes())
		if v.err != "" {
			t.Fatalf("harness: %s", v.err)
		}
		return v.rows
	}
	rows := screen()
	found := false
	for _, r := range rows {
		if strings.Contains(r, "compacting") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the loader row must be on screen: %q", rows)
	}

	// the Compacted commit removes the loader (liveLines drops it).
	s.fe.Notify(core.Compacted{Summary: "s", Dropped: 100, Kept: 10})
	s.await("compact:")
	rows = screen()
	for _, r := range rows {
		if strings.Contains(r, "compacting") {
			t.Fatalf("the loader row must leave with the commit: %q", rows)
		}
	}
}

// TestMenuRowsFitTheWidth (decision 10: the live region is measured):
// a menu row is one terminal row — a long description is dotted to
// what the width leaves after the name, never wrapped, so the six-row
// cap is six terminal rows.
func TestMenuRowsFitTheWidth(t *testing.T) {
	th := oledTheme(t)
	long := &fakeCmd{name: "models", desc: strings.Repeat("a long description ", 6)}
	moveC := &fakeCmd{name: "move", desc: "short"}
	s := newScriptedSession(t, WithTheme(th), WithWidth(30),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{long, moveC}, nil),
		WithTicks(make(chan time.Time)))
	go func() { _, _ = s.input() }()
	s.await(promptMark(th))
	s.si.feed("/mo")
	// the block's two rows + the margin + two menu rows + the hint
	// row + the input + the margin + the status's three rows at this
	// width = 11: the long menu row did not wrap into a twelfth.
	s.awaitScreen(30, 14, []string{"move  short", "tab/↓ pick · enter runs", "❯ /mo", "", "huihui3.8", "up 214k down 18k · cache r 187", "k 87%"})
	rows := screenLines(t, s, 30)
	menuRow := rows[len(rows)-8]
	if !strings.HasPrefix(menuRow, "models  a long") || !strings.HasSuffix(menuRow, th.Glyph(GlyphDot)) {
		t.Fatalf("the long menu row must be dotted to the width: %q", menuRow)
	}
	if displayWidth(menuRow) > 30 {
		t.Fatalf("the menu row overflows the width: %d cols", displayWidth(menuRow))
	}
}

// TestGhostEnterCompletes (decision 9, amended): Enter over a single
// candidate's ghost submits the completed name — the row promised it —
// not the typed prefix as an unknown command.
func TestGhostEnterCompletes(t *testing.T) {
	th := oledTheme(t)
	models := &fakeCmd{name: "models", out: "the table"}
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{models}, nil),
		WithTicks(make(chan time.Time)))
	go func() { _, _ = s.input() }()
	s.await(promptMark(th))
	s.si.feed("/m")
	s.await(th.Paint(SlotDim, "odels"))
	s.si.feed("\n")
	s.await("the table")
	if models.calls != 1 {
		t.Fatalf("models ran %d times, want 1 (the ghost's Enter completes and dispatches)", models.calls)
	}
	if strings.Contains(s.out.String(), "unknown command") {
		t.Fatalf("the typed prefix must not dispatch as unknown:\n%s", s.out.String())
	}
}

// TestLoaderLocksAboveTheInput (decision 2, amended): the activity row
// sits directly above the input; the pending line streams above the
// activity row, so text flows into scrollback over the loader, never
// under it.
func TestLoaderLocksAboveTheInput(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}
	s.fe.Notify(core.TextDelta{Text: "streaming text"})
	// pending, then the loader, then the input, then the status.
	s.awaitScreen(50, 16, []string{"streaming text", "", "| thinking", "", "❯ ", "", "huihui3.8", "up 214k down 18k · cache r 187k 87%"})
	// the pending line closes into scrollback above; the loader stays
	// directly above the input.
	s.fe.Notify(core.TextDelta{Text: "\nmore"})
	s.awaitScreen(50, 17, []string{"streaming text", "more", "", "| thinking", "", "❯ ", "", "huihui3.8", "up 214k down 18k · cache r 187k 87%"})
}

// TestSpacingRule (decision 2, amended): the transcript never carries
// two blank rows in a row — a model's run of trailing newlines
// collapses to one — and a reasoning block that ends gets exactly one
// blank row before the text or the tool that follows, whether or not
// the model emitted a newline there. Reasoning is grey (decision 7).
func TestSpacingRule(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}
	// reasoning with a run of trailing newlines, then a tool: one
	// blank row between the reasoning and the block, not four.
	s.fe.Notify(core.ReasoningDelta{Text: "let me look\n\n\n\n"})
	s.fe.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash", Args: []byte(`{"command":"ls"}`)}})
	s.fe.Notify(core.ToolResult{ID: "c1", Content: "a\n", Duration: 0})
	// text right after the tool, then reasoning butting straight into
	// text with no newline: one blank row between them.
	s.fe.Notify(core.ReasoningDelta{Text: "so then"})
	s.fe.Notify(core.TextDelta{Text: "the answer\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.await("the answer")

	rows := screenLines(t, s, 60)
	// no two consecutive blank rows anywhere on the screen.
	for i := 1; i < len(rows); i++ {
		if rows[i] == "" && rows[i-1] == "" {
			t.Fatalf("two blank rows in a row at %d:\n%q", i, rows)
		}
	}
	// "let me look" then exactly one blank, then the tool block opens.
	find := func(prefix string) int {
		for i, r := range rows {
			if strings.HasPrefix(r, prefix) {
				return i
			}
		}
		return -1
	}
	look, block := find("let me look"), find("● bash")
	if look < 0 || block < 0 || block != look+2 || rows[look+1] != "" {
		t.Fatalf("reasoning -> one blank -> tool block (look %d, block %d):\n%q", look, block, rows)
	}
	// "so then" then exactly one blank, then "the answer".
	then, ans := find("so then"), find("the answer")
	if then < 0 || ans < 0 || ans != then+2 || rows[then+1] != "" {
		t.Fatalf("reasoning -> one blank -> text (then %d, ans %d):\n%q", then, ans, rows)
	}
	// the tool block's close, then exactly one blank, then the
	// reasoning that follows (the after-tool boundary).
	closeIdx := find("bash ")
	if closeIdx < 0 || then != closeIdx+2 || rows[closeIdx+1] != "" {
		t.Fatalf("tool close -> one blank -> next stream (close %d, then %d):\n%q", closeIdx, then, rows)
	}
	// reasoning is grey: the oled slot.
	if !strings.Contains(s.out.String(), th.Paint(SlotReasoning, "so then")) {
		t.Fatalf("reasoning must paint in the reasoning slot")
	}
	if got := th.SGR(SlotReasoning); !strings.Contains(got, "138;138;138") {
		t.Fatalf("oled's reasoning slot must be the grey: %q", got)
	}
}

// TestUsageRowIsLiveWithinTheTurn (decision 3): the status's usage row
// moves on each model call's Done, not only at the turn's close — a
// long agentic turn shows its running up/down and hit rate.
func TestUsageRowIsLiveWithinTheTurn(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}
	// the first model call of the turn: the row moves before TurnEnd.
	s.fe.Notify(core.TextDelta{Text: "one\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 1000, Completion: 50, CacheRead: 900}})
	s.await(th.Paint(SlotDim, "up 1.0k down 50 · cache r 900 90%"))
	// the second call: the totals accumulate, still mid-turn.
	s.fe.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash", Args: []byte(`{"command":"ls"}`)}})
	s.fe.Notify(core.ToolResult{ID: "c1", Content: "a\n"})
	s.fe.Notify(core.TextDelta{Text: "two\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 2000, Completion: 30, CacheRead: 1900}})
	s.await(th.Paint(SlotDim, "up 3.0k down 80 · cache r 2.8k 93%"))
	// the close keeps the turn's totals.
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	rows := screenLines(t, s, 60)
	if last := rows[len(rows)-1]; last != "up 3.0k down 80 · cache r 2.8k 93%" {
		t.Fatalf("the usage row after the close = %q, want the turn's totals", last)
	}
}

// TestVerbMenuOnTheWholeName (decision 9, amended): a complete command
// name with verbs opens the verb menu before the space is typed; an
// accept lands "/name verb ".
func TestVerbMenuOnTheWholeName(t *testing.T) {
	th := oledTheme(t)
	todo := &subCmd{fakeCmd: fakeCmd{name: "todo", desc: "the queue"}}
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{todo}, nil),
		WithTicks(make(chan time.Time)))
	go func() { _, _ = s.input() }()
	s.await(promptMark(th))
	s.si.feed("/todo")
	// the verb rows, the hint row, the input, then the margin and the
	// status.
	s.awaitScreen(50, 14, []string{
		"read  the queue",
		"create  the queue, the task's text",
		"done  a task's id",
		"tab/↓ pick · enter runs",
		"❯ /todo", "", "huihui3.8", "up 214k down 18k · cache r 187k 87%",
	})
	// Tab, Enter: the second verb, accepted with the name and space.
	s.si.feed("\t\n")
	s.await(th.Paint(SlotText, " /todo create "))
	if strings.Contains(s.out.String(), "queue reply") {
		t.Fatalf("the accept must not dispatch")
	}
}

// TestMargins (decision 2, amended): the region's groups stand apart —
// the prompt line commits with a blank under it, the loader has a blank
// above and below, the input a blank above and below — and never two
// blanks in a row.
func TestMargins(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	// idle: block, blank, input, blank, status.
	in := make(chan string, 1)
	go func() { l, _ := s.input(); in <- l }()
	s.await(promptMark(th))
	s.awaitScreen(60, 10, []string{"session 2f9a1c0e77b3", "", "❯ ", "", "huihui3.8", "up 214k down 18k · cache r 187k 87%"})
	s.si.feed("go\n")
	<-in
	s.fe.Notify(core.TextDelta{Text: "text"})
	// mid-turn: prompt, blank, pending, blank, loader, blank, input, blank, status.
	s.awaitScreen(60, 16, []string{"❯ go", "", "text", "", "| thinking", "", "❯ ", "", "huihui3.8", "up 214k down 18k · cache r 187k 87%"})
	s.fe.Notify(core.TextDelta{Text: "\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	// after: prompt, blank, text, blank (Done's), input, blank, status —
	// the Done blank and the input margin do not stack.
	s.awaitScreen(60, 14, []string{"❯ go", "", "text", "", "❯ ", "", "huihui3.8 · 12/262k", "up 10 down 2 · cache r 0 0%"})
	rows := screenLines(t, s, 60)
	for i := 1; i < len(rows); i++ {
		if rows[i] == "" && rows[i-1] == "" {
			t.Fatalf("two blank rows in a row at %d:\n%q", i, rows)
		}
	}
}

// TestMarkdownOnTheCommittedPath: the model's text decorates as it
// commits — a heading, a code fence (preformatted: dim, indented, never
// word-wrapped, the fence lines dropped), a bullet — and reasoning
// stays raw; an unclosed fence does not leak into the next turn.
func TestMarkdownOnTheCommittedPath(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(30),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}
	s.fe.Notify(core.TextDelta{Text: "# The plan\n"})
	s.fe.Notify(core.TextDelta{Text: "```go\n"})
	s.fe.Notify(core.TextDelta{Text: "func long_identifier_that_would_wrap_at_thirty() {}\n"})
	s.fe.Notify(core.TextDelta{Text: "```\n"})
	s.fe.Notify(core.TextDelta{Text: "- a **bold** item that is long enough to wrap\n"})
	s.fe.Notify(core.ReasoningDelta{Text: "*raw* reasoning\n"})
	s.fe.Notify(core.Done{})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.await("raw* reasoning")
	out := s.out.String()
	if !strings.Contains(out, th.Paint(SlotAccent, "The plan")) {
		t.Fatalf("the heading must paint accent, marks dropped")
	}
	// the fenced line commits whole (never word-wrapped), indented,
	// dim - the thinking's color, uniform (11, amended).
	if !strings.Contains(out, th.Paint(SlotDim, "  func long_identifier_that_would_wrap_at_thirty() {}")) {
		t.Fatalf("the fenced line must commit preformatted and dim:\n%s", out)
	}
	if strings.Contains(RemoveColor(out), "```") {
		t.Fatalf("the fence lines must drop")
	}
	if !strings.Contains(out, th.Paint(SlotBold, "bold")) {
		t.Fatalf("the bullet's bold must decorate")
	}
	if !strings.Contains(out, th.Paint(SlotReasoning, "*raw* reasoning")) {
		t.Fatalf("reasoning must stay raw")
	}
	rows := screenLines(t, s, 30)
	for _, r := range rows {
		if strings.HasPrefix(r, "  func") && displayWidth(r) <= 30 {
			continue
		}
		if displayWidth(r) > 30 && !strings.HasPrefix(r, "  func") {
			t.Fatalf("a prose row overflows the width (only the code line may): %q", r)
		}
	}
}

// TestRepaintSyncsTheSize (the two-client tmux race): a resize whose
// SIGWINCH has not landed yet must not leave a repaint on the stale
// width — every repaint re-reads the size before building rows, so
// the region's clear math matches the reflowed screen. The test
// injects a size change with no winch at all and asserts the next
// delta's repaint lays the region out at the new width.
func TestRepaintSyncsTheSize(t *testing.T) {
	th := oledTheme(t)
	w := 96
	s := newScriptedSession(t, WithTheme(th), WithWidth(96),
		WithSize(func() (int, int, bool) { return w, 30, true }),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}
	s.fe.Notify(core.TextDelta{Text: "before the resize\n"})
	s.await("before the resize")
	// the resize: no winch fires; the next repaint must pick it up.
	s.fe.mu.Lock()
	w = 40
	s.fe.mu.Unlock()
	s.fe.Notify(core.TextDelta{Text: "after the resize this line is long enough to wrap at forty\n"})
	s.await("forty")
	s.fe.mu.Lock()
	width, lw := s.fe.width, s.fe.live.width
	s.fe.mu.Unlock()
	if width != 40 || lw != 40 {
		t.Fatalf("the repaint did not sync the size: tui %d, live %d, want 40", width, lw)
	}
	// the committed prose wrapped at the new width (word wrap at 40).
	rows := screenLines(t, s, 40)
	found := false
	for _, r := range rows {
		if displayWidth(r) > 40 {
			t.Fatalf("a row overflows the new width: %q", r)
		}
		if strings.Contains(r, "after the resize") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the post-resize line is missing:\n%q", rows)
	}
}

// TestArrowsNavigateTheMenu (decision 9, amended): with the menu open
// the arrows step the selection — the window follows, so a long list
// pages — and Enter accepts the arrow-selected candidate; with the
// menu closed the arrows stay the history.
func TestArrowsNavigateTheMenu(t *testing.T) {
	th := oledTheme(t)
	todo := &subCmd{fakeCmd: fakeCmd{name: "todo", desc: "the queue"}}
	models := &fakeCmd{name: "models", desc: "the table"}
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{todo, models}, nil),
		WithTicks(make(chan time.Time)))
	go func() { _, _ = s.input() }()
	s.await(promptMark(th))
	row := func(name, desc string) string {
		return th.Paint(SlotAccent, name) + th.Paint(SlotText, "  "+desc)
	}
	// the verb menu on /todo: Down steps to the second verb, Up wraps back.
	s.si.feed("/todo")
	s.await(th.Invert(row("read", "the queue")))
	s.si.feed("\x1b[B") // Down
	s.await(th.Invert(row("create", "the queue, the task's text")))
	s.si.feed("\x1b[A") // Up
	s.awaitCount(th.Invert(row("read", "the queue")), 2)
	s.si.feed("\x1b[B\x1b[B") // Down Down -> done
	s.await(th.Invert(row("done", "a task's id")))
	// Enter accepts the arrow-selected verb, never dispatching.
	s.si.feed("\n")
	s.await(th.Paint(SlotText, " /todo done "))
	if strings.Contains(s.out.String(), "queue reply") {
		t.Fatalf("the accept must not dispatch")
	}
}

// TestReasoningStaysRawAndNeverLeaks (11, amended: the operator's
// simplification): reasoning is never decorated - its fences render
// raw grey - and a thought's unclosed fence never re-colors the
// answer (the miscolored-session bug).
func TestReasoningStaysRawAndNeverLeaks(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}
	s.fe.Notify(core.ReasoningDelta{Text: "**raw** thinking\n"})
	s.fe.Notify(core.ReasoningDelta{Text: "```go\n"})
	s.fe.Notify(core.ReasoningDelta{Text: "return nil\n"})
	// the fence never closes: the answer must still render as prose
	// (the miscolored-session bug: a thought's fence must not leak).
	s.fe.Notify(core.TextDelta{Text: "the **answer** in prose\n"})
	s.fe.Notify(core.Done{})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.await("prose")
	out := s.out.String()
	// reasoning is never decorated: its fence lines stay raw grey.
	if !strings.Contains(out, th.Paint(SlotReasoning, "```go")) {
		t.Fatalf("the reasoning's fence line must stay raw:\n%s", out)
	}
	if !strings.Contains(out, th.Paint(SlotReasoning, "**raw** thinking")) {
		t.Fatalf("the reasoning's inline markdown must stay raw")
	}
	if !strings.Contains(out, th.Paint(SlotBold, "answer")) {
		t.Fatalf("the answer must render as markdown prose (the thought's fence must not leak)")
	}
}

// TestEscInterruptsTheLiveTurn (decision 9, amended twice): Esc on an
// EMPTY prompt during a live turn interrupts it — no steer queued,
// stopping is not saying something; Esc with text still only clears
// the text (the ladder's lower rung), and the turn runs on.
func TestEscInterruptsTheLiveTurn(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	ctx, cancel := context.WithCancel(context.Background())
	ctx = core.WithInterrupt(ctx, cancel)
	saved := s.ctx
	s.ctx = ctx
	defer func() { s.ctx = saved }()
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}
	// Esc with text typed: clears only; the turn stays live.
	s.si.feed("half a thought")
	s.await(th.Paint(SlotText, " half a thought"))
	s.si.feed("\x1b")
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.fe.mu.Lock()
		cleared := s.fe.inputText == ""
		s.fe.mu.Unlock()
		if cleared {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Esc did not clear the text")
		}
		time.Sleep(time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("Esc with text must not interrupt the turn")
	}
	// Esc again, empty prompt: the interrupt.
	s.si.feed("\x1b")
	deadline = time.Now().Add(3 * time.Second)
	for ctx.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("Esc on the empty prompt did not interrupt the live turn")
		}
		time.Sleep(time.Millisecond)
	}
	// no steer was queued: after the turn ends, Input waits (nothing
	// in the slot), and a fresh line prompts normally.
	s.fe.Notify(core.TurnEnd{Reason: core.TurnInterrupt})
	s.ctx = saved
	if got := s.prompt(promptMark(th), "next\n"); got != "next" {
		t.Fatalf("the post-interrupt prompt = %q, want next (no slot leftovers)", got)
	}
}
