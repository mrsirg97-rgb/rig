package loop_test

import (
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/loop"
)

type overlapFrontend struct {
	inputs    chan string
	events    []core.Event
	inNotify  atomic.Int32
	overlaps  atomic.Int32
	inTurn    atomic.Bool
	askedLive atomic.Int32
}

func (f *overlapFrontend) Input(ctx context.Context) (string, error) {
	if f.inTurn.Load() {
		f.askedLive.Add(1)
	}
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

func (f *overlapFrontend) Notify(ev core.Event) {
	if !f.inNotify.CompareAndSwap(0, 1) {
		f.overlaps.Add(1)
	}
	time.Sleep(200 * time.Microsecond)
	f.events = append(f.events, ev)
	if _, ok := ev.(core.TurnEnd); ok {
		f.inTurn.Store(false)
	}
	f.inNotify.Store(0)
}

func TestConsumerIsOneGoroutine(t *testing.T) {
	read := newTimed("read")
	calls := []core.ToolCall{timedCall("c1", "read", 40), timedCall("c2", "read", 5), timedCall("c3", "read", 20), timedCall("c4", "read", 1)}
	evs := make([]core.Event, 0, len(calls)+1)
	for _, c := range calls {
		evs = append(evs, callEv(c))
	}
	evs = append(evs, doneEv())
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: evs},
		{events: []core.Event{textEv("a"), textEv("b"), textEv("c"), doneEv()}},
		{events: []core.Event{textEv("second"), doneEv()}},
	}}
	f := &overlapFrontend{inputs: make(chan string, 2)}
	k := rig.New(rig.WithProvider(p), rig.WithFrontend(&turnMarking{f}), rig.WithPolicy(&transcriptPolicy{}), rig.WithTools(read), rig.WithConcurrent(onlyRead))
	k.Session = core.NewSession()
	f.inputs <- "go"
	f.inputs <- "again"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if f.overlaps.Load() != 0 {
		t.Fatalf("Notify overlapped %d times: the consumer is not one goroutine", f.overlaps.Load())
	}
	if f.askedLive.Load() != 0 {
		t.Fatalf("Input was asked %d times during a live turn", f.askedLive.Load())
	}
	got := resultOrder(&recorderFrontend{events: f.events})
	if !same(got, []string{"c1", "c2", "c3", "c4"}) {
		t.Fatalf("results %v, want call order", got)
	}
	if read.peak.Load() < 2 {
		t.Fatalf("the reads did not overlap (peak %d)", read.peak.Load())
	}
	ends := 0
	for _, ev := range f.events {
		if _, ok := ev.(core.TurnEnd); ok {
			ends++
		}
	}
	if ends != 2 {
		t.Fatalf("TurnEnd count %d, want 2", ends)
	}
}

type turnMarking struct{ *overlapFrontend }

func (m *turnMarking) Input(ctx context.Context) (string, error) {
	line, err := m.overlapFrontend.Input(ctx)
	if err == nil {
		m.inTurn.Store(true)
	}
	return line, err
}

func TestStaleCompletionIsIgnored(t *testing.T) {
	slow := &slowCancelTool{delay: 50 * time.Millisecond}
	call := core.ToolCall{ID: "c1", Name: "slow", Args: json.RawMessage(`{}`)}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []core.Event{callEv(call), doneEv()}},
		{holdCtx: true},
	}}
	f := &recorderFrontend{inputs: make(chan string, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	slow.cancel = cancel
	k := rig.New(rig.WithProvider(p), rig.WithFrontend(f), rig.WithPolicy(&transcriptPolicy{}), rig.WithTools(slow))
	k.Session = core.NewSession()
	f.inputs <- "go"
	close(f.inputs)
	if err := loop.Run(ctx, k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(k.Session.Messages) < 3 || k.Session.Messages[2].Role != core.RoleTool {
		t.Fatalf("the completion that landed before the run ended must be in the transcript: %s", dump(k.Session))
	}
}

type slowCancelTool struct {
	delay  time.Duration
	cancel context.CancelFunc
}

func (s *slowCancelTool) Name() string            { return "slow" }
func (s *slowCancelTool) Description() string     { return "slow" }
func (s *slowCancelTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *slowCancelTool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	time.Sleep(s.delay)
	s.cancel()
	return "late", nil
}
