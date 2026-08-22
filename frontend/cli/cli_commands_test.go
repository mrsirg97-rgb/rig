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
	close(r.in)

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

func TestFrontendWithoutCommandsIsUnchanged(t *testing.T) {
	r := build(t, "/models\n")
	close(r.in)
	line, err := r.fe.Input(context.Background())
	if err != nil || line != "/models" {
		t.Fatalf("a frontend without commands must treat /models as a prompt, got %q %v", line, err)
	}
}

func TestUnknownCommandIsLoudNeverAPrompt(t *testing.T) {
	r := buildWithCommands(t, commandsEnv(), "/bogus\n", "hello\n")
	close(r.in)
	line, err := r.fe.Input(context.Background())
	if err != nil || line != "hello" {
		t.Fatalf("the command line is consumed; the next line runs as a prompt, got %q %v", line, err)
	}
	want := "unknown command: bogus (known: approve, compact, effort, models, new, plugins, rem, role, scheduler, sessions, steer, todo)"
	if !strings.Contains(r.out.String(), want) {
		t.Fatalf("the refusal must name the wrong and the right: %q", r.out.String())
	}
}

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

func TestSteerLiveTurn(t *testing.T) {
	r := buildWithCommands(t, commandsEnv(), "one\n")
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	if line, err := r.fe.Input(core.WithInterrupt(ctx1, cancel1)); err != nil || line != "one" {
		t.Fatalf("first input: %q %v", line, err)
	}

	r.in <- "/steer fix it\n"
	waitFor(t, func() bool { return ctx1.Err() != nil }, "the steer must interrupt the live turn")

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

func TestSteerBetweenTurns(t *testing.T) {
	r := buildWithCommands(t, commandsEnv(), "one\n")
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	if line, err := r.fe.Input(core.WithInterrupt(ctx1, cancel1)); err != nil || line != "one" {
		t.Fatalf("first input: %q %v", line, err)
	}
	cancel1()

	r.in <- "/steer fix it\n"

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

func TestSteerEmptyInterrupts(t *testing.T) {

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
	r.in <- "next\n"
	line, err := r.fe.Input(core.WithInterrupt(ctx2, cancel2))
	if err != nil || line != "next" {
		t.Fatalf("after an empty steer the next typed line is the prompt, got %q %v", line, err)
	}
	if !strings.Contains(r.out.String(), "steer: interrupted") {
		t.Fatalf("a broken live turn must report 'steer: interrupted': %q", r.out.String())
	}

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

func TestNewDropsTheQueuedSteer(t *testing.T) {
	env := &command.Env{
		NewSession: func(ctx context.Context) (string, error) { return "s2", nil },
	}
	r := buildWithCommands(t, env, "one\n")

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	if line, err := r.fe.Input(core.WithInterrupt(ctx1, cancel1)); err != nil || line != "one" {
		t.Fatalf("first input: %q %v", line, err)
	}

	r.in <- "steer text\n"
	waitFor(t, func() bool { return ctx1.Err() != nil }, "the queued steer must interrupt the live turn")
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	if line, err := r.fe.Input(core.WithInterrupt(ctx2, cancel2)); err != nil || line != "steer text" {
		t.Fatalf("the queued steer is delivered at the re-entry, got %q %v", line, err)
	}

	r.in <- "/new\n"
	waitFor(t, func() bool { return ctx2.Err() != nil }, "/new must interrupt the live turn")

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

func TestLiveTurnIsStructurallyFalseAtDispatch(t *testing.T) {
	env := commandsEnv()
	env.Compact = func(ctx context.Context) (core.Compacted, bool, error) {
		return core.Compacted{}, false, nil
	}
	r := buildWithCommands(t, env, "one\n", "/compact\n")
	close(r.in)
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	if line, err := r.fe.Input(core.WithInterrupt(ctx1, cancel1)); err != nil || line != "one" {
		t.Fatalf("first input: %q %v", line, err)
	}
	cancel1()

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

func TestBurstInputDeliversEveryLineInOrder(t *testing.T) {
	out := &bytes.Buffer{}
	fe := cli.New(strings.NewReader("/one\n/two\n/three\n"), out,
		cli.WithCommands(command.All(), commandsEnv()))
	time.Sleep(50 * time.Millisecond)
	if _, err := fe.Input(context.Background()); err != io.EOF {
		t.Fatalf("input: want io.EOF after the burst dispatches, got %v", err)
	}
	got := out.String()
	i1, i2, i3 := strings.Index(got, "unknown command: one"), strings.Index(got, "unknown command: two"), strings.Index(got, "unknown command: three")
	if i1 < 0 || i2 < 0 || i3 < 0 || !(i1 < i2 && i2 < i3) {
		t.Fatalf("burst must dispatch every line in order, got:\n%s", got)
	}
}

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
