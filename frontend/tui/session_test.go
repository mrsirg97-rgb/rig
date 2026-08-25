package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

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

func promptMark(th Theme) string {
	return th.Paint(SlotEmber, th.Glyph(GlyphPrompt))
}

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

func TestStatusLineRefresh(t *testing.T) {
	th := oledTheme(t)

	blockRow := strings.Split(RenderStatus(th, statusFixture()), "\n")[0]

	model, window := "huihui3.8", 262144
	var calls atomic.Int32
	statusIn := func(ctx context.Context) StatusIn {
		n := calls.Add(1)
		if n == 1 {
			return StatusIn{
				Model: model, Effort: "xhigh", Window: window,
				Up: 214000, Down: 18200, CacheRead: 187000,
			}
		}

		return StatusIn{Model: "model2", Effort: "rf" + strconv.Itoa(int(n)), Window: 131072}
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

	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt one = %q, want go", got)
	}
	if got := blockCount(); got != 1 {
		t.Fatalf("the session start committed the block %d times, want 1", got)
	}

	s.fe.Notify(core.TextDelta{Text: "hi\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10}})
	s.await(th.Paint(SlotText, "huihui3.8") + th.Paint(SlotDim, " · ") + th.Paint(SlotDim, "10/262k"))
	if got := blockCount(); got != 1 {
		t.Fatalf("a plain turn reprinted the block: %d, want 1", got)
	}

	s.fe.Notify(core.Compacted{Dropped: 100, Kept: 3400})
	s.await("3.4k/262k")
	if got := blockCount(); got != 1 {
		t.Fatalf("the Compacted event reprinted the block: %d, want 1", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	if got := int(calls.Load()); got != 1 {
		t.Fatalf("a turn moved the status door: %d calls, want 1", got)
	}

	in := make(chan string, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		l, err := s.input()
		if err != nil {

			select {
			case <-done:
			default:
				s.t.Errorf("prompt: %v", err)
			}
		}
		in <- l
	}()
	s.await(promptMark(th))

	s.si.feed("/new\n")
	s.await("rf2")
	if got := int(calls.Load()); got != 2 {
		t.Fatalf("/new refreshed %d times, want 2 total", got)
	}
	s.si.feed("/sessions resume s3\n")
	s.await("rf3")
	if got := int(calls.Load()); got != 3 {
		t.Fatalf("/sessions resume refreshed %d times, want 3 total", got)
	}
	s.si.feed("/models m2\n")
	s.await("rf4")
	if got := int(calls.Load()); got != 4 {
		t.Fatalf("/models m2 refreshed %d times, want 4 total", got)
	}

	s.si.feed("/compact\n")
	s.await("nothing to drop")
	if got := int(calls.Load()); got != 4 {
		t.Fatalf("the compact command refreshed the status: %d, want 4", got)
	}

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

func TestMidTurnLinesSteer(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	mark := promptMark(th)

	ctx, cancel := context.WithCancel(context.Background())
	ctx = core.WithInterrupt(ctx, cancel)
	saved := s.ctx
	s.ctx = ctx
	if got := s.prompt(mark, "a\n"); got != "a" {
		t.Fatalf("prompt one = %q, want a", got)
	}

	s.si.feed("b\nc\n")
	deadline := time.Now().Add(3 * time.Second)
	for ctx.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("the mid-prefill line did not interrupt the turn")
		}
		time.Sleep(time.Millisecond)
	}
	waitSlot(t, s.fe, "c")
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
	s.si.feed(string(byte(0x14)))
	s.awaitReasoning(false)
	s.fe.Notify(core.ReasoningDelta{Text: "third thought"})
	s.si.feed(string(byte(0x14)))
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

	s.si.feed(string(byte(0x03)))
	s.awaitCtxDone(ctx)
	s.fe.Notify(core.TurnEnd{Reason: core.TurnInterrupt})

	s.ctx = saved
	line, err := s.input()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("after Ctrl-C the input = (%q, %v), want io.EOF", line, err)
	}
}

func TestCtrlDEmptyExits(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q, want go", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	s.si.feed(string(byte(0x04)))
	line, err := s.input()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Ctrl-D at the empty prompt: (%q, %v), want io.EOF", line, err)
	}
	_ = line
}

func TestCtrlDNonBlankKept(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "keep me\x04\n"); got != "keep me" {
		t.Fatalf("Ctrl-D on a non-blank line = %q, want keep me (kept)", got)
	}
}

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

	s.si.feed("/nope\n")
	s.await("unknown command: nope")
	out := s.out.String()
	if strings.Contains(out, th.Paint(SlotDim, "/nope")) {
		t.Fatalf("the command line must not echo a second dim copy")
	}
	if !strings.Contains(out, th.Paint(SlotDim, "unknown command: nope (known: new)")) {
		t.Fatalf("the unknown command is not loud with the known set:\n%s", out)
	}

	s.si.feed("/new\n")
	s.await(th.Paint(SlotText, "new session: s2"))
	if !strings.Contains(s.out.String(), th.Paint(SlotText, "new session: s2")) {
		t.Fatalf("the command output is not committed as the CLI's bytes")
	}

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

	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

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
	fe.Steer("second")
	s.fe.Notify(core.TurnEnd{Reason: core.TurnInterrupt})

	s.ctx = saved
	line, err := s.input()
	if line != "second" || err != nil {
		t.Fatalf("after the steering: (%q, %v), want second (latest wins)", line, err)
	}

	if !fe.LiveTurn() {
		t.Fatal("LiveTurn after the delivered line, want true (the next turn has started)")
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	if fe.LiveTurn() {
		t.Fatal("LiveTurn after the turn end, want false")
	}

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

	s.fe.Notify(core.TextDelta{Text: "hel"})
	s.fe.Notify(core.TextDelta{Text: "lo\n"})

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
		"workers: none",
		"chat with your model",
		", or type / for comm",
		"ands",
		"",
		"❯ go",
		"",
		"aaaaaaaaaaaaaaaaaaaa",
		"aaaa",
		"bb",
		"",
		"❯ ",
		"",

		"huihui3.8 · 12/262k",
		"xhigh · default · au",
		"to",

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

	usage := "up 10 down 2 · cache r 0 0%"
	if last := v.rows[len(v.rows)-1]; last != usage {
		t.Fatalf("the status's second row must carry the turn's usage: %q\n%q", last, v.rows)
	}

	doneIdx := -1
	for i, l := range v.rows {
		if l == "done" {
			doneIdx = i
		}
	}
	if doneIdx < 0 || v.rows[doneIdx+1] != "" || !strings.HasPrefix(v.rows[doneIdx+2], "❯") {
		t.Fatalf("the CLI's Done newline did not land as a blank line after the text, before the input:\n%q", v.rows)
	}

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
	stance := "xhigh · default · auto"
	usage := "up 214k down 18k · cache r 187k 87%"
	hint := "tab/↓ pick · enter runs"

	s.si.feed("/mo")
	s.awaitScreen(50, 16, []string{"models  the per-model table", "move  move a thing", hint, "❯ /mo", "", status, stance, usage})
	s.await(th.Invert(row("models", "the per-model table")))

	s.si.feed("\t")
	s.await(th.Invert(row("move", "move a thing")))
	s.si.feed("\x1b[Z")
	s.awaitScreen(50, 16, []string{"models  the per-model table", "move  move a thing", hint, "❯ /mo", "", status, stance, usage})

	s.si.feed("\x1b")
	s.awaitScreen(50, 13, []string{"❯ /mo", "", status, stance, usage})

	s.si.feed("d")
	s.await(th.Paint(SlotDim, "els"))

	s.si.feed("\x1b")
	s.awaitScreen(50, 13, []string{"❯ ", "", status, stance, usage})

	s.si.feed("/todo ")
	s.awaitScreen(50, 17, []string{
		"read  the queue",
		"create  the queue, the task's text",
		"done  a task's id",
		hint,
		"❯ /todo ", "", status, stance, usage,
	})
	s.si.feed("\n")

	s.await("queue reply")
	if todo.calls != 1 {
		t.Fatalf("the no-nav Enter dispatched %d times, want 1 (the complete command runs)", todo.calls)
	}

	s.si.feed("/todo ")
	s.awaitScreen(50, 25, []string{
		"read  the queue",
		"create  the queue, the task's text",
		"done  a task's id",
		hint,
		"❯ /todo ", "", status, stance, usage,
	})

	s.si.feed("\t")
	s.await(th.Invert(row("create", "the queue, the task's text")))
	s.si.feed("\x1b[Z\x1b[Z")
	s.await(th.Invert(row("done", "a task's id")))

	s.si.feed("\n")
	s.awaitScreen(50, 21, []string{"❯ /todo done ", "", status, stance, usage})
	if todo.calls != 1 {
		t.Fatalf("the accepted line dispatched: %d calls, want 1 (Enter after navigation accepts, it does not run)", todo.calls)
	}

	for i := 0; i < 5; i++ {
		s.si.feed("\x7f")
	}
	s.si.feed("d")
	s.await(th.Paint(SlotDim, "one"))
}

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

	digits := strings.Repeat("0123456789", 5) + "01234567XY"

	statusRows := 1
	for _, r := range strings.Split(RemoveColor(RenderStatusLine(th, "huihui3.8", "xhigh", "", "", 0, 262144, false,
		214000, 18200, 187000)), "\n") {
		statusRows += (displayWidth(r) + 9) / 10
	}
	above := func(n int) []string {
		t.Helper()
		all := screenLast(n + statusRows)
		return all[:n]
	}

	s.si.feed(digits[:17])
	s.await(th.Paint(SlotText, " "+digits[:17]))
	rows := above(2)
	if rows[0] != "❯ 01234567" || rows[1] != "890123456" {
		t.Fatalf("wrapped input rows = %q", rows)
	}

	s.si.feed(digits[17:])
	s.await(th.Paint(SlotText, digits[18:]))
	rows = above(5)
	if rows[0] != "8901234567" || rows[4] != "XY" {
		t.Fatalf("tail window rows = %q", rows)
	}

	s.si.feed("\x01")
	s.await(th.Paint(SlotText, " "+digits[:48]))
	rows = above(5)
	if rows[0] != "❯ 01234567" {
		t.Fatalf("head window rows = %q, want the prompt row first", rows)
	}

	s.si.feed("Z")
	s.await(th.Paint(SlotText, " Z"+digits[:47]))
	s.si.feed("\n")
	if line := <-in; line != "Z"+digits {
		t.Fatalf("the committed line = %q, want the full text", line)
	}
}

