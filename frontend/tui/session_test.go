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

	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/core"
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
		th, WithWidth(50),
		WithStatus(statusIn),
		WithCommands([]core.Command{newC, sessC, modelsC, compactC}, nil),
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
	s.tick()
	s.await(th.Paint(SlotText, "huihui3.8") + th.Paint(SlotDim, " · ") + th.Paint(SlotDim, "10/262k"))
	if got := blockCount(); got != 1 {
		t.Fatalf("a plain turn reprinted the block: %d, want 1", got)
	}

	s.fe.Notify(core.Compacted{Dropped: 100, Kept: 3400})
	s.tick()
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
	if got := int(calls.Load()); got != 5 {
		t.Fatalf("the generic rule recaptures after any command: %d calls, want 5", got)
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
	if got := int(calls.Load()); got != 6 {
		t.Fatalf("the models list recaptures too: %d calls, want 6", got)
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
	s.tick()
	s.await(th.Paint(SlotText, "model2") + th.Paint(SlotDim, " · ") + th.Paint(SlotDim, "20/131k"))
	if got := blockCount(); got != 1 {
		t.Fatalf("the block count at the end = %d, want 1", got)
	}
	if got := int(calls.Load()); got != 6 {
		t.Fatalf("the status door moved at the end: %d calls, want 6", got)
	}
}

func TestWithTitleCustomRowsTaglineAndFallbackName(t *testing.T) {
	th := oledTheme(t)
	rows := []string{
		"▄▀█ █▀▀ █▀▀",
		"█▀█ █▄▄ █ █",
		"▀ ▀ ▀▀▀ ▀▀▀",
	}
	name, tagline := "orbit", "the app"
	s := newScriptedSession(t, th, WithWidth(100),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTitle(name, rows, tagline),
	)
	s.prompt(promptMark(th), "go\n")

	for i, row := range rows {
		if got := bytes.Count(s.out.Bytes(), []byte(th.Paint(SlotEmber, row))); got != 1 {
			t.Fatalf("custom title row %d committed %d times, want 1:\n%s", i, got, s.out.String())
		}
	}
	if got := bytes.Count(s.out.Bytes(), []byte(th.Paint(SlotDim, tagline))); got != 1 {
		t.Fatalf("the tagline committed %d times, want 1:\n%s", got, s.out.String())
	}
	if got := bytes.Count(s.out.Bytes(), []byte(th.Paint(SlotEmber, "█▀▄ █ █▀▀"))); got != 0 {
		t.Fatalf("the default rig row still renders %d time(s):\n%s", got, s.out.String())
	}

	ath, err := ResolveTheme("", []byte(`{"base":"oled","glyphs":"ascii"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	s2 := newScriptedSession(t, ath, WithWidth(100),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithTitle(name, rows, tagline),
	)
	s2.prompt(promptMark(ath), "go\n")
	if got := bytes.Count(s2.out.Bytes(), []byte(ath.Paint(SlotEmber, name))); got != 1 {
		t.Fatalf("the fallback name committed %d times, want 1:\n%s", got, s2.out.String())
	}
	if got := bytes.Count(s2.out.Bytes(), []byte(ath.Paint(SlotEmber, rows[0]))); got != 0 {
		t.Fatalf("the ascii fallback printed the art row %d time(s):\n%s", got, s2.out.String())
	}
}

func TestFlowCoalescesDeltas(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(50))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}

	frames := func() int { return bytes.Count(s.out.Bytes(), []byte(syncOn)) }
	before := frames()

	s.fe.Notify(core.TextDelta{Text: "hel"})
	s.fe.Notify(core.TextDelta{Text: "lo"})
	if got := frames(); got != before {
		t.Fatalf("deltas painted before the frame tick: %d frames, want %d", got, before)
	}

	s.tick()
	deadline := time.Now().Add(3 * time.Second)
	for frames() != before+1 {
		if time.Now().After(deadline) {
			t.Fatalf("the frame tick never painted: %d frames, want %d", frames(), before+1)
		}
		time.Sleep(time.Millisecond)
	}
	stream := s.out.String()
	mid := strings.TrimSuffix(strings.TrimPrefix(stream[strings.LastIndex(stream, syncOn):], syncOn), syncOff)
	if !strings.Contains(mid, "hel") || !strings.Contains(mid, "lo") {
		t.Fatalf("the coalesced frame lost a delta: %q", mid)
	}
}

func TestMidTurnLinesSteer(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q, want go", got)
	}

	s.fe.Notify(core.ReasoningDelta{Text: "first thought"})
	s.fe.Notify(core.ReasoningDelta{Text: "second thought"})
	s.tick()
	s.await("first thought")
	s.si.feed(string(byte(0x14)))
	s.awaitReasoning(false)
	s.fe.Notify(core.ReasoningDelta{Text: "third thought"})
	s.si.feed(string(byte(0x14)))
	s.awaitReasoning(true)
	s.fe.Notify(core.ReasoningDelta{Text: "fourth thought"})
	s.tick()
	s.await("fourth thought")

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
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "keep me\x04\n"); got != "keep me" {
		t.Fatalf("Ctrl-D on a non-blank line = %q, want keep me (kept)", got)
	}
}

func TestDispatchVoice(t *testing.T) {
	th := oledTheme(t)
	newC := &fakeCmd{name: "new", out: "new session: s2"}
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{newC}, nil),
	)
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

func TestWidePendingLineWrapsClean(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(20),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{models, moveC, todo}, nil),
	)
	go func() { _, _ = s.input() }()
	s.await(promptMark(th))
	row := func(name, desc string) string {
		return th.Paint(SlotEmber, name) + th.Paint(SlotText, "  "+desc)
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
	s.awaitScreen(50, 22, []string{
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
	s.awaitScreen(50, 18, []string{"❯ /todo done ", "", status, stance, usage})
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
	s := newScriptedSession(t, th, WithWidth(10),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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
		214000, 18200, 187000, 0)), "\n") {
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
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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

func TestMenuRowsFitTheWidth(t *testing.T) {
	th := oledTheme(t)
	long := &fakeCmd{name: "models", desc: strings.Repeat("a long description ", 6)}
	moveC := &fakeCmd{name: "move", desc: "short"}
	s := newScriptedSession(t, th, WithWidth(30),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{long, moveC}, nil),
	)
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
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithCommands([]core.Command{models}, nil),
	)
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

func TestSpacingRule(t *testing.T) {
	th := oledTheme(t)
	for _, tc := range []struct {
		name string
		end  string
	}{
		{"trailing newline", "the answer\n"},
		{"trailing blank line", "the answer\n\n"},
		{"no trailing newline", "the answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newScriptedSession(t, th, WithWidth(60),
				WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
			)
			if got := s.prompt(promptMark(th), "go\n"); got != "go" {
				t.Fatalf("the prompt = %q, want go", got)
			}

			s.fe.Notify(core.TextDelta{Text: tc.end})
			s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 10, Completion: 2}})
			s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
			s.tick()
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
			ans := find("the answer")
			promptIdx := -1
			for i, r := range rows {
				if strings.HasPrefix(r, th.Glyph(GlyphPrompt)) {
					promptIdx = i
				}
			}
			if ans < 0 || promptIdx < 0 || promptIdx != ans+2 || rows[promptIdx-1] != "" || rows[promptIdx-2] != "the answer" {
				t.Fatalf("exactly one blank row before the prompt (ans %d, prompt %d):\n%q", ans, promptIdx, rows)
			}
		})
	}
}

func TestBlockSpacingRule(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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
	s.tick()
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

func TestParallelToolBlocksSpaceApart(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}

	s.fe.Notify(core.TextDelta{Text: "hello\n"})
	s.fe.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "read", Args: []byte(`{"path":"/tmp/a"}`)}})
	s.fe.Notify(core.ToolStart{Call: core.ToolCall{ID: "c2", Name: "read", Args: []byte(`{"path":"/tmp/b"}`)}})
	s.fe.Notify(core.ToolResult{ID: "c1", Content: "a\n", Duration: 0})
	s.fe.Notify(core.ToolResult{ID: "c2", Content: "b\n", Duration: 0})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.tick()
	s.await(th.Paint(SlotDim, "  b"))

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
	hello, first := find("hello"), find("● read · /tmp/a")
	if hello < 0 || first < 0 || first != hello+2 || rows[hello+1] != "" {
		t.Fatalf("prose -> one blank -> the first block (hello %d, block %d):\n%q", hello, first, rows)
	}
	close1, second := find("read "), find("● read · /tmp/b")
	if close1 < 0 || second < 0 || second != close1+2 || rows[close1+1] != "" {
		t.Fatalf("one blank between the two parallel blocks (close %d, next %d):\n%q", close1, second, rows)
	}
}

func TestUsageRowIsLiveWithinTheTurn(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("the prompt = %q, want go", got)
	}

	s.fe.Notify(core.TextDelta{Text: "one\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 1000, Completion: 50, CacheRead: 900}})
	s.tick()
	s.await(th.Paint(SlotDim, "up 1.0k down 50 · cache r 900 90%"))

	s.fe.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash", Args: []byte(`{"command":"ls"}`)}})
	s.fe.Notify(core.ToolResult{ID: "c1", Content: "a\n"})
	s.fe.Notify(core.TextDelta{Text: "two\n"})
	s.fe.Notify(core.Done{Usage: core.Usage{Prompt: 2000, Completion: 30, CacheRead: 1900}})
	s.tick()
	s.await(th.Paint(SlotDim, "up 3.0k down 80 · cache r 2.8k 93%"))

	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	rows := screenLines(t, s, 60)
	if last := rows[len(rows)-1]; last != "up 3.0k down 80 · cache r 2.8k 93%" {
		t.Fatalf("the usage row after the close = %q, want the turn's totals", last)
	}
}

func TestMarkdownOnTheCommittedPath(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(30),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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

func TestReasoningStaysRawAndNeverLeaks(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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
	s := newScriptedSession(t, th, WithWidth(50),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
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

func waitStatusCalls(t *testing.T, calls *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for calls.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("the status function made %d calls, want %d", calls.Load(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStatusTickRedrawsOnlyChangedRows(t *testing.T) {
	th := oledTheme(t)
	var calls atomic.Int32
	var rows atomic.Value
	rows.Store([]string{"projects held: 3"})
	s := newScriptedSession(t, th, WithWidth(100),
		WithStatus(func(ctx context.Context) StatusIn {
			if calls.Add(1) > 2 {
				rows.Store([]string{"projects held: 4"})
			}
			b := statusFixture()
			b.Rows, _ = rows.Load().([]string)
			return b
		}),
		WithStatusTick(10*time.Millisecond),
	)

	go func() { _, _ = s.fe.Input(s.ctx) }()
	s.await(promptMark(th))
	s.await("projects held: 3")
	s.await("projects held: 4")
	time.Sleep(60 * time.Millisecond)
	if got := bytes.Count(s.out.Bytes(), []byte("projects held: 3")); got != 1 {
		t.Fatalf("an unchanged status re-painted the row %d times, want 1:\n%s", got, s.out.String())
	}
	if got := bytes.Count(s.out.Bytes(), []byte("projects held: 4")); got != 1 {
		t.Fatalf("the flipped row painted %d times, want 1:\n%s", got, s.out.String())
	}
}

func TestStatusTickSkipsLiveTurn(t *testing.T) {
	th := oledTheme(t)
	var calls atomic.Int32
	s := newScriptedSession(t, th, WithWidth(100),
		WithStatus(func(ctx context.Context) StatusIn {
			calls.Add(1)
			b := statusFixture()
			b.Rows = []string{"projects held: 3"}
			return b
		}),
		WithStatusTick(10*time.Millisecond),
	)

	started := make(chan string, 1)
	go func() {
		l, _ := s.fe.Input(s.ctx)
		started <- l
	}()
	s.await(promptMark(th))
	waitStatusCalls(t, &calls, 2)
	s.si.feed("go\n")
	if l := <-started; l != "go" {
		t.Fatalf("prompt = %q, want go", l)
	}
	mid := calls.Load()
	s.fe.Notify(core.TextDelta{Text: "hi\n"})
	s.tick()
	s.await("hi")
	time.Sleep(60 * time.Millisecond)
	if got := calls.Load(); got != mid {
		t.Fatalf("the status ticked mid-turn: %d calls, want %d", got, mid)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	go func() { _, _ = s.fe.Input(s.ctx) }()
	waitStatusCalls(t, &calls, mid+1)
}
