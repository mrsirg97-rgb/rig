package effort_test

import (
	"context"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/policy/effort"
)

// recProvider is a fake core.Provider that captures the requests it sees
// and streams one Done. The DI seam: the decorator's only job is to
// stamp the session's effort, so the provider's reply does not matter.
type recProvider struct {
	mu   sync.Mutex
	reqs []core.Request
}

func (p *recProvider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()
	ch := make(chan core.Event, 1)
	ch <- core.Done{Usage: core.Usage{Prompt: 1}}
	close(ch)
	return ch, nil
}

func (p *recProvider) got() []core.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]core.Request(nil), p.reqs...)
}

func drain(ch <-chan core.Event) {
	for range ch {
	}
}

// TestDecoratorStampsSessionEffortOnBareRequest (SPEC_MODES, named): a
// request that carries no effort gains the session's. The main turn's
// request is built by the loop with none (core.Request{Messages, Tools}),
// so the decorator is the seam that puts the dial on the wire.
func TestDecoratorStampsSessionEffortOnBareRequest(t *testing.T) {
	inner := &recProvider{}
	d := effort.Decorator(inner, func() string { return "xhigh" })
	ch, err := d.Stream(context.Background(), core.Request{
		Messages: []core.Message{{Role: core.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	drain(ch)
	got := inner.got()
	if len(got) != 1 || got[0].ReasoningEffort != "xhigh" {
		t.Fatalf("the bare request must gain the session's effort xhigh: %+v", got)
	}
}

// TestDecoratorLeavesSummaryEffortUntouched (SPEC_MODES, named): a
// request that already carries an effort — the compaction summary call,
// which sets its own (the row's, SPEC_CONFIG 4) — is passed through
// untouched, whatever the session's dial says.
func TestDecoratorLeavesSummaryEffortUntouched(t *testing.T) {
	inner := &recProvider{}
	d := effort.Decorator(inner, func() string { return "xhigh" })
	ch, err := d.Stream(context.Background(), core.Request{
		Messages:        []core.Message{{Role: core.RoleUser, Content: "s"}},
		ReasoningEffort: "low",
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	drain(ch)
	got := inner.got()
	if len(got) != 1 || got[0].ReasoningEffort != "low" {
		t.Fatalf("the summary call's own effort must survive untouched: %+v", got)
	}
}

// TestDecoratorEmptyEffortIsTodayBytes (SPEC_MODES, named): with the
// dial unset the request is today's request bytes — the decorator
// stamps nothing, so the golden wire holds.
func TestDecoratorEmptyEffortIsTodayBytes(t *testing.T) {
	inner := &recProvider{}
	d := effort.Decorator(inner, func() string { return "" })
	ch, err := d.Stream(context.Background(), core.Request{
		Messages: []core.Message{{Role: core.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	drain(ch)
	got := inner.got()
	if len(got) != 1 || got[0].ReasoningEffort != "" {
		t.Fatalf("the unset dial must stamp nothing: %+v", got)
	}
}
