package guard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

type bound struct {
	limit      int
	counts     map[string]int
	lastFailed map[string]string // tool name -> the args of the last failure
}

func Bound(limit int) core.ToolMiddleware {
	if limit < 1 {
		limit = 1
	}
	return &bound{limit: limit, counts: map[string]int{}, lastFailed: map[string]string{}}
}

func (g *bound) Wrap(next core.ToolExec) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		// a corrected call (args differing from the last failed args) resets
		// the streak before the guard check, so the "change the call" teaching
		// never blocks the changed call.
		if g.lastFailed[call.Name] != string(call.Args) {
			g.counts[call.Name] = 0
		}
		if g.counts[call.Name] >= g.limit {
			msg := fmt.Sprintf("bound exhausted: %s has failed %d times; stop reissuing this call", call.Name, g.limit)
			return msg, errors.New(msg)
		}
		content, err := next(ctx, call)
		if err != nil {
			// the bound strikes identical retries only: same args as the last
			// failure keep counting; differing args start a fresh streak.
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
	g.counts = map[string]int{}
	g.lastFailed = map[string]string{}
}
