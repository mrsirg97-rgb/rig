package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/looper"
	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/loop"
)

// --- DI-seam fakes ---------------------------------------------------------
// scriptedProvider replays canned event streams, one per model call.
type scriptedTurn struct {
	events  []core.Event
	err     error // transport error at call time
	holdCtx bool  // block until ctx cancels, then close without Done (teardown)
	bare    bool  // close without Done or Fault (provider bug)
}

type scriptedProvider struct {
	turns    []scriptedTurn
	calls    int
	toolReqs int // model calls that carried tools
}

func (p *scriptedProvider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	if p.calls >= len(p.turns) {
		panic("scriptedProvider: more model calls than scripts")
	}
	if len(req.Tools) > 0 {
		p.toolReqs++
	}
	turn := p.turns[p.calls]
	p.calls++
	if turn.err != nil {
		return nil, turn.err
	}
	if turn.holdCtx {
		<-ctx.Done() // transport torn down by the cancellation
		ch := make(chan core.Event)
		close(ch)
		return ch, nil
	}
	out := make(chan core.Event)
	if !turn.bare {
		go func() {
			for _, ev := range turn.events {
				out <- ev
			}
			close(out)
		}()
	} else {
		close(out)
	}
	return out, nil
}

// recorderFrontend serves scripted inputs (close = EOF) and records every
// Notify.
type recorderFrontend struct {
	inputs chan string
	events []core.Event
}

func (f *recorderFrontend) Input(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case v, ok := <-f.inputs:
		if !ok {
			return "", io.EOF
		}
		return v, nil
	}
}

func (f *recorderFrontend) Notify(ev core.Event) {
	f.events = append(f.events, ev)
}

// transcriptPolicy is the minimal seam fake for Assemble in these tests.
type transcriptPolicy struct{ system string }

func (p transcriptPolicy) Assemble(ctx context.Context, s *core.Session) ([]core.Message, error) {
	msgs := []core.Message{}
	if p.system != "" {
		msgs = append(msgs, core.Message{Role: core.RoleSystem, Content: p.system})
	}
	return append(msgs, s.Messages...), nil
}

// scriptedTool answers with a canned result, failing for its first fail
// calls to exercise fed-back paths.
type scriptedTool struct {
	name   string
	fail   int
	result string
	calls  int
	cancel context.CancelFunc // deterministic cancellation point, tests only
}

func (t *scriptedTool) Name() string { return t.name }

func (t *scriptedTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (t *scriptedTool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	t.calls++
	if t.cancel != nil {
		t.cancel()
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if t.calls <= t.fail {
		return "synthetic failure", errors.New("synthetic failure")
	}
	return t.result, nil
}

func textEv(s string) core.Event        { return core.TextDelta{Text: s} }
func callEv(c core.ToolCall) core.Event { return core.ToolCallEvent{Call: c} }
func faultEv(err error) core.Event      { return core.Fault{Err: err} }
func doneEv() core.Event                { return core.Done{StopReason: "stop"} }

func wantTranscript(t *testing.T, s *core.Session, want ...core.Message) {
	t.Helper()
	got, wantStr := dump(s), dumpList(want)
	if got != wantStr {
		t.Fatalf("transcript diverged:\n got: %s\nwant: %s", got, wantStr)
	}
}

func dump(s *core.Session) string {
	var b strings.Builder
	for _, m := range s.Messages {
		switch m.Role {
		case core.RoleAssistant:
			b.WriteString("assistant(")
			if len(m.ToolCalls) > 0 {
				for _, c := range m.ToolCalls {
					b.WriteString("call:" + c.Name)
				}
			}
			b.WriteString("|" + m.Content + ") ")
		case core.RoleTool:
			b.WriteString("tool(" + m.ToolID + "|" + m.Content + ") ")
		default:
			b.WriteString(string(m.Role) + "(" + m.Content + ") ")
		}
	}
	return b.String()
}

func dumpList(ms []core.Message) string {
	var b strings.Builder
	for _, m := range ms {
		switch m.Role {
		case core.RoleAssistant:
			b.WriteString("assistant(")
			if len(m.ToolCalls) > 0 {
				for _, c := range m.ToolCalls {
					b.WriteString("call:" + c.Name)
				}
			}
			b.WriteString("|" + m.Content + ") ")
		case core.RoleTool:
			b.WriteString("tool(" + m.ToolID + "|" + m.Content + ") ")
		default:
			b.WriteString(string(m.Role) + "(" + m.Content + ") ")
		}
	}
	return b.String()
}

// --- the named cases -------------------------------------------------------

func TestTextOnlyTurnOrdering(t *testing.T) {
	p := &scriptedProvider{turns: []scriptedTurn{{
		events: []core.Event{textEv("hello"), textEv(" world"), doneEv()},
	}}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := looper.New(
		looper.WithProvider(p),
		looper.WithFrontend(f),
		looper.WithPolicy(transcriptPolicy{system: "be terse"}),
	)
	k.Session = session

	f.inputs <- "hi"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}

	want := []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleAssistant, Content: "hello world"},
	}
	wantTranscript(t, session, want...)

	// every streamed event must reach the frontend, in order.
	var kinds []string
	for _, ev := range f.events {
		switch ev.(type) {
		case core.TextDelta:
			kinds = append(kinds, "delta")
		case core.Done:
			kinds = append(kinds, "done")
		}
	}
	if strings.Join(kinds, ",") != "delta,delta,done" {
		t.Fatalf("notify order = %v, want delta,delta,done", kinds)
	}
}

