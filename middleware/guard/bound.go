package guard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/rig/core"
)

type bound struct {
	mu         sync.Mutex
	limit      int
	counts     map[string]int
	lastFailed map[string]string
}

func Bound(limit int) core.ToolMiddleware {
	if limit < 1 {
		limit = 1
	}
	return &bound{limit: limit, counts: map[string]int{}, lastFailed: map[string]string{}}
}

func (g *bound) Wrap(next core.ToolExec) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		g.mu.Lock()
		if g.lastFailed[call.Name] != string(call.Args) {
			g.counts[call.Name] = 0
		}
		if g.counts[call.Name] >= g.limit {
			g.mu.Unlock()
			msg := fmt.Sprintf("bound exhausted: %s has failed %d times; stop reissuing this call", call.Name, g.limit)
			return msg, errors.New(msg)
		}
		g.mu.Unlock()
		content, err := next(ctx, call)
		g.mu.Lock()
		defer g.mu.Unlock()
		if err != nil {
			if g.lastFailed[call.Name] == string(call.Args) {
				g.counts[call.Name]++
			} else {
				g.counts[call.Name] = 1
				g.lastFailed[call.Name] = string(call.Args)
			}
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
			delete(g.lastFailed, call.Name)
		}
		return content, err
	}
}

func (g *bound) TurnStart(ctx context.Context, s *core.Session) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.counts = map[string]int{}
	g.lastFailed = map[string]string{}
}
