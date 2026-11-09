package empty

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
)

const maxResamples = 2

var toolCallMarkers = []string{"<invoke name=", "<tool_call>", "<function="}

type decorator struct {
	inner core.Provider
}

func Decorator(inner core.Provider) core.Provider {
	return &decorator{inner: inner}
}

func (d *decorator) Stream(ctx context.Context, req core.Request) (<-chan evt.Signal, error) {
	ch, err := d.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan evt.Signal, 4)
	go func() {
		defer close(out)
		d.relay(ctx, out, req, ch)
	}()
	return out, nil
}

func (d *decorator) relay(ctx context.Context, out chan<- evt.Signal, req core.Request, ch <-chan evt.Signal) {
	attempt := 1
	var last *emptyTurn
	for {
		usage, model, turn, empty := d.relayOnce(out, ch)
		if !empty {
			return
		}
		if ctx.Err() != nil {
			out <- core.Done{Usage: usage, Model: model}
			return
		}
		if last != nil && *last == turn {
			out <- core.Fault{Err: errors.New(sameMessage(turn.reasoning))}
			return
		}
		if attempt > maxResamples {
			out <- core.Fault{Err: errors.New(faultMessage(turn.reasoning))}
			return
		}
		last = &turn
		out <- core.EmptyTurn{Resample: attempt, Limit: maxResamples, Usage: usage, Model: model}
		attempt++
		ch2, err := d.inner.Stream(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				out <- core.Done{Usage: usage, Model: model}
				return
			}
			out <- core.Fault{Err: err}
			return
		}
		ch = ch2
	}
}

// emptyTurn is what an empty attempt said: a resample that says it again came from a model that answers this
// request the same way every time (greedy decoding), so a third attempt cannot differ
type emptyTurn struct {
	content, reasoning string
}

func (d *decorator) relayOnce(out chan<- evt.Signal, ch <-chan evt.Signal) (core.Usage, string, emptyTurn, bool) {
	var (
		content strings.Builder
		reason  strings.Builder
		calls   int
	)
	for ev := range ch {
		switch e := ev.(type) {
		case core.TextDelta:
			content.WriteString(e.Text)
		case core.ReasoningDelta:
			reason.WriteString(e.Text)
		case core.ToolCallEvent:
			calls++
		}
		if e, ok := ev.(core.Done); ok && e.StopReason == "stop" && strings.TrimSpace(content.String()) == "" && calls == 0 {
			return e.Usage, e.Model, emptyTurn{content.String(), reason.String()}, true
		}
		out <- ev
	}
	return core.Usage{}, "", emptyTurn{}, false
}

func faultMessage(reasoning string) string {
	return withMarker(fmt.Sprintf("model returned an empty turn %d times (no content, no tool call)", maxResamples+1), reasoning)
}

func sameMessage(reasoning string) string {
	return withMarker("model returned the same empty turn twice (no content, no tool call): it answers this request "+
		"the same way every time, so resampling cannot change it", reasoning)
}

func withMarker(msg, reasoning string) string {
	for _, marker := range toolCallMarkers {
		if strings.Contains(reasoning, marker) {
			return msg + "; last reasoning ended with what looks like a tool call written inside its thinking"
		}
	}
	return msg
}
