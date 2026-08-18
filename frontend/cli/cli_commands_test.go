package cli_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	rigpkg "github.com/mrsirg97-rgb/rig"
	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/frontend/cli"
	"github.com/mrsirg97-rgb/rig/loop"
	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/policy"
)

type cmdRig struct {
	fe  core.Frontend
	out *bytes.Buffer
	in  chan string
}

// buildWithCommands wires the CLI over the standard set with the given
// env (the dispatcher fills the frontend-owned seam, decision 2).
func buildWithCommands(t *testing.T, env *command.Env, lines ...string) *cmdRig {
	t.Helper()
	in := make(chan string, len(lines)+2)
	for _, l := range lines {
		in <- l
	}
	out := &bytes.Buffer{}
	fe := cli.New(lineReader{lines: in}, out, cli.WithCommands(command.All(), env))
	return &cmdRig{fe: fe, out: out, in: in}
}

// defaultTable is the 0.2.0 rows (SPEC_CONFIG 4: the table leaves code;
// the test harnesses construct the same rows).
func defaultTable() models.Table {
	tbl, err := models.New(
		models.Model{ID: "local", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleInteractive},
		models.Model{ID: "qwen3.8-workers", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker},
	)
	if err != nil {
		panic("cli: defaultTable: " + err.Error())
	}
	return tbl
}

func commandsEnv() *command.Env {
	return &command.Env{
		Models:      func() models.Table { return defaultTable() },
		ActiveModel: func() string { return "local" },
	}
}

// cliProvider is a scripted core.Provider with request capture, for the
// loop-level dispatch case.
type cliProvider struct {
	mu     sync.Mutex
	reqs   []core.Request
	answer string
}

func (p *cliProvider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()
	out := make(chan core.Event, 2)
	go func() {
		defer close(out)
		out <- core.TextDelta{Text: p.answer}
		out <- core.Done{}
	}()
	return out, nil
}

func (p *cliProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.reqs)
}

func (p *cliProvider) requests() []core.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]core.Request(nil), p.reqs...)
}

// TestDispatchByPrefixLoopNeverSeesCommand (SPEC_COMMANDS, named):
// loop.Run over the CLI with commands and a scripted provider — the
// command line is consumed in the dispatch; the provider is called
// exactly once with exactly one user message; the command's fake
// recorded the call; no command line anywhere in the transcript or the
// request.
func TestDispatchByPrefixLoopNeverSeesCommand(t *testing.T) {
	prov := &cliProvider{answer: "hi"}
	modelsCalls := 0
	env := &command.Env{
		Models: func() models.Table {
			modelsCalls++
			return defaultTable()
		},
		ActiveModel: func() string { return "local" },
	}
	r := buildWithCommands(t, env, "/models\n", "hello\n")
	close(r.in) // EOF after the two lines

	k := rigpkg.New(
		rigpkg.WithProvider(prov),
		rigpkg.WithFrontend(r.fe),
		rigpkg.WithPolicy(policy.Passthrough("")),
		rigpkg.WithCommands(command.All()...),
	)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("loop: %v", err)
	}

	if prov.count() != 1 {
		t.Fatalf("the provider must be called exactly once (the command is not a prompt), got %d", prov.count())
	}
	req := prov.requests()[0]
	if len(req.Messages) != 1 || req.Messages[0].Role != core.RoleUser || req.Messages[0].Content != "hello" {
		t.Fatalf("the request must carry exactly one user message, hello: %+v", req.Messages)
	}
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "/models") {
			t.Fatalf("no command line may reach the request: %+v", m)
		}
	}
	if modelsCalls != 1 {
		t.Fatalf("the models command's fake must have recorded the call, got %d", modelsCalls)
	}
	var userMsgs []string
	for _, m := range k.Session.Messages {
		if m.Role == core.RoleUser {
			userMsgs = append(userMsgs, m.Content)
		}
	}
	if len(userMsgs) != 1 || userMsgs[0] != "hello" {
		t.Fatalf("the transcript must carry the prompt only (no /models): %v", userMsgs)
	}
	if !strings.Contains(r.out.String(), "window 65536") {
		t.Fatalf("the models table line must have printed: %q", r.out.String())
	}
}

