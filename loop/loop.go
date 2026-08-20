package loop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig"
	"github.com/mrsirg97-rgb/rig/core"
)

func Run(ctx context.Context, k *rig.Kernel) error {
	if k.Provider == nil {
		return errors.New("loop: kernel has no provider registered")
	}
	if k.Frontend == nil {
		return errors.New("loop: kernel has no frontend registered")
	}
	if k.Policy == nil {
		return errors.New("loop: kernel has no context policy registered")
	}
	if k.Session == nil {
		k.Session = core.NewSession()
	}

	tools := make(map[string]core.Tool, len(k.Tools))
	specs := make([]core.ToolSpec, 0, len(k.Tools))
	for _, t := range k.Tools {
		tools[t.Name()] = t
		specs = append(specs, core.ToolSpec{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
	}

	var exec core.ToolExec = directExec(tools)
	for _, mw := range k.Middleware {
		exec = mw.Wrap(exec)
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		turnCtx, turnCancel := context.WithCancel(ctx)

		userMsg, err := k.Frontend.Input(core.WithInterrupt(turnCtx, turnCancel))
		if err != nil {
			turnCancel()
			if ctx.Err() != nil || errors.Is(err, io.EOF) {
				return nil
			}
			if turnCtx.Err() != nil {
				continue
			}
			k.Frontend.Notify(core.Fault{Err: err})
			return err
		}
		if strings.TrimSpace(userMsg) == "" {
			turnCancel()
			continue
		}

		session := k.Session
		session.Append(core.Message{Role: core.RoleUser, Content: userMsg})

		for _, mw := range k.Middleware {
			if obs, ok := mw.(core.TurnObserver); ok {
				obs.TurnStart(turnCtx, session)
			}
		}

		reason := core.TurnOver
	turn:
		for {
			msgs, err := k.Policy.Assemble(turnCtx, session)
			if err != nil {
				if turnCtx.Err() != nil {
					reason = core.TurnInterrupt
					break turn
				}
				k.Frontend.Notify(core.Fault{Err: err})
				reason = core.TurnFault
				break turn
			}

			events, err := k.Provider.Stream(turnCtx, core.Request{Messages: msgs, Tools: specs})
			if err != nil {
				if turnCtx.Err() != nil {
					reason = core.TurnInterrupt
					break turn
				}
				k.Frontend.Notify(core.Fault{Err: err})
				reason = core.TurnFault
				break turn
			}

			var (
				text      strings.Builder
				reasoning strings.Builder
				calls     []core.ToolCall
				done      bool
				faulted   bool
				usage     core.Usage
			)
			for ev := range events {
				switch e := ev.(type) {
				case core.TextDelta:
					k.Frontend.Notify(ev)
					text.WriteString(e.Text)
				case core.ReasoningDelta:
					k.Frontend.Notify(ev)
					reasoning.WriteString(e.Text)
				case core.ToolCallEvent:
					k.Frontend.Notify(ev)
					calls = append(calls, e.Call)
				case core.Done:
					k.Frontend.Notify(ev)
					done = true
					usage = e.Usage
				case core.Fault:
					k.Frontend.Notify(ev)
					faulted = true
					if turnCtx.Err() != nil {
						reason = core.TurnInterrupt
					} else {
						reason = core.TurnFault
					}
				default:
					k.Frontend.Notify(ev)
				}
			}

			switch {
			case faulted:
				break turn
			case !done:
				if ctx.Err() != nil {
					turnCancel()
					return nil
				}
				if turnCtx.Err() != nil {
					reason = core.TurnInterrupt
					break turn
				}
				k.Frontend.Notify(core.Fault{Err: errors.New("loop: provider closed the stream without Done or Fault")})
				turnCancel()
				k.Frontend.Notify(core.TurnEnd{Reason: core.TurnFault})
				return errors.New("loop: provider closed the stream without Done or Fault")
			case len(calls) == 0:
				session.Append(core.Message{Role: core.RoleAssistant, Content: text.String(), Reasoning: reasoning.String(), ContextTokens: usage.Prompt + usage.Completion})
				break turn
			default:
				session.Append(core.Message{
					Role:          core.RoleAssistant,
					Content:       text.String(),
					Reasoning:     reasoning.String(),
					ToolCalls:     calls,
					ContextTokens: usage.Prompt + usage.Completion,
				})
				for _, call := range calls {
					k.Frontend.Notify(core.ToolStart{Call: call})
					start := time.Now()
					content, execErr := exec(core.WithSession(turnCtx, session), call)
					if execErr != nil && content == "" {
						content = execErr.Error()
					}
					k.Frontend.Notify(core.ToolResult{ID: call.ID, Content: content, Err: execErr, Duration: time.Since(start)})
					session.Append(core.Message{
						Role:    core.RoleTool,
						ToolID:  call.ID,
						Content: content,
					})
				}
				continue
			}
		}

		turnCancel()
		k.Frontend.Notify(core.TurnEnd{Reason: reason})
	}
}

func directExec(tools map[string]core.Tool) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		t, ok := tools[call.Name]
		if !ok {
			msg := fmt.Sprintf("unknown tool: %s", call.Name)
			return msg, errors.New(msg)
		}
		return t.Exec(ctx, call.Args)
	}
}
