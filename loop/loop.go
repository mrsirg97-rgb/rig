// Package loop is the one place turn ordering is written down. Per turn:
//
//	awaiting_input -> awaiting_model -> executing_tools
//	-> awaiting_model -> ... -> done
//
// Faults abort the turn and return to awaiting_input; the loop never
// retries silently, and cancellation is ctx at every await.
package loop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mrsirg97-rgb/looper"
	"github.com/mrsirg97-rgb/looper/core"
)

// Run drives turns until the frontend dries up or ctx cancels. It is a
// concrete function by design: making the loop pluggable would make
// ordering emergent and undebuggable.
func Run(ctx context.Context, k *looper.Kernel) error {
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
		specs = append(specs, core.ToolSpec{Name: t.Name(), Schema: t.Schema()})
	}

	// The execution chain composes in listed order; first-listed is
	// innermost, so a listed guard bounds what it wraps.
	var exec core.ToolExec = directExec(tools)
	for _, mw := range k.Middleware {
		exec = mw(exec)
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil // cancellation at the boundary is clean
		}

		// awaiting_input: the frontend blocks for one user message.
		userMsg, err := k.Frontend.Input(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) {
				return nil
			}
			k.Frontend.Notify(core.Fault{Err: err})
			return err
		}
		if strings.TrimSpace(userMsg) == "" {
			continue // never pollute the transcript with empties
		}

		session := k.Session
		session.Append(core.Message{Role: core.RoleUser, Content: userMsg})

	turn:
		for {
			// awaiting_model.
			msgs, err := k.Policy.Assemble(ctx, session)
			if err != nil {
				return fmt.Errorf("loop: assemble: %w", err)
			}

			events, err := k.Provider.Stream(ctx, core.Request{Messages: msgs, Tools: specs})
			if err != nil {
				// transport error: surfaced, turn aborted, session intact.
				k.Frontend.Notify(core.Fault{Err: err})
				break turn
			}

			var (
				text    strings.Builder
				calls   []core.ToolCall
				done    bool
				faulted bool
			)
			for ev := range events {
				switch e := ev.(type) {
				case core.TextDelta:
					k.Frontend.Notify(ev)
					text.WriteString(e.Text)
				case core.ToolCallEvent:
					k.Frontend.Notify(ev)
					calls = append(calls, e.Call)
				case core.Done:
					k.Frontend.Notify(ev)
					done = true
				case core.Fault:
					k.Frontend.Notify(ev)
					faulted = true
				}
			}

			switch {
			case faulted:
				// session preserved up to the last complete message;
				// back to awaiting_input.
				break turn
			case !done:
				if ctx.Err() != nil {
					return nil // cancelled teardown, clean
				}
				return errors.New("loop: provider closed the stream without Done or Fault")
			case len(calls) == 0:
				// turn over.
				session.Append(core.Message{Role: core.RoleAssistant, Content: text.String()})
				break turn
			default:
				// executing_tools: sequential, in order, through the chain.
				session.Append(core.Message{
					Role:      core.RoleAssistant,
					Content:   text.String(),
					ToolCalls: calls,
				})
				for _, call := range calls {
					content, execErr := exec(core.WithSession(ctx, session), call)
					if execErr != nil && content == "" {
						content = execErr.Error()
					}
					session.Append(core.Message{
						Role:    core.RoleTool,
						ToolID:  call.ID,
						Content: content,
					})
				}
				continue // back to awaiting_model with the result transcript
			}
		}
	}
}

// directExec is the innermost exec: lookup and run. Unknown tools and tool
// failures come back as a fed-back string plus an error, so the chain can
// bound the repetition and the loop can feed the string back to the model.
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