// TestFrontendWithoutCommandsIsUnchanged (SPEC_COMMANDS 10): the CLI
// without WithCommands — its / lines are prompts, as today; nothing is
// hijacked from it.
func TestFrontendWithoutCommandsIsUnchanged(t *testing.T) {
	r := build(t, "/models\n")
	close(r.in)
	line, err := r.fe.Input(context.Background())
	if err != nil || line != "/models" {
		t.Fatalf("a frontend without commands must treat /models as a prompt, got %q %v", line, err)
	}
}

// TestUnknownCommandIsLoudNeverAPrompt (SPEC_COMMANDS, named): /bogus
// prints the refusal naming the known list, the line is consumed (the
// next line runs as a prompt), and the transcript is untouched (the
// prompt is the typed line, not the command line).
func TestUnknownCommandIsLoudNeverAPrompt(t *testing.T) {
	r := buildWithCommands(t, commandsEnv(), "/bogus\n", "hello\n")
	close(r.in)
	line, err := r.fe.Input(context.Background())
	if err != nil || line != "hello" {
		t.Fatalf("the command line is consumed; the next line runs as a prompt, got %q %v", line, err)
	}
	want := "unknown command: bogus (known: compact, models, new, scheduler, sessions, steer, todo)"
	if !strings.Contains(r.out.String(), want) {
		t.Fatalf("the refusal must name the wrong and the right: %q", r.out.String())
	}
}

// TestEscapedPromptIsAPrompt (SPEC_COMMANDS 1): the // escape consumes
// one slash — //home/ng reaches the model as /home/ng; ///x as //x.
func TestEscapedPromptIsAPrompt(t *testing.T) {
	r := buildWithCommands(t, commandsEnv(), "//home/ng\n")
	close(r.in)
	line, err := r.fe.Input(context.Background())
	if err != nil || line != "/home/ng" {
		t.Fatalf("the escape must consume one slash, got %q %v", line, err)
	}

	r = buildWithCommands(t, commandsEnv(), "///x\n")
	close(r.in)
	line, err = r.fe.Input(context.Background())
	if err != nil || line != "//x" {
		t.Fatalf("the escape must consume exactly one slash, got %q %v", line, err)
	}
}

// TestSteerLiveTurn (SPEC_COMMANDS, named): a /steer line typed during
// a live turn — the turn breaks (the interrupt lands), the command
// reports it, and the queued text drives the next model call as the
// user message.
func TestSteerLiveTurn(t *testing.T) {
	r := buildWithCommands(t, commandsEnv(), "one\n")
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	if line, err := r.fe.Input(core.WithInterrupt(ctx1, cancel1)); err != nil || line != "one" {
		t.Fatalf("first input: %q %v", line, err)
	}

	// the turn is live: the steer line queues and interrupts.
	r.in <- "/steer fix it\n"
	waitFor(t, func() bool { return ctx1.Err() != nil }, "the steer must interrupt the live turn")

	// the re-entry: the command reports the interrupt, and the queued
	// text is the next user message.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	line, err := r.fe.Input(core.WithInterrupt(ctx2, cancel2))
	if err != nil || line != "fix it" {
		t.Fatalf("the queued text must drive the next model call as the user message, got %q %v", line, err)
	}
	if !strings.Contains(r.out.String(), "steer: queued fix it · turn interrupted") {
		t.Fatalf("the command must report the interrupt: %q", r.out.String())
	}
}

// TestSteerBetweenTurns (SPEC_COMMANDS, named): at a quiet prompt the
// steer queues only — no interrupt, no report of one — and the text is
// the next prompt, exactly as a typed line would be.
func TestSteerBetweenTurns(t *testing.T) {
	r := buildWithCommands(t, commandsEnv(), "one\n")
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	if line, err := r.fe.Input(core.WithInterrupt(ctx1, cancel1)); err != nil || line != "one" {
		t.Fatalf("first input: %q %v", line, err)
	}
	cancel1() // the turn ends at the boundary

	r.in <- "/steer fix it\n" // between turns: queued, no interrupt

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	line, err := r.fe.Input(core.WithInterrupt(ctx2, cancel2))
	if err != nil || line != "fix it" {
		t.Fatalf("the steer text must be the next prompt, got %q %v", line, err)
	}
	if !strings.Contains(r.out.String(), "steer: queued fix it") {
		t.Fatalf("the queued line must print: %q", r.out.String())
	}
	if strings.Contains(r.out.String(), "turn interrupted") {
		t.Fatalf("between turns there is no interrupt to report: %q", r.out.String())
	}
}

