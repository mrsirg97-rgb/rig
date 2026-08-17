package compact

import (
	"context"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

// decorator is the overflow recovery (decision 7): it relays the stream
// untouched (no buffering — incremental rendering is preserved) and, on a
// classifiable fault with budget left, runs the compact action,
// reassembles, and re-issues the same request shape (same tools) exactly
// once. A non-classifiable fault, or an exhausted budget, surfaces the
// fault as-is. Never a silent loop: the budget is structural (the
// transcript length at the last attempt), not a retry limit.
type decorator struct {
	inner core.Provider
	p     *policy
}

// Decorator registers the overflow decorator around the shared inner
// instance (decision 7's wiring). The summary call does not pass through
// it (1), so a fault in it surfaces as an Assemble error.
func Decorator(inner core.Provider, p *policy) core.Provider {
	return &decorator{inner: inner, p: p}
}

// contextPhrases is the classifier's wordlist (decision 7), the common
// phrasings of OpenAI, llama.cpp, and vLLM; the match is case-folded.
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

// Stream is the provider seam: the main call's MaxTokens is clamped to
// the window minus the request's anchor size (8's request-side reserve —
// the response budget is explicit, at least the reserve, not the
// server's default), and the recovery path rides the relay below.
func (d *decorator) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	mt, err := d.p.clampMaxTokens(d.p.sizeOf(req.Messages))
	if err != nil {
		// a request that still does not fit the window after compaction:
		// a pre-stream error, surfaced by the loop as a Fault — the -p
		// worker exits non-zero, the run record says fail (decision 8's
		// refuse-loud, not the floor-1 slow death).
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

// relay returns once the context is dead (a torn-down stream: closed
// without Done or Fault — the loop's existing read).
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
				// the recovery: compact (rewrite, event, reflection),
				// re-issue, relay. The Compacted event rides the channel
				// between the swallowed fault and the retry's first event
				// (decision 7), and the loop forwards it in its existing
				// default — byte-identical.
				ev2, compacted, cerr := d.p.compact(ctx)
				d.p.spendBudget() // the attempt is spent on this transcript state, compacted or not
				if cerr != nil {
					if ctx.Err() != nil {
						// the turn ctx died in the retry window (decision 7's
						// named shape): no Fault — the loop reads the torn-down
						// stream as its existing interrupt path.
						return
					}
					if !emit(core.Fault{Err: cerr}) {
						return
					}
					return
				}
				if !compacted {
					// nothing to drop (a single oversized last message):
					// surface, the retry cannot help — the named boundary
					// of 3, and never a silent loop.
					if !emit(e) {
						return
					}
					return
				}
				// the compact ran: its event rides the stream before the
				// retry decision (decision 5), so the recorder lands the
				// summary even when the retry then refuses loud.
				if !emit(ev2) {
					return
				}
				req2 := core.Request{Messages: d.p.assemble(d.p.s), Tools: req.Tools}
				mt, merr := d.p.clampMaxTokens(d.p.sizeOf(req2.Messages))
				if merr != nil {
					// the compact ran but the kept batch alone still overruns
					// the window (the tool result larger than the model can
					// hold): surface the refusal, so -p exits non-zero.
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
