package effort_test

import (
	"context"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/policy/effort"
)

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
