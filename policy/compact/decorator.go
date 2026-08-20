package compact

import (
	"context"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
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

func (d *decorator) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	mt, err := d.p.clampMaxTokens(d.p.sizeOf(req.Messages))
	if err != nil {
		return nil, err
	}
	req.MaxTokens = mt
	ch, err := d.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan core.Event, 4)
	go func() {
		defer close(out)
		d.relay(ctx, out, ch, req)
	}()
	return out, nil
}

func (d *decorator) relay(ctx context.Context, out chan<- core.Event, ch <-chan core.Event, req core.Request) {
	emit := func(ev core.Event) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for ev := range ch {
		switch e := ev.(type) {
		case core.Fault:
			if ctx.Err() == nil && classifiesContextLength(e.Err) && d.p.recoveryOwed() {
				ev2, compacted, cerr := d.p.compact(ctx)
				d.p.spendBudget()
				if cerr != nil {
					if ctx.Err() != nil {
						return
					}
					if !emit(core.Fault{Err: cerr}) {
						return
					}
					return
				}
				if !compacted {
					if !emit(e) {
						return
					}
					return
				}
				if !emit(ev2) {
					return
				}
				req2 := core.Request{Messages: d.p.assemble(d.p.s), Tools: req.Tools}
				mt, merr := d.p.clampMaxTokens(d.p.sizeOf(req2.Messages))
				if merr != nil {
					if !emit(core.Fault{Err: merr}) {
						return
					}
					return
				}
				req2.MaxTokens = mt
				ch2, err2 := d.inner.Stream(ctx, req2)
				if err2 != nil {
					if ctx.Err() != nil {
						return
					}
					if !emit(core.Fault{Err: err2}) {
						return
					}
					return
				}
				for ev3 := range ch2 {
					if done, ok := ev3.(core.Done); ok {
						d.p.calibrate(req2, done.Usage)
					}
					if !emit(ev3) {
						return
					}
				}
				return
			}
			if !emit(e) {
				return
			}
			return
		case core.Done:
			d.p.calibrate(req, e.Usage)
			if !emit(e) {
				return
			}
			return
		default:
			if !emit(ev) {
				return
			}
		}
	}
}
