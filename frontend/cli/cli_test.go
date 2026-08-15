package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/frontend/cli"
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

func TestNotifyRendersTheTurnStream(t *testing.T) {
	r := build(t)
	r.fe.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c1", Name: "bash"}})
	r.fe.Notify(core.TextDelta{Text: "hello "})
	r.fe.Notify(core.TextDelta{Text: "world"})
	r.fe.Notify(core.Done{StopReason: "stop"})
	if got := r.out.String(); got != "\n[call] bash\nhello world\n" {
		t.Fatalf("rendered output = %q", got)
	}
}

func TestNotifyRendersFaults(t *testing.T) {
	r := build(t)
	r.fe.Notify(core.Fault{Err: errors.New("boom")})
	if got := r.out.String(); got != "\n[fault] boom\n" {
		t.Fatalf("fault rendering = %q", got)
	}
}
