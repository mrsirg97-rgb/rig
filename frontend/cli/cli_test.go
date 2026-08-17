package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/frontend/cli"
)

// lineReader serves whole lines from a channel; close = EOF.
type lineReader struct{ lines chan string }

func (r lineReader) Read(p []byte) (int, error) {
	v, ok := <-r.lines
	if !ok {
		return 0, io.EOF
	}
	return copy(p, []byte(v)), nil
}

type rig struct {
	fe  core.Frontend
	out *bytes.Buffer
	in  chan string
}

func build(t *testing.T, lines ...string) *rig {
	t.Helper()
	in := make(chan string, len(lines)+2)
	for _, l := range lines {
		in <- l
	}
	out := &bytes.Buffer{}
	return &rig{
		fe:  cli.New(lineReader{lines: in}, out),
		out: out,
		in:  in,
	}
}

func TestInputServesLinesAndSkipsBlanks(t *testing.T) {
	r := build(t, "\n", "   \n", "hello\n")
	got, err := r.fe.Input(context.Background())
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	if got != "hello" {
		t.Fatalf("input = %q, want hello (blanks skipped, newline stripped)", got)
	}
}

func TestInputEOF(t *testing.T) {
	r := build(t)
	close(r.in)
	_, err := r.fe.Input(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("closed stdin must read as io.EOF, got %v", err)
	}
}

func TestInputServesAFinalLineWithoutNewline(t *testing.T) {
	r := build(t)
	r.in <- "tail"
	close(r.in)
	got, err := r.fe.Input(context.Background())
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	if got != "tail" {
		t.Fatalf("final line = %q, want tail", got)
	}
	if _, err := r.fe.Input(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("the next pull must then read EOF, got %v", err)
	}
}

func TestInputCancellationBeforeRead(t *testing.T) {
	r := build(t, "never served\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.fe.Input(ctx); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled input must surface the context error, got %v", err)
	}
}

func TestInputServesTheSteeringSlotBeforeBlocking(t *testing.T) {
	// a queued message is delivered on the next Input without touching
	// stdin: the slot is checked before the reader path.
	r := build(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// turn one, live:
	r.in <- "one\n"
	if line, err := r.fe.Input(core.WithInterrupt(ctx, cancel)); err != nil || line != "one" {
		t.Fatalf("first input: %q %v", line, err)
	}
	// steer during the live turn: the line lands in the slot and the
	// handle fires.
	r.in <- "steer\n"
	waitFor(t, func() bool { return ctx.Err() != nil }, "the live turn must be interrupted")
	// the re-entry delivers the queued line without a stdin read:
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	line, err := r.fe.Input(core.WithInterrupt(ctx2, cancel2))
	if err != nil || line != "steer" {
		t.Fatalf("the re-entry must deliver the queued line, got %q %v", line, err)
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", what)
}

func TestSteeringBetweenTurnsQueuesWithoutCancellingTheNextTurn(t *testing.T) {
	r := build(t, "one\n")

	// turn one, live: the loop handed its cancel with the Input ctx.
	ctx1, cancel1 := context.WithCancel(context.Background())
	if line, err := r.fe.Input(core.WithInterrupt(ctx1, cancel1)); err != nil || line != "one" {
		t.Fatalf("first input: %q %v", line, err)
	}
	// turn one ends: the loop cancels the turn's context at the exit.
	cancel1()

	// a line that lands between turns queues: the held handle is the dead
	// turn's (a no-op cancel), so the next turn's context is untouched.
	r.in <- "between\n"

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	line, err := r.fe.Input(core.WithInterrupt(ctx2, cancel2))
	if err != nil || line != "between" {
		t.Fatalf("the between-turns line must be delivered on the next Input, got %q %v", line, err)
	}
}

func TestSlotIsSingleLatestWins(t *testing.T) {
	r := build(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.in <- "first\n"
	if line, err := r.fe.Input(core.WithInterrupt(ctx, cancel)); err != nil || line != "first" {
		t.Fatalf("first input: %q %v", line, err)
	}
	// live turn: two lines arrive; the slot holds at most one (the latest),
	// anything the reader had in flight lands direct. The latest intent is
	// never lost and an older line never lands after it.
	r.in <- "a\n"
	r.in <- "b\n"
	waitFor(t, func() bool { return ctx.Err() != nil }, "the first steering line must interrupt the turn")
	close(r.in) // the reader drains the rest, then EOFs: no pull may block forever

	var delivered []string
	for i := 0; i < 2; i++ {
		ctxN, cancelN := context.WithCancel(context.Background())
		line, err := r.fe.Input(core.WithInterrupt(ctxN, cancelN))
		cancelN()
		if err != nil {
			break
		}
		delivered = append(delivered, line)
	}
	has := func(s string) bool {
		for _, d := range delivered {
			if d == s {
				return true
			}
		}
		return false
	}
	if !has("b") {
		t.Fatalf("the latest line must not be lost: delivered %v", delivered)
	}
	if has("a") && !(delivered[0] == "a" && delivered[1] == "b") {
		t.Fatalf("an older line must not land after the latest: %v", delivered)
	}
}

// --- the rendering -------------------------------------------------------

// The bracket replaced the [call] line (SPEC_HARDENING decision 1): the
// model's intent (ToolCallEvent) and its execution (the bracket) are
// distinct, and the CLI renders the execution rows from events alone.
func TestNotifyRendersTheTurnStream(t *testing.T) {
	r := build(t)
	r.fe.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c1", Name: "bash"}})
	r.fe.Notify(core.TextDelta{Text: "hello "})
	r.fe.Notify(core.TextDelta{Text: "world"})
	r.fe.Notify(core.Done{StopReason: "stop"})
	if got := r.out.String(); got != "hello world\n" {
		t.Fatalf("rendered output = %q", got)
	}
}

func TestNotifyRendersTheExecutionBracket(t *testing.T) {
	r := build(t)
	r.fe.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash"}})
	r.fe.Notify(core.ToolResult{ID: "c1", Content: "out", Duration: 5 * time.Millisecond})
	if got := r.out.String(); got != "\n● bash\nbash ✓ 5ms\n" {
		t.Fatalf("bracket rendering = %q", got)
	}
}

func TestNotifyRendersFailedExecutions(t *testing.T) {
	r := build(t)
	r.fe.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "edit"}})
	r.fe.Notify(core.ToolResult{ID: "c1", Content: "bound exhausted", Err: errors.New("bound exhausted"), Duration: time.Millisecond})
	if got := r.out.String(); got != "\n● edit\nedit ✕ 1ms\n" {
		t.Fatalf("failure rendering = %q", got)
	}
}

