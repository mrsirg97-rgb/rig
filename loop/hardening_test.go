package loop_test

// The SPEC_HARDENING named cases (failing first): the tool-event bracket,
// the reasoning accumulation, the steering contract (L2, L3), the interrupt
// handle (L1), the TurnStart fan-out (L6), the TurnEnd vocabulary (L7),
// and the compat rule (TestEvent).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/loop"
	"github.com/mrsirg97-rgb/rig/middleware/guard"
	"github.com/mrsirg97-rgb/rig/middleware/perm"
)

func reasonEv(s string) core.Event { return core.ReasoningDelta{Text: s} }

func turnEnds(events []core.Event) []core.TurnEnd {
	var out []core.TurnEnd
	for _, ev := range events {
		if te, ok := ev.(core.TurnEnd); ok {
			out = append(out, te)
		}
	}
	return out
}

func hasFault(events []core.Event) bool {
	for _, ev := range events {
		if _, ok := ev.(core.Fault); ok {
			return true
		}
	}
	return false
}

// L4: the bracket order for a tool turn, the guarded result carried, the
// transcript byte-identical to TestToolRoundTripOrdering.
func TestToolEventBracketOrderAndContent(t *testing.T) {
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
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
		rig.WithTools(bash),
	)
	k.Session = session

	f.inputs <- "what is the answer?"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}

	// the transcript is byte-identical to TestToolRoundTripOrdering's.
	want := []core.Message{
		{Role: core.RoleUser, Content: "what is the answer?"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash"}}},
		{Role: core.RoleTool, ToolID: "c1", Content: "42"},
		{Role: core.RoleAssistant, Content: "the answer is 42"},
	}
	wantTranscript(t, session, want...)

	// the bracket: ToolStart before ToolResult, after the stream's Done,
	// before the next model call's events.
	var (
		kinds  []string
		result *core.ToolResult
	)
	for _, ev := range f.events {
		switch e := ev.(type) {
		case core.ToolCallEvent:
			kinds = append(kinds, "call")
		case core.Done:
			kinds = append(kinds, "done")
		case core.TextDelta:
			kinds = append(kinds, "delta")
		case core.ToolStart:
			kinds = append(kinds, "start")
		case core.ToolResult:
			kinds = append(kinds, "result")
			if e.ID == "c1" {
				r := e
				result = &r
			}
		}
	}
	if strings.Join(kinds, ",") != "call,done,start,result,delta,delta,done" {
		t.Fatalf("bracket order = %v, want call,done,start,result,delta,delta,done", kinds)
	}
	if result == nil || result.Content != "42" || result.Err != nil {
		t.Fatalf("ToolResult must carry the executed result, got %+v", result)
	}
	if result.Duration <= 0 {
		t.Fatalf("Duration must be measured by the loop, got %v", result.Duration)
	}
}

