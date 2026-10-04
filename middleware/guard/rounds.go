package guard

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
)

type rounds struct {
	mu     sync.Mutex
	limit  int
	count  int
	record decision.Recorder
}

func Rounds(n int, rec ...decision.Recorder) core.ToolMiddleware {
	if n < 0 {
		n = 0
	}
	r := &rounds{limit: n}
	if len(rec) > 0 {
		r.record = rec[0]
	}
	return r
}

func (r *rounds) Wrap(next core.ToolExec) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		r.mu.Lock()
		r.count++
		if r.limit > 0 && r.count > r.limit {
			r.mu.Unlock()
			if r.record != nil {
				r.record.Record(ctx, decision.Final{
					Site:     decision.SiteGuard,
					State:    call.Name,
					Question: decision.Binary("call", "make another tool call this turn?"),
					Answer:   "no",
					Decider:  decision.SiteGuard,
				})
			}
			msg := fmt.Sprintf("round cap: %d tool calls is this turn's limit; stop calling tools and report, or ask the operator to raise it", r.limit)
			return msg, errors.New(msg)
		}
		r.mu.Unlock()
		return next(ctx, call)
	}
}

func (r *rounds) TurnStart(ctx context.Context, s *core.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count = 0
}
