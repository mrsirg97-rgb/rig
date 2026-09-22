package empty_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/loop"
	"github.com/mrsirg97-rgb/rig/policy"
	empty "github.com/mrsirg97-rgb/rig/policy/empty"
)

type scriptedTurn struct {
	events  []core.Event
	err     error
	signal  func()
	blockOn chan struct{}
}

type scriptedProvider struct {
	mu       sync.Mutex
	turns    []scriptedTurn
	n        int
	captured []core.Request
}

func (p *scriptedProvider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	p.mu.Lock()
	if p.n >= len(p.turns) {
		p.mu.Unlock()
		panic("scriptedProvider: more model calls than scripted")
	}
	turn := p.turns[p.n]
	p.n++
	p.captured = append(p.captured, req)
	p.mu.Unlock()
	if turn.signal != nil {
		turn.signal()
	}
	if turn.blockOn != nil {
		select {
		case <-turn.blockOn:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if turn.err != nil {
		return nil, turn.err
	}
	out := make(chan core.Event, len(turn.events)+1)
	go func() {
		defer close(out)
		for _, ev := range turn.events {
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p *scriptedProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

func (p *scriptedProvider) reqs() []core.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]core.Request(nil), p.captured...)
}

type testFrontend struct {
	mu     sync.Mutex
	inputs []string
	cancel context.CancelFunc
	events []core.Event
}

func (f *testFrontend) Input(ctx context.Context) (string, error) {
	if cancel, ok := core.InterruptFrom(ctx); ok {
		f.mu.Lock()
		f.cancel = cancel
		f.mu.Unlock()
	}
	f.mu.Lock()
	if len(f.inputs) == 0 {
		f.mu.Unlock()
		return "", io.EOF
	}
	s := f.inputs[0]
	f.inputs = f.inputs[1:]
	f.mu.Unlock()
	return s, nil
}

func (f *testFrontend) Notify(ev core.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *testFrontend) snapshot() []core.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.Event(nil), f.events...)
}

func (f *testFrontend) steal() context.CancelFunc {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.cancel
	f.cancel = nil
	return c
}

func run(t *testing.T, prov *scriptedProvider, fe *testFrontend) *rig.Kernel {
	t.Helper()
	k := rig.New(rig.WithProvider(empty.Decorator(prov)), rig.WithFrontend(fe), rig.WithPolicy(policy.Passthrough("")))
	k.Session = core.NewSession()
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("loop.Run: %v", err)
	}
	return k
}

func TestEmptyTurnResamplesSameRequest(t *testing.T) {
	discarded := core.ReasoningDelta{Text: "thinking about an edit\n<invoke name=\"edit\" path=\"x\">"}
	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{discarded, core.Done{StopReason: "stop", Usage: core.Usage{Prompt: 100, Completion: 268, CacheRead: 99}}}},
		{events: []core.Event{core.ReasoningDelta{Text: "ok"}, core.TextDelta{Text: "answer"}, core.Done{StopReason: "stop", Usage: core.Usage{Prompt: 100, Completion: 10, CacheRead: 99}}}},
	}}
	fe := &testFrontend{inputs: []string{"go"}}
	k := run(t, prov, fe)

	if prov.calls() != 2 {
		t.Fatalf("provider calls = %d, want 2", prov.calls())
	}
	reqs := prov.reqs()
	if !reflect.DeepEqual(reqs[0], reqs[1]) {
		t.Fatalf("the resample must reuse the identical request: %+v vs %+v", reqs[0], reqs[1])
	}

	seen := false
	notice := false
	for _, ev := range fe.snapshot() {
		switch e := ev.(type) {
		case core.ReasoningDelta:
			if strings.Contains(e.Text, "thinking about an edit") {
				seen = true
			}
		case core.EmptyTurn:
			notice = true
			if e.Resample != 1 || e.Limit != 2 || e.Usage != (core.Usage{Prompt: 100, Completion: 268, CacheRead: 99}) {
				t.Fatalf("EmptyTurn = %+v, want resample 1/2 with the discarded usage", e)
			}
		}
	}
	if !seen {
		t.Fatalf("the discarded reasoning must stream live to the frontend: %v", fe.snapshot())
	}
	if !notice {
		t.Fatalf("the frontend must see the resampling notice, got %v", fe.snapshot())
	}

	want := []core.Message{
		{Role: core.RoleUser, Content: "go"},
		{Role: core.RoleAssistant, Content: "answer", Reasoning: "thinking about an edit\n<invoke name=\"edit\" path=\"x\">ok", ContextTokens: 110},
	}
	if !reflect.DeepEqual(k.Session.Messages, want) {
		t.Fatalf("the transcript must contain only the good turn: %+v", k.Session.Messages)
	}
}