func TestPasteAndEscKeybinds(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	in := make(chan string, 1)
	go func() { l, _ := s.input(); in <- l }()
	s.await(promptMark(th))

	s.si.feed("\x1b[200~do the thing\n\n1) first\x1b[201~")
	s.await(th.Paint(SlotText, " do the thing⏎⏎1) first"))

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

	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	go func() { l, _ := s.input(); in <- l }()
	s.si.feed("\x1b[200~two\nlines\x1b[201~\n")
	if line := <-in; line != "two\nlines" {
		t.Fatalf("the pasted line = %q, want the newline kept", line)
	}
}

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

	if !strings.Contains(after, "history · pgup/pgdn · q returns") || !strings.Contains(after, promptMark(th)) {
		t.Fatalf("the pager frame must pin the live region under the history: %q", after)
	}

	mark := len(s.out.String())
	s.fe.Notify(core.TextDelta{Text: "while paging\n"})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.await("while paging")
	if got := s.out.String()[mark:]; !strings.Contains(got, altOn[:3]) && !strings.Contains(got, clearAll) {
		t.Fatalf("the pager must repaint on the commit: %q", got)
	}

	s.si.feed("aq")
	s.await(th.Paint(SlotText, " aq"))
	if strings.Contains(s.out.String()[mark:], altOff) {
		t.Fatalf("q with text typed must not exit the pager")
	}

	s.si.feed("\x15")
	s.si.feed("q")
	s.await(altOff)
}