// TestSteerEmptyInterrupts (SPEC_COMMANDS, named): an empty steer
// interrupts only — the live turn: the turn breaks, the slot keeps no
// steer text, 'steer: interrupted'; a quiet prompt after a clean
// boundary: 'steer: no live turn'.
func TestSteerEmptyInterrupts(t *testing.T) {
	// live: the empty steer breaks the turn and queues no text.
	r := buildWithCommands(t, commandsEnv(), "one\n")
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	if line, err := r.fe.Input(core.WithInterrupt(ctx1, cancel1)); err != nil || line != "one" {
		t.Fatalf("first input: %q %v", line, err)
	}
	r.in <- "/steer\n"
	waitFor(t, func() bool { return ctx1.Err() != nil }, "the empty steer must interrupt the live turn")

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	r.in <- "next\n" // the slot kept no steer text: the next line is the prompt
	line, err := r.fe.Input(core.WithInterrupt(ctx2, cancel2))
	if err != nil || line != "next" {
		t.Fatalf("after an empty steer the next typed line is the prompt, got %q %v", line, err)
	}
	if !strings.Contains(r.out.String(), "steer: interrupted") {
		t.Fatalf("a broken live turn must report 'steer: interrupted': %q", r.out.String())
	}

	// a clean boundary: nothing live, nothing queued.
	r = buildWithCommands(t, commandsEnv())
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	r.in <- "/steer\n"
	r.in <- "after\n"
	line, err = r.fe.Input(core.WithInterrupt(ctx3, cancel3))
	if err != nil || line != "after" {
		t.Fatalf("after a quiet steer the next typed line is the prompt, got %q %v", line, err)
	}
	if !strings.Contains(r.out.String(), "steer: no live turn") {
		t.Fatalf("a clean boundary must report 'steer: no live turn': %q", r.out.String())
	}
}

// TestNewDropsTheQueuedSteer (SPEC_COMMANDS 4, named): a steer text in
// the slot, then /new — the slot is empty and the next prompt is the
// typed line, not the steer text; the fresh transcript has no steer
// text. The flow is scheduled, not slept: each queued line is observed
// through its interrupt (the slot write happens-before the cancel the
// test waits on), so the re-entry sees exactly what the test scheduled.
func TestNewDropsTheQueuedSteer(t *testing.T) {
	env := &command.Env{
		NewSession: func(ctx context.Context) (string, error) { return "s2", nil },
	}
	r := buildWithCommands(t, env, "one\n")

	// turn one, live.
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	if line, err := r.fe.Input(core.WithInterrupt(ctx1, cancel1)); err != nil || line != "one" {
		t.Fatalf("first input: %q %v", line, err)
	}

	// a steer queued during the live turn: it queues, interrupts, and is
	// delivered at the re-entry as the old session's prompt (7's flow).
	r.in <- "steer text\n"
	waitFor(t, func() bool { return ctx1.Err() != nil }, "the queued steer must interrupt the live turn")
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	if line, err := r.fe.Input(core.WithInterrupt(ctx2, cancel2)); err != nil || line != "steer text" {
		t.Fatalf("the queued steer is delivered at the re-entry, got %q %v", line, err)
	}

	// turn two, live: /new queues and interrupts; the re-entry dispatches
	// it (the slot write happened before the cancel the test waited on).
	r.in <- "/new\n"
	waitFor(t, func() bool { return ctx2.Err() != nil }, "/new must interrupt the live turn")

	// the re-entry: the dispatch prints the fresh id, the slot is clear,
	// and the next prompt is the typed line (direct, the dispatch is in
	// flight — not the steer text).
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	r.in <- "typed line\n"
	line, err := r.fe.Input(core.WithInterrupt(ctx3, cancel3))
	if err != nil || line != "typed line" {
		t.Fatalf("the next prompt is the typed line, not the steer text, got %q %v", line, err)
	}
	if !strings.Contains(r.out.String(), "new: session s2") {
		t.Fatalf("the new line must print the fresh id: %q", r.out.String())
	}
	if i := strings.Index(r.out.String(), "new: session s2"); i < 0 || strings.Contains(r.out.String()[i:], "steer text") {
		t.Fatalf("the steer text must not be delivered into the new session: %q", r.out.String())
	}
}

