package compact

import (
	"fmt"

	"github.com/mrsirg97-rgb/rig/core"
)

// Estimate is the raw stdlib estimate (decision 4): for each message, the
// bytes of Content plus Reasoning plus each tool call's name and args,
// divided by 4, rounded up — per message, so the ceiling does not
// undercount many small messages. Named approximate: it is a trigger,
// not an accounting.
func Estimate(msgs []core.Message) int {
	total := 0
	for _, m := range msgs {
		b := len(m.Content) + len(m.Reasoning)
		for _, c := range m.ToolCalls {
			b += len(c.Name) + len(c.Args)
		}
		total += (b + 3) / 4
	}
	return total
}

// sizeOf is the anchor-aware size (decision 4): the last message with a
// server count (ContextTokens > 0 — the L8 stamp) anchors, its count
// being the server's own number for everything up to and including it
// (the system prompt, the tool specs, the history — all exact), and the
// estimate covers only the messages after it — the delta — calibrated by
// the factor. No anchored message (a fresh or resumed session), the whole
// list is estimated, as before.
func (p *policy) sizeOf(msgs []core.Message) int {
	anchor, after := 0, msgs
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].ContextTokens > 0 {
			anchor = msgs[i].ContextTokens
			after = msgs[i+1:]
			break
		}
	}
	p.mu.Lock()
	f := p.factor
	p.mu.Unlock()
	return anchor + int(float64(Estimate(after))*f)
}

// clampMaxTokens is the row's max_tokens clamp (decisions 3, 8): the
// window minus the request's size, clamped to the row's MaxTokens. When
// the request still does not fit — the kept batch overruns the window,
// so `Window - size` is below the minimum (the smaller of Reserve/4 and
// a fixed 256) — it refuses loud, surfaced as a Fault so a -p worker
// exits non-zero and the run record says fail. The floor-1 reflex (a
// one-token answer that logs success) is the slow-death shape; this is
// its stop condition.
func (p *policy) clampMaxTokens(size int) (int, error) {
	budget := p.row.Window - size
	threshold := p.row.Reserve / 4
	if threshold > 256 {
		threshold = 256
	}
	if budget < threshold {
		return 0, fmt.Errorf("compact: %s: context exceeds the window even after compaction: request %d tokens against a window of %d (left %d < min %d); the kept batch is too large for the model",
			p.row.ID, size, p.row.Window, budget, threshold)
	}
	if p.row.MaxTokens < budget {
		return p.row.MaxTokens, nil
	}
	return budget, nil
}
