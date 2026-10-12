package compact

import (
	"context"
	"strings"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
)

type decorator struct {
	inner core.Provider
	p     *policy
}

func Decorator(inner core.Provider, p *policy) core.Provider {
	return &decorator{inner: inner, p: p}
}

var contextPhrases = []string{
	"context length",
	"context_length",
	"context window",
	"maximum context",
	"max context",
	"prompt is too long",
	"prompt too long",
	"too many tokens",
	"exceeds the maximum",
}

func classifiesContextLength(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, p := range contextPhrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func (d *decorator) Stream(ctx context.Context, req core.Request) (<-chan evt.Signal, error) {
	mt, err := d.p.clampMaxTokens(d.p.sizeOf(req.Messages))
	if err != nil {
		return nil, err
	}
	req.MaxTokens = mt
	ch, err := d.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan evt.Signal, 4)
	go func() {
		defer close(out)
		d.relay(ctx, out, ch, req)
	}()
	return out, nil
}

func (d *decorator) relay(ctx context.Context, out chan<- evt.Signal, ch <-chan evt.Signal, req core.Request) {
	for ev := range ch {
		switch e := ev.(type) {
		case core.Fault:
			if ctx.Err() != nil || !classifiesContextLength(e.Err) || !d.p.recoveryOwed() {
				out <- e
				continue
			}
			ev2, compacted, cerr := d.p.compact(ctx)
			d.p.spendBudget()
			if cerr != nil {
				if ctx.Err() == nil {
					out <- core.Fault{Err: cerr}
				}
				continue
			}
			if !compacted {
				out <- e
				continue
			}
			out <- ev2
			req2 := core.Request{Messages: d.p.assemble(d.p.s), Tools: req.Tools}
			mt, merr := d.p.clampMaxTokens(d.p.sizeOf(req2.Messages))
			if merr != nil {
				out <- core.Fault{Err: merr}
				continue
			}
			req2.MaxTokens = mt
			ch2, err2 := d.inner.Stream(ctx, req2)
			if err2 != nil {
				if ctx.Err() == nil {
					out <- core.Fault{Err: err2}
				}
				continue
			}
			for ev3 := range ch2 {
				if done, ok := ev3.(core.Done); ok {
					d.p.calibrate(req2, done.Usage)
				}
				out <- ev3
			}
		case core.Done:
			d.p.calibrate(req, e.Usage)
			out <- e
		default:
			out <- ev
		}
	}
}
