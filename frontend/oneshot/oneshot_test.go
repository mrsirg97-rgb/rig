package oneshot_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/frontend/oneshot"
)

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestOneShotFeedsExactlyOnePromptThenEOFs(t *testing.T) {
	o := &oneshot.OneShot{Prompt: "do the thing"}
	p, err := o.Input(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p != "do the thing" {
		t.Fatalf("first input %q", p)
	}

	if _, err := o.Input(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("second input err %v, want io.EOF", err)
	}
}

func TestOneShotErrPromptNamesTheEmptyConstruction(t *testing.T) {
	if err := oneshot.ErrPrompt(""); !errors.Is(err, oneshot.ErrOneShot) {
		t.Fatalf("ErrPrompt(\"\") %v", err)
	}
	if err := oneshot.ErrPrompt("  \n"); !errors.Is(err, oneshot.ErrOneShot) {
		t.Fatalf("ErrPrompt(blank) %v", err)
	}
	if err := oneshot.ErrPrompt("real"); err != nil {
		t.Fatalf("ErrPrompt(real) %v", err)
	}
}

func TestOneShotKeepsStdoutTheAnswerAndStderrTheLiveness(t *testing.T) {
	var out, errB strings.Builder
	o := &oneshot.OneShot{Out: &out, Err: &errB}
	o.Notify(core.ReasoningDelta{Text: "thinking "})
	o.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash"}})
	o.Notify(core.ToolResult{ID: "c1", Content: "out"})
	o.Notify(core.TextDelta{Text: "hello"})
	o.Notify(core.Done{})
	if out.String() != "hello\n" {
		t.Fatalf("stdout is the answer only, got %q", out.String())
	}
	stderr := errB.String()
	for _, want := range []string{"thinking ", "tool bash start", "tool bash end"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr must carry %q, got %q", want, stderr)
		}
	}
}

func TestOneShotHeartbeatsOnStderrWhileAToolRuns(t *testing.T) {
	var out syncBuffer
	var errB syncBuffer
	o := &oneshot.OneShot{Out: &out, Err: &errB, Heartbeat: 5 * time.Millisecond}
	o.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash"}})
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(errB.String(), "heartbeat") {
		if time.Now().After(deadline) {
			t.Fatalf("no heartbeat while the tool ran: %q", errB.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	o.Notify(core.ToolResult{ID: "c1", Content: "out"})
	after := errB.String()
	time.Sleep(50 * time.Millisecond)
	if errB.String() != after {
		t.Fatalf("heartbeats must stop with the tool, got %q", errB.String())
	}
	if out.String() != "" {
		t.Fatalf("a silent tool run must not touch stdout, got %q", out.String())
	}
}

func TestOneShotNotifyRendersAssistantTextAndFaultsLoud(t *testing.T) {
	var sb strings.Builder
	o := &oneshot.OneShot{Out: &sb}
	o.Notify(core.TextDelta{Text: "hel"})
	o.Notify(core.ToolCallEvent{Call: core.ToolCall{Name: "bash"}})
	o.Notify(core.TextDelta{Text: "lo"})
	o.Notify(core.Done{})
	want := "hello\n"
	if sb.String() != want {
		t.Fatalf("rendered %q, want %q", sb.String(), want)
	}
	var fb strings.Builder
	o = &oneshot.OneShot{Out: &fb}
	o.Notify(core.Fault{Err: errors.New("boom")})
	if !strings.Contains(fb.String(), "boom") {
		t.Fatalf("fault voice lost: %q", fb.String())
	}
	if !o.Faulted() {
		t.Fatal("fault did not mark the session")
	}
	o2 := &oneshot.OneShot{Out: &fb}
	if o2.Faulted() {
		t.Fatal("a fresh session reports faulted")
	}
}

func TestOneShotFaultLandsOnStderrWhenPresent(t *testing.T) {
	var out, errB strings.Builder
	o := &oneshot.OneShot{Out: &out, Err: &errB}
	o.Notify(core.Fault{Err: errors.New("boom")})
	if !strings.Contains(errB.String(), "boom") {
		t.Fatalf("a fault must land on stderr: %q", errB.String())
	}
	if out.String() != "" {
		t.Fatalf("a fault must not touch stdout: %q", out.String())
	}
	if !o.Faulted() {
		t.Fatal("the fault must mark the session")
	}
}