func TestNotifyRendersReasoningVerbatim(t *testing.T) {
	r := build(t)
	r.fe.Notify(core.ReasoningDelta{Text: "thinking "})
	r.fe.Notify(core.TextDelta{Text: "answer"})
	if got := r.out.String(); got != "thinking answer" {
		t.Fatalf("reasoning must render verbatim: %q", got)
	}
}

// One usage line at the explicit turn boundary; totals accumulate across
// the turn's model calls and reset per turn.
func TestTurnEndPrintsTheUsageLine(t *testing.T) {
	r := build(t)
	r.fe.Notify(core.Done{StopReason: "stop", Usage: core.Usage{Prompt: 922, Completion: 10, CacheRead: 918}})
	r.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	// hit = CacheRead / Prompt: 918/922 = 99% (the wire reports cached
	// tokens as a subset of prompt)
	if !strings.HasSuffix(r.out.String(), "\n↑922 ↓10 · cache 918 99%\n") {
		t.Fatalf("usage line = %q", r.out.String())
	}
}

func TestUsageTotalsAccumulateAcrossTheTurn(t *testing.T) {
	r := build(t)
	// two model calls in the turn: the Frontend sums what it observes
	r.fe.Notify(core.Done{Usage: core.Usage{Prompt: 5, Completion: 2, CacheRead: 4}})
	r.fe.Notify(core.Done{Usage: core.Usage{Prompt: 5, Completion: 3}})
	r.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	// the next turn starts fresh
	r.fe.Notify(core.Done{Usage: core.Usage{Prompt: 7, Completion: 1}})
	r.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	out := r.out.String()
	if !strings.Contains(out, "↑10 ↓5 · cache 4 40%") {
		t.Fatalf("the turn totals must accumulate across model calls: %q", out)
	}
	if !strings.Contains(out, "↑7 ↓1 · cache 0 0%") {
		t.Fatalf("the next turn must start with fresh totals: %q", out)
	}
	if strings.Count(out, "cache ") != 2 {
		t.Fatalf("exactly one usage line per turn: %q", out)
	}
	if strings.Index(out, "↑10") > strings.Index(out, "↑7") {
		t.Fatalf("the turns must render in order: %q", out)
	}
}

func TestNotifyRendersFaults(t *testing.T) {
	r := build(t)
	r.fe.Notify(core.Fault{Err: errors.New("boom")})
	if got := r.out.String(); got != "\n[fault] boom\n" {
		t.Fatalf("fault rendering = %q", got)
	}
}

// decision 8: an event the CLI does not name is noise — no output, no
// panic, ordering intact.
func TestNotifyIgnoresUnknownEvents(t *testing.T) {
	r := build(t)
	r.fe.Notify(core.TestEvent{Name: "x"})
	r.fe.Notify(core.ReasoningDelta{Text: "after"})
	if got := r.out.String(); got != "after" {
		t.Fatalf("unknown events must not render: %q", got)
	}
}