func TestCompactingLoader(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	go func() { _, _ = s.input() }()
	s.await(promptMark(th))

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

	s.fe.Notify(core.Compacted{Summary: "s", Dropped: 100, Kept: 10})
	s.await("compact:")
	rows = screen()
	for _, r := range rows {
		if strings.Contains(r, "compacting") {
			t.Fatalf("the loader row must leave with the commit: %q", rows)
		}
	}
}

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

	s.awaitScreen(30, 18, []string{"move  short", "tab/↓ pick · enter runs", "❯ /mo", "", "huihui3.8", "xhigh · default · auto", "up 214k down 18k · cache r 187", "k 87%"})
	rows := screenLines(t, s, 30)
	menuRow := rows[len(rows)-9]
	if !strings.HasPrefix(menuRow, "models  a long") || !strings.HasSuffix(menuRow, th.Glyph(GlyphDot)) {
		t.Fatalf("the long menu row must be dotted to the width: %q", menuRow)
	}
	if displayWidth(menuRow) > 30 {
		t.Fatalf("the menu row overflows the width: %d cols", displayWidth(menuRow))
	}
}

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

func TestLoaderLocksAboveTheInput(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}
	s.fe.Notify(core.TextDelta{Text: "streaming text"})

	s.awaitScreen(50, 19, []string{"streaming text", "", "| thinking", "", "❯ ", "", "huihui3.8", "xhigh · default · auto", "up 214k down 18k · cache r 187k 87%"})

	s.fe.Notify(core.TextDelta{Text: "\nmore"})
	s.awaitScreen(50, 20, []string{"streaming text", "more", "", "| thinking", "", "❯ ", "", "huihui3.8", "xhigh · default · auto", "up 214k down 18k · cache r 187k 87%"})
}