// TestLiveTurnIsStructurallyFalseAtDispatch (SPEC_COMMANDS 2, named):
// at the CLI's dispatch the loop is in awaiting_input — the previous
// turn's ctx is dead, so compact / new / sessions resume do not refuse
// on liveness (the TUI's mid-turn keypress is the refusal case).
func TestLiveTurnIsStructurallyFalseAtDispatch(t *testing.T) {
	env := commandsEnv()
	env.Compact = func(ctx context.Context) (core.Compacted, bool, error) {
		return core.Compacted{}, false, nil // nothing to drop (an empty session)
	}
	r := buildWithCommands(t, env, "one\n", "/compact\n")
	close(r.in)
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	if line, err := r.fe.Input(core.WithInterrupt(ctx1, cancel1)); err != nil || line != "one" {
		t.Fatalf("first input: %q %v", line, err)
	}
	cancel1() // the turn ends: the loop is back in awaiting_input
	// the /compact line dispatches without a live-turn refusal: the
	// compact command's own voice (nothing to drop — an empty session)
	// is what prints, not the refusal.
	if _, err := r.fe.Input(context.Background()); err != nil && !errorsIsEOF(err) {
		t.Fatalf("input: %v", err)
	}
	if strings.Contains(r.out.String(), "a turn is live") {
		t.Fatalf("at dispatch the CLI must not refuse on liveness: %q", r.out.String())
	}
	if !strings.Contains(r.out.String(), "compact: nothing to drop") {
		t.Fatalf("the compact command must run at the quiet prompt: %q", r.out.String())
	}
}

func errorsIsEOF(err error) bool { return err == io.EOF }

// A burst of lines that arrives while Input is not parked (a pipe, a
// paste, the window between turns) must deliver every line in order —
// the steering slot's latest-wins is live-turn semantics only. Red
// against the old readLoop fallback, which dropped all but the last.
func TestBurstInputDeliversEveryLineInOrder(t *testing.T) {
	out := &bytes.Buffer{}
	fe := cli.New(strings.NewReader("/one\n/two\n/three\n"), out,
		cli.WithCommands(command.All(), commandsEnv()))
	time.Sleep(50 * time.Millisecond) // let the reader race ahead of Input, the bug's shape
	if _, err := fe.Input(context.Background()); err != io.EOF {
		t.Fatalf("input: want io.EOF after the burst dispatches, got %v", err)
	}
	got := out.String()
	i1, i2, i3 := strings.Index(got, "unknown command: one"), strings.Index(got, "unknown command: two"), strings.Index(got, "unknown command: three")
	if i1 < 0 || i2 < 0 || i3 < 0 || !(i1 < i2 && i2 < i3) {
		t.Fatalf("burst must dispatch every line in order, got:\n%s", got)
	}
}

// The same burst shape for prompts: two pasted lines are two prompts,
// in order, not one surviving steer.
func TestPastedPromptsAreDeliveredInOrder(t *testing.T) {
	fe := cli.New(strings.NewReader("one\ntwo\n"), &bytes.Buffer{})
	time.Sleep(50 * time.Millisecond)
	ctx := context.Background()
	a, err := fe.Input(ctx)
	if err != nil || a != "one" {
		t.Fatalf("first pasted prompt = %q, %v; want \"one\"", a, err)
	}
	b, err := fe.Input(ctx)
	if err != nil || b != "two" {
		t.Fatalf("second pasted prompt = %q, %v; want \"two\"", b, err)
	}
}
