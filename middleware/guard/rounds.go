package guard

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mrsirg97-rgb/rig/core"
)

// rounds is the per-turn round cap: it counts every call in a turn on the
// widened seam and, past the limit, refuses every further call without
// executing, in a teaching voice. The counter sits under a mutex because a
// concurrent batch (SPEC_EVT 6) calls the chain from many goroutines.
type rounds struct {
	mu    sync.Mutex
	limit int
	count int
}

func Rounds(n int) core.ToolMiddleware {
	if n < 1 {
		n = 1
	}
	return &rounds{limit: n}
}

func (r *rounds) Wrap(next core.ToolExec) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		r.mu.Lock()
		r.count++
		if r.count > r.limit {
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