func TestThreeEmptyTurnsSurfaceTheFault(t *testing.T) {
	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.ReasoningDelta{Text: "first"}, core.Done{StopReason: "stop", Usage: core.Usage{Prompt: 5, Completion: 1}}}},
		{events: []core.Event{core.ReasoningDelta{Text: "second"}, core.Done{StopReason: "stop", Usage: core.Usage{Prompt: 5, Completion: 2}}}},
		{events: []core.Event{core.ReasoningDelta{Text: "third"}, core.Done{StopReason: "stop", Usage: core.Usage{Prompt: 5, Completion: 3}}}},
	}}
	fe := &testFrontend{inputs: []string{"go"}}
	k := run(t, prov, fe)

	if prov.calls() != 3 {
		t.Fatalf("provider calls = %d, want 3", prov.calls())
	}
	var notices int
	var fault *core.Fault
	for _, ev := range fe.snapshot() {
		switch e := ev.(type) {
		case core.EmptyTurn:
			notices++
		case core.Fault:
			fault = &e
		}
	}
	if notices != 2 {
		t.Fatalf("EmptyTurn notices = %d, want 2 (one per resample)", notices)
	}
	if fault == nil {
		t.Fatal("the third empty turn must surface a fault")
	}
	want := "model returned an empty turn 3 times (no content, no tool call)"
	if fault.Err.Error() != want {
		t.Fatalf("fault = %q, want %q", fault.Err.Error(), want)
	}
	if !reflect.DeepEqual(k.Session.Messages, []core.Message{{Role: core.RoleUser, Content: "go"}}) {
		t.Fatalf("the transcript must be unchanged after three empty turns: %+v", k.Session.Messages)
	}
}

func TestFaultNamesToolCallInsideThinking(t *testing.T) {
	emptyTurn := func(reason string) scriptedTurn {
		return scriptedTurn{events: []core.Event{core.ReasoningDelta{Text: reason}, core.Done{StopReason: "stop"}}}
	}
	prov := &scriptedProvider{turns: []scriptedTurn{
		emptyTurn("plain"),
		emptyTurn("still plain"),
		emptyTurn("buried\n<invoke name=\"edit\" old=\"a\" new=\"b\">\nrest"),
	}}
	fe := &testFrontend{inputs: []string{"go"}}
	run(t, prov, fe)
	var fault *core.Fault
	for _, ev := range fe.snapshot() {
		if e, ok := ev.(core.Fault); ok {
			fault = &e
		}
	}
	if fault == nil || !strings.Contains(fault.Err.Error(), "last reasoning ended with what looks like a tool call written inside its thinking") {
		t.Fatalf("the marker clause must name the thinking-block tool call: %v", fault)
	}
}

