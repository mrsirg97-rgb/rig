package compact

import (
	"fmt"

	"github.com/mrsirg97-rgb/rig/core"
)

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
