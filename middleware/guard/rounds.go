package guard

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mrsirg97-rgb/rig/core"
)

type rounds struct {
	mu    sync.Mutex
	limit int
	count int
}

func Rounds(n int) core.ToolMiddleware {
	if n < 0 {
		n = 0
	}
	return &rounds{limit: n}
}

func (r *rounds) Wrap(next core.ToolExec) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		r.mu.Lock()
		r.count++
		if r.limit > 0 && r.count > r.limit {
			r.mu.Unlock()
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