func TestToolRoundTripOrdering(t *testing.T) {
	bash := &scriptedTool{name: "bash", result: "42"}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{
			callEv(core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"echo 42"}`)}),
			doneEv(),
		}},
		{events: []core.Event{textEv("the answer is "), textEv("42"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := looper.New(
		looper.WithProvider(p),
		looper.WithFrontend(f),
		looper.WithPolicy(transcriptPolicy{}),
		looper.WithTools(bash),
	)
	k.Session = session

	f.inputs <- "what is the answer?"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}

	if bash.calls != 1 {
		t.Fatalf("tool executed %d times, want exactly 1", bash.calls)
	}
	want := []core.Message{
		{Role: core.RoleUser, Content: "what is the answer?"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash"}}},
		{Role: core.RoleTool, ToolID: "c1", Content: "42"},
		{Role: core.RoleAssistant, Content: "the answer is 42"},
	}
	wantTranscript(t, session, want...)

	// both model calls must have carried the tool spec.
	if p.toolReqs != 2 {
		t.Fatalf("tool-carrying model calls = %d, want 2", p.toolReqs)
	}
}

func TestFaultMidStreamPreservesSession(t *testing.T) {
	boom := errors.New("mid-stream fault")
	p := &scriptedProvider{turns: []scriptedTurn{{
		events: []core.Event{textEv("partial "), faultEv(boom)},
	}, {
		events: []core.Event{textEv("recovered"), doneEv()},
	}}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := looper.New(
		looper.WithProvider(p),
		looper.WithFrontend(f),
		looper.WithPolicy(transcriptPolicy{}),
	)
	k.Session = session

	f.inputs <- "first"
	f.inputs <- "second"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}

	// the partial assistant text never enters the transcript; the user
	// message, being complete, survives.
	want := []core.Message{
		{Role: core.RoleUser, Content: "first"},
		{Role: core.RoleUser, Content: "second"},
		{Role: core.RoleAssistant, Content: "recovered"},
	}
	wantTranscript(t, session, want...)

	surfaced := false
	for _, ev := range f.events {
		if ft, ok := ev.(core.Fault); ok && errors.Is(ft.Err, boom) {
			surfaced = true
		}
	}
	if !surfaced {
		t.Fatal("fault never surfaced through Notify")
	}
}

func TestTransportErrorSurfacesAndContinues(t *testing.T) {
	boom := errors.New("transport down")
	p := &scriptedProvider{turns: []scriptedTurn{
		{err: boom},
		{events: []core.Event{textEv("up again"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := looper.New(
		looper.WithProvider(p),
		looper.WithFrontend(f),
		looper.WithPolicy(transcriptPolicy{}),
	)
	k.Session = session

	f.inputs <- "one"
	f.inputs <- "two"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}

	var surfaced bool
	for _, ev := range f.events {
		if ft, ok := ev.(core.Fault); ok && errors.Is(ft.Err, boom) {
			surfaced = true
		}
	}
	if !surfaced {
		t.Fatal("transport error never surfaced through Notify")
	}
	wantTranscript(t, session,
		core.Message{Role: core.RoleUser, Content: "one"},
		core.Message{Role: core.RoleUser, Content: "two"},
		core.Message{Role: core.RoleAssistant, Content: "up again"},
	)
}

func TestClosedStreamWithoutDoneFailsLoud(t *testing.T) {
	p := &scriptedProvider{turns: []scriptedTurn{{
		events: []core.Event{textEv("half a thought")},
		bare:   true,
	}}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := looper.New(
		looper.WithProvider(p),
		looper.WithFrontend(f),
		looper.WithPolicy(transcriptPolicy{}),
	)
	k.Session = session

	f.inputs <- "speak"
	close(f.inputs)
	err := loop.Run(context.Background(), k)
	if err == nil {
		t.Fatal("provider closed the stream without Done or Fault; Run must fail loudly")
	}
	wantTranscript(t, session, core.Message{Role: core.RoleUser, Content: "speak"})
}

func TestCancellationMidStream(t *testing.T) {
	p := &scriptedProvider{turns: []scriptedTurn{{
		events:  []core.Event{textEv("never delivered")},
		holdCtx: true,
	}}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := looper.New(
		looper.WithProvider(p),
		looper.WithFrontend(f),
		looper.WithPolicy(transcriptPolicy{}),
	)
	k.Session = session

	ctx, cancel := context.WithCancel(context.Background())
	f.inputs <- "speak"
	cancel() // armed before Run: Input drains the line first, then the stream tears down
	close(f.inputs)
	if err := loop.Run(ctx, k); err != nil {
		t.Fatalf("cancellation must exit cleanly, got %v", err)
	}
}

func TestCancellationBetweenToolCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// the cancellation lands inside the first tool execution, so the
	// boundary between tool calls and the next model call is exercised
	// without racing Input.
	bash := &scriptedTool{name: "bash", cancel: cancel}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{
			callEv(core.ToolCall{ID: "c1", Name: "bash"}),
			doneEv(),
		}},
		{holdCtx: true}, // second model call meets the cancelled context
	}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := looper.New(
		looper.WithProvider(p),
		looper.WithFrontend(f),
		looper.WithPolicy(transcriptPolicy{}),
		looper.WithTools(bash),
	)
	k.Session = session

	f.inputs <- "run it"
	close(f.inputs)
	if err := loop.Run(ctx, k); err != nil {
		t.Fatalf("cancellation between tool calls must exit cleanly, got %v", err)
	}

	// transcript preserved through the complete assistant turn.
	if len(session.Messages) < 2 || session.Messages[1].Role != core.RoleAssistant {
		t.Fatalf("transcript must preserve through the tool-call turn, got: %s", dump(session))
	}
}

func TestMalformedCallFedBackOnce(t *testing.T) {
	bash := &scriptedTool{name: "bash", fail: 1, result: "recovered"}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{
			callEv(core.ToolCall{ID: "c1", Name: "bash"}),
			doneEv(),
		}},
		{events: []core.Event{textEv("recovered"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := looper.New(
		looper.WithProvider(p),
		looper.WithFrontend(f),
		looper.WithPolicy(transcriptPolicy{}),
		looper.WithTools(bash),
	)
	k.Session = session

	f.inputs <- "go"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}

	if bash.calls != 1 {
		t.Fatalf("loop retried the malformed call: %d executions, want exactly 1", bash.calls)
	}
	want := []core.Message{
		{Role: core.RoleUser, Content: "go"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash"}}},
		{Role: core.RoleTool, ToolID: "c1", Content: "synthetic failure"},
		{Role: core.RoleAssistant, Content: "recovered"},
	}
	wantTranscript(t, session, want...)
}

func TestOversizedToolResultStaysIntact(t *testing.T) {
	huge := strings.Repeat("x", 2<<20)
	bash := &scriptedTool{name: "bash", result: huge}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{
			callEv(core.ToolCall{ID: "c1", Name: "bash"}),
			doneEv(),
		}},
		{events: []core.Event{textEv("done"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := looper.New(
		looper.WithProvider(p),
		looper.WithFrontend(f),
		looper.WithPolicy(transcriptPolicy{}),
		looper.WithTools(bash),
	)
	k.Session = session

	f.inputs <- "go big"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}

	var result string
	for _, m := range session.Messages {
		if m.Role == core.RoleTool {
			result = m.Content
		}
	}
	if result != huge {
		t.Fatalf("tool result at the context edge mutated: got %d bytes, want %d", len(result), len(huge))
	}
}

func TestMissingSeamsFailLoud(t *testing.T) {
	f := &recorderFrontend{inputs: make(chan string, 8)}
	k := looper.New(looper.WithFrontend(f), looper.WithPolicy(transcriptPolicy{}))
	err := loop.Run(context.Background(), k)
	if err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("kernel without a provider must fail loudly naming the seam, got %v", err)
	}
}

func TestDuplicateToolNamesPanic(t *testing.T) {
	a := &scriptedTool{name: "bash", result: "a"}
	b := &scriptedTool{name: "bash", result: "b"}
	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("duplicate tool names must panic at wiring time")
		}
	}()
	looper.New(looper.WithTools(a, b))
}
