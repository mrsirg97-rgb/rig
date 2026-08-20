package guard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

type bound struct {
	limit  int
	counts map[string]int
}

func Bound(limit int) core.ToolMiddleware {
	if limit < 1 {
		limit = 1
	}
	return &bound{limit: limit, counts: map[string]int{}}
}

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
				note := fmt.Sprintf("[retry-guard] %s failed %d× in a row this turn. The error is above; read it and change the call, or stop calling this tool. Do not retry blindly.", call.Name, g.limit)
				if strings.TrimSpace(content) == "" {
					content = note
				} else {
					content = content + "\n" + note
				}
			}
		} else {
			delete(g.counts, call.Name)
		}
		return content, err
	}
}

func (g *bound) TurnStart(ctx context.Context, s *core.Session) {
	g.counts = map[string]int{}
}