func TestSpacingRule(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}

	s.fe.Notify(core.ReasoningDelta{Text: "let me look\n\n\n\n"})
	s.fe.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash", Args: []byte(`{"command":"ls"}`)}})
	s.fe.Notify(core.ToolResult{ID: "c1", Content: "a\n", Duration: 0})

	s.fe.Notify(core.ReasoningDelta{Text: "so then"})
	s.fe.Notify(core.TextDelta{Text: "the answer\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.await("the answer")

	rows := screenLines(t, s, 60)

	for i := 1; i < len(rows); i++ {
		if rows[i] == "" && rows[i-1] == "" {
			t.Fatalf("two blank rows in a row at %d:\n%q", i, rows)
		}
	}

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

	then, ans := find("so then"), find("the answer")
	if then < 0 || ans < 0 || ans != then+2 || rows[then+1] != "" {
		t.Fatalf("reasoning -> one blank -> text (then %d, ans %d):\n%q", then, ans, rows)
	}

	closeIdx := find("bash ")
	if closeIdx < 0 || then != closeIdx+2 || rows[closeIdx+1] != "" {
		t.Fatalf("tool close -> one blank -> next stream (close %d, then %d):\n%q", closeIdx, then, rows)
	}

	if !strings.Contains(s.out.String(), th.Paint(SlotReasoning, "so then")) {
		t.Fatalf("reasoning must paint in the reasoning slot")
	}
	if got := th.SGR(SlotReasoning); !strings.Contains(got, "138;138;138") {
		t.Fatalf("oled's reasoning slot must be the grey: %q", got)
	}
}

func TestUsageRowIsLiveWithinTheTurn(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}

	s.fe.Notify(core.TextDelta{Text: "one\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 1000, Completion: 50, CacheRead: 900}})
	s.await(th.Paint(SlotDim, "up 1.0k down 50 · cache r 900 90%"))

	s.fe.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash", Args: []byte(`{"command":"ls"}`)}})
	s.fe.Notify(core.ToolResult{ID: "c1", Content: "a\n"})
	s.fe.Notify(core.TextDelta{Text: "two\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 2000, Completion: 30, CacheRead: 1900}})
	s.await(th.Paint(SlotDim, "up 3.0k down 80 · cache r 2.8k 93%"))

	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	rows := screenLines(t, s, 60)
	if last := rows[len(rows)-1]; last != "up 3.0k down 80 · cache r 2.8k 93%" {
		t.Fatalf("the usage row after the close = %q, want the turn's totals", last)
	}
}

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

	s.awaitScreen(50, 17, []string{
		"read  the queue",
		"create  the queue, the task's text",
		"done  a task's id",
		"tab/↓ pick · enter runs",
		"❯ /todo", "", "huihui3.8", "xhigh · default · auto", "up 214k down 18k · cache r 187k 87%",
	})

	s.si.feed("\t\n")
	s.await(th.Paint(SlotText, " /todo create "))
	if strings.Contains(s.out.String(), "queue reply") {
		t.Fatalf("the accept must not dispatch")
	}
}

func TestMargins(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, WithTheme(th), WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))

	in := make(chan string, 1)
	go func() { l, _ := s.input(); in <- l }()
	s.await(promptMark(th))
	s.awaitScreen(60, 13, []string{"session 2f9a1c0e77b3", "workers: none", "chat with your model, or type / for commands", "", "❯ ", "", "huihui3.8", "xhigh · default · auto", "up 214k down 18k · cache r 187k 87%"})
	s.si.feed("go\n")
	<-in
	s.fe.Notify(core.TextDelta{Text: "text"})

	s.awaitScreen(60, 19, []string{"❯ go", "", "text", "", "| thinking", "", "❯ ", "", "huihui3.8", "xhigh · default · auto", "up 214k down 18k · cache r 187k 87%"})
	s.fe.Notify(core.TextDelta{Text: "\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	s.awaitScreen(60, 17, []string{"❯ go", "", "text", "", "❯ ", "", "huihui3.8 · 12/262k", "xhigh · default · auto", "up 10 down 2 · cache r 0 0%"})
	rows := screenLines(t, s, 60)
	for i := 1; i < len(rows); i++ {
		if rows[i] == "" && rows[i-1] == "" {
			t.Fatalf("two blank rows in a row at %d:\n%q", i, rows)
		}
	}
}

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

	s.si.feed("/todo")
	s.await(th.Invert(row("read", "the queue")))
	s.si.feed("\x1b[B")
	s.await(th.Invert(row("create", "the queue, the task's text")))
	s.si.feed("\x1b[A")
	s.awaitCount(th.Invert(row("read", "the queue")), 2)
	s.si.feed("\x1b[B\x1b[B")
	s.await(th.Invert(row("done", "a task's id")))

	s.si.feed("\n")
	s.await(th.Paint(SlotText, " /todo done "))
	if strings.Contains(s.out.String(), "queue reply") {
		t.Fatalf("the accept must not dispatch")
	}
}

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

	s.fe.Notify(core.TextDelta{Text: "the **answer** in prose\n"})
	s.fe.Notify(core.Done{})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.await("prose")
	out := s.out.String()

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

	s.si.feed("\x1b")
	deadline = time.Now().Add(3 * time.Second)
	for ctx.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("Esc on the empty prompt did not interrupt the live turn")
		}
		time.Sleep(time.Millisecond)
	}

	s.fe.Notify(core.TurnEnd{Reason: core.TurnInterrupt})
	s.ctx = saved
	if got := s.prompt(promptMark(th), "next\n"); got != "next" {
		t.Fatalf("the post-interrupt prompt = %q, want next (no slot leftovers)", got)
	}
}