// L4 with the chain: ToolResult carries the guarded result — the refusal,
// named, with Err non-nil (the fed-back failure marker).
func TestToolResultCarriesTheGuardedRefusal(t *testing.T) {
	bash := &countingTool{name: "bash", fail: 999}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{callEv(core.ToolCall{ID: "c1", Name: "bash"}), doneEv()}},
		{events: []core.Event{callEv(core.ToolCall{ID: "c2", Name: "bash"}), doneEv()}},
		{events: []core.Event{textEv("recovered"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
		rig.WithTools(bash),
		rig.WithMiddleware(
			perm.Allowlist("bash"),
			guard.Bound(1),
		),
	)
	k.Session = session

	f.inputs <- "go"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}

	var results []core.ToolResult
	for _, ev := range f.events {
		if r, ok := ev.(core.ToolResult); ok {
			results = append(results, r)
		}
	}
	if len(results) != 2 {
		t.Fatalf("two ToolResults expected, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("the failing execution must carry Err (the fed-back failure marker)")
	}
	if results[1].Err == nil || !strings.Contains(results[1].Content, "stop reissuing") {
		t.Fatalf("the guarded refusal must ride ToolResult with Err non-nil, got %+v", results[1])
	}
}

// L7: TurnEnd closes every turn with the right reason, after the turn's
// last other event.
func TestTurnEndClosesEveryTurnWithTheRightReason(t *testing.T) {
	boom := errors.New("mid-stream fault")
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{textEv("one"), doneEv()}},
		{events: []core.Event{textEv("partial "), faultEv(boom)}},
		{events: []core.Event{textEv("three"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
	)
	k.Session = session

	f.inputs <- "a"
	f.inputs <- "b"
	f.inputs <- "c"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}

	ends := turnEnds(f.events)
	if len(ends) != 3 {
		t.Fatalf("TurnEnd count = %d, want 3 (one per turn)", len(ends))
	}
	want := []core.TurnReason{core.TurnOver, core.TurnFault, core.TurnOver}
	for i, w := range want {
		if ends[i].Reason != w {
			t.Fatalf("TurnEnd %d reason = %q, want %q", i, ends[i].Reason, w)
		}
	}
	// the fault's TurnEnd follows the Fault event, not the other way round.
	faultIdx, endIdx := -1, -1
	for i, ev := range f.events {
		if ft, ok := ev.(core.Fault); ok && errors.Is(ft.Err, boom) {
			faultIdx = i
		}
		if te, ok := ev.(core.TurnEnd); ok && te.Reason == core.TurnFault {
			endIdx = i
		}
	}
	if faultIdx < 0 || endIdx < 0 || faultIdx > endIdx {
		t.Fatalf("the fault TurnEnd must follow the Fault event (fault at %d, end at %d)", faultIdx, endIdx)
	}
}

// L7: a run-context cancel ends the run, not a turn — no TurnEnd.
func TestTurnEndAbsentOnRunContextEnd(t *testing.T) {
	p := &scriptedProvider{turns: []scriptedTurn{{
		events:  []core.Event{textEv("never delivered")},
		holdCtx: true,
	}}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
	)
	k.Session = session

	ctx, cancel := context.WithCancel(context.Background())
	f.inputs <- "speak"
	cancel() // armed before Run: the run context ends the run at the boundary
	close(f.inputs)
	if err := loop.Run(ctx, k); err != nil {
		t.Fatalf("cancellation must exit cleanly, got %v", err)
	}
	if ends := turnEnds(f.events); len(ends) != 0 {
		t.Fatalf("a run-context end must not emit TurnEnd, got %v", ends)
	}
}

// L5: reasoning accumulates onto the assistant message in both branches,
// and the deltas forward in stream order.
func TestReasoningAccumulatesInBothBranches(t *testing.T) {
	// the text-only branch.
	p := &scriptedProvider{turns: []scriptedTurn{{
		events: []core.Event{reasonEv("thinking... "), reasonEv("got it"), textEv("answer"), doneEv()},
	}}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
	)
	k.Session = session

	f.inputs <- "hi"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(session.Messages) != 2 {
		t.Fatalf("transcript = %s", dump(session))
	}
	asst := session.Messages[1]
	if asst.Role != core.RoleAssistant || asst.Reasoning != "thinking... got it" || asst.Content != "answer" {
		t.Fatalf("reasoning must accumulate onto the assistant message: %+v", asst)
	}
	var kinds []string
	for _, ev := range f.events {
		switch ev.(type) {
		case core.ReasoningDelta:
			kinds = append(kinds, "reasoning")
		case core.TextDelta:
			kinds = append(kinds, "delta")
		case core.Done:
			kinds = append(kinds, "done")
		}
	}
	if strings.Join(kinds, ",") != "reasoning,reasoning,delta,done" {
		t.Fatalf("stream order = %v, want reasoning,reasoning,delta,done", kinds)
	}

	// the tool-call branch: the thinking that led to the calls survives.
	bash := &scriptedTool{name: "bash", result: "42"}
	p2 := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{reasonEv("planning the call"), callEv(core.ToolCall{ID: "c1", Name: "bash"}), doneEv()}},
		{events: []core.Event{textEv("done"), doneEv()}},
	}}
	f2 := &recorderFrontend{inputs: make(chan string, 8)}
	session2 := core.NewSession()
	k2 := rig.New(
		rig.WithProvider(p2),
		rig.WithFrontend(f2),
		rig.WithPolicy(&transcriptPolicy{}),
		rig.WithTools(bash),
	)
	k2.Session = session2

	f2.inputs <- "run it"
	close(f2.inputs)
	if err := loop.Run(context.Background(), k2); err != nil {
		t.Fatalf("run: %v", err)
	}
	var withCalls bool
	for _, m := range session2.Messages {
		if len(m.ToolCalls) > 0 {
			withCalls = true
			if m.Reasoning != "planning the call" {
				t.Fatalf("the tool-call assistant message must carry its reasoning: %+v", m)
			}
		}
	}
	if !withCalls {
		t.Fatal("no assistant message with calls landed")
	}
}

// steeringFrontend holds the interrupt handle from each Input ctx (L1) and
// steers: "delta" cancels on the first streamed delta (mid-turn), "prompt"
// cancels and errors on the first Input (at the prompt, L2).
type steeringFrontend struct {
	inputs      []string
	n           int
	served      int
	events      []core.Event
	handle      context.CancelFunc
	mode        string
	steeredOnce bool
}