func TestFaultOmitsMarkerClauseWithoutMarker(t *testing.T) {
	emptyTurn := func(reason string) scriptedTurn {
		return scriptedTurn{events: []core.Event{core.ReasoningDelta{Text: reason}, core.Done{StopReason: "stop"}}}
	}
	prov := &scriptedProvider{turns: []scriptedTurn{
		emptyTurn("no marker here"),
		emptyTurn("still none"),
		emptyTurn("nothing either"),
	}}
	fe := &testFrontend{inputs: []string{"go"}}
	run(t, prov, fe)
	for _, ev := range fe.snapshot() {
		if e, ok := ev.(core.Fault); ok {
			if strings.Contains(e.Err.Error(), "tool call written inside its thinking") {
				t.Fatalf("the marker clause must not appear without a marker: %v", e.Err)
			}
			return
		}
	}
	t.Fatal("the third empty turn must fault")
}

func TestLengthFinishIsNotResampled(t *testing.T) {
	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.ReasoningDelta{Text: "partial thinking"}, core.Done{StopReason: "length", Usage: core.Usage{Prompt: 5, Completion: 4}}}},
	}}
	fe := &testFrontend{inputs: []string{"go"}}
	k := run(t, prov, fe)
	if prov.calls() != 1 {
		t.Fatalf("a length-cut turn must not be resampled: %d calls", prov.calls())
	}
	for _, ev := range fe.snapshot() {
		switch ev.(type) {
		case core.EmptyTurn, core.Fault:
			t.Fatalf("the length-cut turn must keep the existing behavior, got %+v", ev)
		}
	}
	if len(k.Session.Messages) != 2 || k.Session.Messages[1].Reasoning != "partial thinking" {
		t.Fatalf("the length-cut thinking must persist as today: %+v", k.Session.Messages)
	}
}

func TestContentTurnIsNotResampled(t *testing.T) {
	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.ReasoningDelta{Text: "think"}, core.TextDelta{Text: "final answer"}, core.Done{StopReason: "stop"}}},
	}}
	fe := &testFrontend{inputs: []string{"go"}}
	run(t, prov, fe)
	if prov.calls() != 1 {
		t.Fatalf("a content turn must not be resampled: %d calls", prov.calls())
	}
	for _, ev := range fe.snapshot() {
		if _, ok := ev.(core.EmptyTurn); ok {
			t.Fatalf("the content turn must not emit the notice: %+v", ev)
		}
	}
}

func TestCancelDuringResampleStopsClean(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.ReasoningDelta{Text: "first"}, core.Done{StopReason: "stop"}}},
		{signal: func() { close(started) }, blockOn: block},
	}}
	fe := &testFrontend{inputs: []string{"go"}}
	k := rig.New(rig.WithProvider(empty.Decorator(prov)), rig.WithFrontend(fe), rig.WithPolicy(policy.Passthrough("")))
	k.Session = core.NewSession()
	done := make(chan error, 1)
	go func() { done <- loop.Run(context.Background(), k) }()

	<-started
	if cancel := fe.steal(); cancel == nil {
		t.Fatal("the loop must hand the frontend its interrupt handle")
	} else {
		cancel()
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatalf("loop.Run: %v (a cancel during the resample must not fault)", err)
	}
	if prov.calls() != 2 {
		t.Fatalf("the cancel must not start a third call: %d calls", prov.calls())
	}
	for _, ev := range fe.snapshot() {
		if _, ok := ev.(core.Fault); ok {
			t.Fatalf("the cancel must not emit a fault: %+v", ev)
		}
		if e, ok := ev.(core.TurnEnd); ok && e.Reason != core.TurnInterrupt {
			t.Fatalf("the cancel must close the turn as an interrupt, got %+v", e)
		}
	}
}

func TestResampleStreamErrorSurfaces(t *testing.T) {
	prov := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{core.ReasoningDelta{Text: "x"}, core.Done{StopReason: "stop"}}},
		{err: errors.New("resample: transport down")},
	}}
	fe := &testFrontend{inputs: []string{"go"}}
	run(t, prov, fe)
	saw := false
	for _, ev := range fe.snapshot() {
		if e, ok := ev.(core.Fault); ok && strings.Contains(e.Err.Error(), "transport down") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a resample stream error must surface as a fault: %v", fe.snapshot())
	}
}
