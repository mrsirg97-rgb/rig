package oneshot_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/frontend/oneshot"
)

type wire struct {
	mu    sync.Mutex
	beats int
}

func (w *wire) Id() int64 { return 7 }

func (w *wire) Send(ctx context.Context, callback func(error), messages ...broadcast.Message) {
	w.mu.Lock()
	for _, m := range messages {
		if m.Origin() == 7 && m.Ok() && m.Event() == nil {
			w.beats++
		}
	}
	w.mu.Unlock()
	callback(nil)
}

func (w *wire) Recv(context.Context, func(error, ...broadcast.Message)) {}

func (w *wire) Close() {}

func (w *wire) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.beats
}

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

func TestOneShotHeartbeatsOnTheFleetWhileAToolRuns(t *testing.T) {
	var out syncBuffer
	var errB syncBuffer
	fleet := &wire{}
	o := &oneshot.OneShot{Out: &out, Err: &errB, Heartbeat: 5 * time.Millisecond, Fleet: fleet}
	o.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash"}})
	deadline := time.Now().Add(2 * time.Second)
	for fleet.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no heartbeat while the tool ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
	o.Notify(core.ToolResult{ID: "c1", Content: "out"})
	after := fleet.count()
	time.Sleep(50 * time.Millisecond)
	if fleet.count() != after {
		t.Fatal("heartbeats must stop with the tool")
	}
	if strings.Contains(errB.String(), "heartbeat") {
		t.Fatalf("the heartbeat is a message, never a stderr line: %q", errB.String())
	}
	if out.String() != "" {
		t.Fatalf("a silent tool run must not touch stdout, got %q", out.String())
	}
}

func TestOneShotWithoutAFleetNeverHeartbeats(t *testing.T) {
	var out, errB syncBuffer
	o := &oneshot.OneShot{Out: &out, Err: &errB, Heartbeat: time.Millisecond}
	o.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash"}})
	time.Sleep(20 * time.Millisecond)
	o.Notify(core.ToolResult{ID: "c1", Content: "out"})
	if strings.Contains(errB.String(), "heartbeat") {
		t.Fatalf("a worker nobody listens to says nothing: %q", errB.String())
	}
}

func TestOneShotBatchHeartbeatOutlivesTheFirstResult(t *testing.T) {
	var out syncBuffer
	var errB syncBuffer
	fleet := &wire{}
	o := &oneshot.OneShot{Out: &out, Err: &errB, Heartbeat: 5 * time.Millisecond, Fleet: fleet}
	o.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash"}})
	o.Notify(core.ToolStart{Call: core.ToolCall{ID: "c2", Name: "python"}})
	deadline := time.Now().Add(2 * time.Second)
	for fleet.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no heartbeat while the batch ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
	o.Notify(core.ToolResult{ID: "c1", Content: "bash out"})
	before := fleet.count()
	deadline = time.Now().Add(2 * time.Second)
	for fleet.count() == before {
		if time.Now().After(deadline) {
			t.Fatal("the batch heartbeat must outlive the first result")
		}
		time.Sleep(5 * time.Millisecond)
	}
	o.Notify(core.ToolResult{ID: "c2", Content: "py out"})
	after := errB.String()
	if !strings.Contains(after, "tool bash end") || !strings.Contains(after, "tool python end") {
		t.Fatalf("each result must name its own tool: %q", after)
	}
	if strings.Index(after, "tool bash end") > strings.Index(after, "tool python end") {
		t.Fatalf("the end lines must land in call order: %q", after)
	}
	beats := fleet.count()
	time.Sleep(50 * time.Millisecond)
	if fleet.count() != beats {
		t.Fatal("the batch heartbeat must stop with the last result")
	}
	if out.String() != "" {
		t.Fatalf("a silent batch must not touch stdout: %q", out.String())
	}
}