func (f *steeringFrontend) Input(ctx context.Context) (string, error) {
	f.n++
	if cancel, ok := core.InterruptFrom(ctx); ok {
		f.handle = cancel
	}
	if f.mode == "prompt" && f.n == 1 {
		f.handle() // steer at the prompt: the handle fires, the Input fails
		return "", errors.New("steered at the prompt")
	}
	if f.served >= len(f.inputs) {
		return "", io.EOF
	}
	line := f.inputs[f.served]
	f.served++
	return line, nil
}

func (f *steeringFrontend) Notify(ev core.Event) {
	f.events = append(f.events, ev)
	if f.mode == "delta" && !f.steeredOnce {
		if _, ok := ev.(core.TextDelta); ok {
			f.steeredOnce = true
			f.handle() // the line lands during the live turn: interrupt
		}
	}
}

// L3 + the interrupt handle (L1): a line during the live turn cancels the
// turn; the queued message is delivered on the re-entry; the run continues
// and the partial never lands.
func TestSteeringCancelsTheLiveTurnAndDeliversOnReentry(t *testing.T) {
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{textEv("partial ")}, holdAfter: true},
		{events: []core.Event{textEv("delivered"), doneEv()}},
	}}
	f := &steeringFrontend{inputs: []string{"speak", "steer"}, mode: "delta"}
	session := core.NewSession()
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
	)
	k.Session = session

	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("steering must not end the run, got %v", err)
	}

	// both turns land in the transcript; the partial never does.
	want := []core.Message{
		{Role: core.RoleUser, Content: "speak"},
		{Role: core.RoleUser, Content: "steer"},
		{Role: core.RoleAssistant, Content: "delivered"},
	}
	wantTranscript(t, session, want...)

	if hasFault(f.events) {
		t.Fatal("steering is not a fault")
	}
	ends := turnEnds(f.events)
	if len(ends) != 2 {
		t.Fatalf("TurnEnd count = %d, want 2 (the interrupted turn and the completed one)", len(ends))
	}
	if ends[0].Reason != core.TurnInterrupt || ends[1].Reason != core.TurnOver {
		t.Fatalf("reasons = %v/%v, want interrupt then over", ends[0].Reason, ends[1].Reason)
	}
	// the interrupt TurnEnd precedes the re-entry's events.
	endIdx, secondIdx := -1, -1
	for i, ev := range f.events {
		if te, ok := ev.(core.TurnEnd); ok && te.Reason == core.TurnInterrupt {
			endIdx = i
		}
		if _, ok := ev.(core.TextDelta); ok && i > 0 && secondIdx == -1 {
			if i >= 2 { // the first delta is the partial's
				secondIdx = i
			}
		}
	}
	if endIdx < 0 || secondIdx < 0 || endIdx > secondIdx {
		t.Fatalf("the interrupt must close the turn before the re-entry streams (end %d, second %d)", endIdx, secondIdx)
	}
}

// L2: a dead turn context at the prompt is an interrupt, not a fault: the
// loop re-enters awaiting_input and the run continues.
func TestSteeringAtThePromptReentersWithoutFault(t *testing.T) {
	p := &scriptedProvider{turns: []scriptedTurn{{
		events: []core.Event{textEv("real"), doneEv()},
	}}}
	f := &steeringFrontend{inputs: []string{"real"}, mode: "prompt"}
	session := core.NewSession()
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
	)
	k.Session = session

	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("an interrupt at the prompt must not end the run, got %v", err)
	}
	if hasFault(f.events) {
		t.Fatal("the interrupt at the prompt must not emit a Fault")
	}
	want := []core.Message{
		{Role: core.RoleUser, Content: "real"},
		{Role: core.RoleAssistant, Content: "real"},
	}
	wantTranscript(t, session, want...)
	ends := turnEnds(f.events)
	if len(ends) != 1 || ends[0].Reason != core.TurnOver {
		t.Fatalf("exactly one completed turn expected, got %v", ends)
	}
}

// the pre-stream seam (L1): a dead turn ctx at the Assemble or Stream call
// is the user's interrupt (or the session's end), not a provider fault —
// no Fault row, no re-entry; the turn re-prompts like L2. The real shape
// of that interleaving: the frontend serves the steer line and cancels
// the turn's interrupt handle in the same Input — by the time the loop
// reaches the first seam of the turn, the turn ctx is already dead.

// steerFrontend delivers one named line with the interrupt: it cancels
// the turn's handle (from the Input ctx) as it serves it.
type steerFrontend struct {
	*recorderFrontend
	steer string
}

func (f *steerFrontend) Input(ctx context.Context) (string, error) {
	line, err := f.recorderFrontend.Input(ctx)
	if err == nil && line == f.steer && f.steer != "" {
		f.steer = "" // once
		if cancel, ok := core.InterruptFrom(ctx); ok {
			cancel()
		}
	}
	return line, err
}

