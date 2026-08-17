// Package guard bounds the repetition of fed-back failures. Every call
// executes exactly once, always; what is bounded is the model's
// re-issuance of a failing tool, keyed by tool name, per turn. After
// `limit` failures of one tool in a turn, the next issuance of that tool
// is refused without executing, naming the bound. The limit-th failure
// carries a note telling the model to change the call. Successful
// re-issuance (polling) never counts and stays unbounded. Sequential
// delivery means no locking.
package guard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

// bound is the retry guard's per-turn state: consecutive failures per tool
// name. It is one object on the widened seam — it wraps (the refusal and
// the note shape (content, err) on the way out) and observes (TurnStart
// clears the budget) — not two hooks.
type bound struct {
	limit  int
	counts map[string]int // tool name -> consecutive failures this turn
}

// Bound caps the repetition of failing calls of one tool in a turn, keyed
// by tool name. Named for what it does: it bounds, never retries.
func Bound(limit int) core.ToolMiddleware {
	if limit < 1 {
		limit = 1
	}
	return &bound{limit: limit, counts: map[string]int{}}
}

// Wrap is the seam. Keyed by name: drifting args cannot dodge the budget.
func (g *bound) Wrap(next core.ToolExec) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		if g.counts[call.Name] >= g.limit {
			msg := fmt.Sprintf("bound exhausted: %s has failed %d times; stop reissuing this call", call.Name, g.limit)
			return msg, errors.New(msg)
		}
		content, err := next(ctx, call)
		if err != nil {
			g.counts[call.Name]++
			if g.counts[call.Name] == g.limit {
				// the note: the bound teaches instead of merely stopping.
				// The tool's own content stays above it.
				note := fmt.Sprintf("[retry-guard] %s failed %d× in a row this turn. The error is above; read it and change the call, or stop calling this tool. Do not retry blindly.", call.Name, g.limit)
				if strings.TrimSpace(content) == "" {
					content = note
				} else {
					content = content + "\n" + note
				}
			}
		} else {
			delete(g.counts, call.Name) // success clears the count: the bound tracks streaks, not history
		}
		return content, err
	}
}

// TurnStart is the loop's fan-out (SPEC_HARDENING L6): a new user message
// is a new budget.
func (g *bound) TurnStart(ctx context.Context, s *core.Session) {
	g.counts = map[string]int{}
}
