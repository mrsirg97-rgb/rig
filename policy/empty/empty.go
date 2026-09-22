package empty

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

const maxResamples = 2

var toolCallMarkers = []string{"<invoke name=", "<tool_call>", "<function="}

type decorator struct {
	inner core.Provider
}

func Decorator(inner core.Provider) core.Provider {
	return &decorator{inner: inner}
}

func (d *decorator) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	ch, err := d.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan core.Event, 4)
	go func() {
		defer close(out)
		d.relay(ctx, out, req, ch)
	}()
	return out, nil
}

func (d *decorator) relay(ctx context.Context, out chan<- core.Event, req core.Request, ch <-chan core.Event) {
	attempt := 1
	for {
		usage, reasoning, empty := d.relayOnce(ctx, out, ch)
		if ctx.Err() != nil {
			return
		}
		if !empty {
			return
		}
		if attempt > maxResamples {
			if !emit(ctx, out, core.Fault{Err: errors.New(faultMessage(reasoning))}) {
				return
			}
			return
		}
		if !emit(ctx, out, core.EmptyTurn{Resample: attempt, Limit: maxResamples, Usage: usage}) {
			return
		}
		attempt++
		ch2, err := d.inner.Stream(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !emit(ctx, out, core.Fault{Err: err}) {
				return
			}
			return
		}
		ch = ch2
	}
}

func (d *decorator) relayOnce(ctx context.Context, out chan<- core.Event, ch <-chan core.Event) (core.Usage, string, bool) {
	var (
		content strings.Builder
		reason  strings.Builder
		calls   int
	)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return core.Usage{}, "", false
			}
			switch e := ev.(type) {
			case core.TextDelta:
				content.WriteString(e.Text)
			case core.ReasoningDelta:
				reason.WriteString(e.Text)
			case core.ToolCallEvent:
				calls++
			}
			switch e := ev.(type) {
			case core.Done:
				if e.StopReason == "stop" && strings.TrimSpace(content.String()) == "" && calls == 0 {
					return e.Usage, reason.String(), true
				}
				if !emit(ctx, out, ev) {
					return core.Usage{}, "", false
				}
				return core.Usage{}, "", false
			default:
				if !emit(ctx, out, ev) {
					return core.Usage{}, "", false
				}
			}
		case <-ctx.Done():
			return core.Usage{}, "", false
		}
	}
}

func faultMessage(reasoning string) string {
	msg := fmt.Sprintf("model returned an empty turn %d times (no content, no tool call)", maxResamples+1)
	for _, marker := range toolCallMarkers {
		if strings.Contains(reasoning, marker) {
			return msg + "; last reasoning ended with what looks like a tool call written inside its thinking"
		}
	}
	return msg
}

func emit(ctx context.Context, out chan<- core.Event, ev core.Event) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