// ctxCheckingPolicy fails Assemble at call time when the context is dead
// — the "or a policy that does" case of the pre-stream seam.
type ctxCheckingPolicy struct{ *transcriptPolicy }

func (p *ctxCheckingPolicy) Assemble(ctx context.Context, s *core.Session) ([]core.Message, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return p.transcriptPolicy.Assemble(ctx, s)
}

// ctxCheckingProvider fails at call time when its context is already dead
// — the "a different Provider would" case of the pre-stream seam.
type ctxCheckingProvider struct{ *scriptedProvider }

func (p *ctxCheckingProvider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return p.scriptedProvider.Stream(ctx, req)
}

func TestPreStreamAssembleFailureOnADeadTurnIsAnInterrupt(t *testing.T) {
	p := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{textEv("second turn"), doneEv()}}}}
	f := &steerFrontend{recorderFrontend: &recorderFrontend{inputs: make(chan string, 8)}, steer: "first"}
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&ctxCheckingPolicy{transcriptPolicy: &transcriptPolicy{}}),
	)
	k.Session = core.NewSession()

	f.inputs <- "first"
	f.inputs <- "second"
	close(f.inputs)

	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// turn 1 died at Assemble: exactly one TurnEnd{interrupt}, no Fault,
	// no Done, and the provider was never called. Turn 2 is clean.
	if len(f.events) != 4 {
		t.Fatalf("events = %v, want TurnEnd, TextDelta, Done, TurnEnd", f.events)
	}
	te, ok := f.events[0].(core.TurnEnd)
	if !ok || te.Reason != core.TurnInterrupt {
		t.Fatalf("event 0 = %+v, want TurnEnd{interrupt} — no Fault: the model never started", f.events[0])
	}
	if p.calls != 1 {
		t.Fatalf("provider streamed %d times, want exactly 1 (turn 2 only)", p.calls)
	}
}

func TestPreStreamCallFailureOnADeadTurnIsAnInterrupt(t *testing.T) {
	inner := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{textEv("second turn"), doneEv()}}}}
	f := &steerFrontend{recorderFrontend: &recorderFrontend{inputs: make(chan string, 8)}, steer: "first"}
	k := rig.New(
		rig.WithProvider(&ctxCheckingProvider{inner}),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
	)
	k.Session = core.NewSession()

	f.inputs <- "first"
	f.inputs <- "second"
	close(f.inputs)

	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// turn 1 died at the Stream call: same shape — interrupt, no Fault.
	if len(f.events) != 4 {
		t.Fatalf("events = %v, want TurnEnd, TextDelta, Done, TurnEnd", f.events)
	}
	te, ok := f.events[0].(core.TurnEnd)
	if !ok || te.Reason != core.TurnInterrupt {
		t.Fatalf("event 0 = %+v, want TurnEnd{interrupt} — no Fault at the Stream seam either", f.events[0])
	}
	if inner.calls != 1 {
		t.Fatalf("provider streamed %d times, want exactly 1 (turn 2 only)", inner.calls)
	}
}

// L6: the turn fan-out fires once per user turn, before the first Assemble
// — not per model call.
type countingObserver struct {
	core.ToolMiddlewareFunc
	turns int
}

func (o *countingObserver) TurnStart(ctx context.Context, s *core.Session) { o.turns++ }

func TestTurnStartFansOutOncePerTurn(t *testing.T) {
	bash := &scriptedTool{name: "bash", result: "ok"}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{callEv(core.ToolCall{ID: "c1", Name: "bash"}), doneEv()}},
		{events: []core.Event{textEv("mid"), doneEv()}},
		{events: []core.Event{textEv("second turn"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	obs := &countingObserver{ToolMiddlewareFunc: func(next core.ToolExec) core.ToolExec { return next }}
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
		rig.WithTools(bash),
		rig.WithMiddleware(obs),
	)
	k.Session = session

	f.inputs <- "one"
	f.inputs <- "two"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if obs.turns != 2 {
		t.Fatalf("TurnStart fired %d times, want 2 (once per user turn, not per model call)", obs.turns)
	}
}

// decision 8: the loop forwards an event it does not name, untouched, in
// order; it does not accumulate it.
func TestTestEventForwardsUntouched(t *testing.T) {
	p := &scriptedProvider{turns: []scriptedTurn{{
		events: []core.Event{core.TestEvent{Name: "x"}, textEv("hello"), doneEv()},
	}}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
	)
	k.Session = session

	f.inputs <- "hi"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(f.events) == 0 {
		t.Fatal("no events forwarded at all")
	}
	first, ok := f.events[0].(core.TestEvent)
	if !ok || first.Name != "x" {
		t.Fatalf("the TestEvent must forward first and untouched, got %+v", f.events[0])
	}
	want := []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleAssistant, Content: "hello"},
	}
	wantTranscript(t, session, want...)
}