func TestAskDoor(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptedSession(t, WithTheme(th), WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTicks(make(chan time.Time)))
	go func() { _, _ = s.input() }()
	s.await(promptMark(th))

	ask := func(feed string) bool {
		res := make(chan bool, 1)
		go func() { res <- s.fe.Ask(context.Background(), "bash {\"cmd\":\"go test\"}") }()
		s.await("approve bash")
		for _, r := range feed {
			s.si.feed(string(r))
		}
		select {
		case v := <-res:
			return v
		case <-time.After(3 * time.Second):
			t.Fatalf("the ask never resolved on %q", feed)
			return false
		}
	}
	if !ask("y") {
		t.Fatal("y must approve")
	}
	s.out.Reset()
	if ask("n") {
		t.Fatal("n must decline")
	}
	s.out.Reset()

	if !ask("x7y") {
		t.Fatal("y after swallowed keys must approve")
	}
	if got := s.fe.ed.text(); got != "" {
		t.Fatalf("the swallowed keys must not reach the input: %q", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan bool, 1)
	go func() { res <- s.fe.Ask(ctx, "write {\"path\":\"x\"}") }()
	s.out.Reset()
	s.await("approve write")
	cancel()
	select {
	case v := <-res:
		if v {
			t.Fatal("a dead context is a decline")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the ask must resolve when the context ends")
	}
}

func waitSlot(t *testing.T, tu *tui, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		tu.mu.Lock()
		got, has := tu.slot, tu.hasSlot
		tu.mu.Unlock()
		if has && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the slot never held %q (last %q)", want, got)
		}
		time.Sleep(time.Millisecond)
	}
}
