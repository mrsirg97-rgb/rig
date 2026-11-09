// Package lineage keeps a model's reasoning to itself: a request carries the reasoning of the turns this model row
// wrote, and only their text for turns another row wrote (SPEC_LINEAGE).
package lineage

import (
	"context"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
)

// Own is the reasoning one model row wrote in one session, as the loop's messages carry it (each stream's reasoning
// deltas joined). Every decorator built for the row shares it, so a rebuilt provider (a role switch) keeps it.
type Own struct {
	mu    sync.Mutex
	texts map[string]struct{}
}

// NewOwn is a row's record, seeded with reasoning it wrote before (a resumed session's, from the store).
func NewOwn(seed ...string) *Own {
	o := &Own{texts: map[string]struct{}{}}
	for _, s := range seed {
		o.add(s)
	}
	return o
}

func (o *Own) add(s string) {
	if s == "" {
		return
	}
	o.mu.Lock()
	o.texts[s] = struct{}{}
	o.mu.Unlock()
}

func (o *Own) has(s string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.texts[s]
	return ok
}

type decorator struct {
	inner core.Provider
	own   *Own
}

// Decorator wraps the row's provider: the request's other turns lose their reasoning (the session is never changed,
// only the request's copy), and what this row reasons is recorded as its own.
func Decorator(inner core.Provider, own *Own) core.Provider {
	return &decorator{inner: inner, own: own}
}

func (d *decorator) Stream(ctx context.Context, req core.Request) (<-chan evt.Signal, error) {
	req.Messages = d.theirsStripped(req.Messages)
	ch, err := d.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan evt.Signal, 4)
	go func() {
		defer close(out)
		var reason strings.Builder
		for ev := range ch {
			if e, ok := ev.(core.ReasoningDelta); ok {
				reason.WriteString(e.Text)
			}
			if _, ok := ev.(core.Done); ok {
				d.own.add(reason.String())
			}
			out <- ev
		}
	}()
	return out, nil
}

// theirsStripped: a copy of msgs whose assistant turns not this row's carry their text alone. a turn with reasoning
// details (signed, a hosted provider's) is left as it is: it must go back unchanged.
func (d *decorator) theirsStripped(msgs []core.Message) []core.Message {
	out := make([]core.Message, len(msgs))
	copy(out, msgs)
	for i, m := range out {
		if m.Role == core.RoleAssistant && m.Reasoning != "" && len(m.ReasoningDetails) == 0 && !d.own.has(m.Reasoning) {
			out[i].Reasoning = ""
		}
	}
	return out
}
