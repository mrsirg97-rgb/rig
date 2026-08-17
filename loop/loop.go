// Package loop is the one place turn ordering is written down. Per turn:
//
//	awaiting_input -> awaiting_model -> executing_tools
//	-> awaiting_model -> ... -> done
//
// Faults abort the turn and return to awaiting_input; the loop never
// retries silently. Cancellation is ctx at every await: a run-context
// cancel ends the session at the boundary, clean. Each turn also carries a
// per-turn context (child of the run's), cancelled on every turn exit and
// threaded onto the Input ctx as the interrupt handle
// (core.WithInterrupt/core.InterruptFrom): a dead turn context with a live
// run context is an interrupt — at the prompt the loop re-enters
// awaiting_input (no Fault), mid-stream it breaks the turn. Every turn
// exit emits TurnEnd{Reason}; the compat rule (events are added, never
// changed) makes unknown events noise, not a misread.
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

// Run drives turns until the frontend dries up or ctx cancels. It is a
// concrete function by design: making the loop pluggable would make
// ordering emergent and undebuggable.
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

	// The execution chain composes in listed order; first-listed is
	// innermost, so a listed guard bounds what it wraps. This inverts the
	// http-Handler convention deliberately: the spec's wiring example pairs a
	// denial innermost with a guard outermost, which is what lets the guard
	// bound the repetition of denials. Read WithMiddleware(perm, guard) as
	// guard(perm(...)), not perm(guard(...)).
	var exec core.ToolExec = directExec(tools)
	for _, mw := range k.Middleware {
		exec = mw.Wrap(exec)
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil // cancellation at the boundary is clean
		}

		// L1: the per-turn context, and the interrupt handle threaded onto
		// the Input ctx under a typed key.
		turnCtx, turnCancel := context.WithCancel(ctx)

		// awaiting_input: the frontend blocks for one user message.
		userMsg, err := k.Frontend.Input(core.WithInterrupt(turnCtx, turnCancel))
		if err != nil {
			turnCancel()
			if ctx.Err() != nil || errors.Is(err, io.EOF) {
				return nil
			}
			// L2: turn death at the prompt is an interrupt, not a fault:
			// the loop re-enters awaiting_input.
			if turnCtx.Err() != nil {
				continue
			}
			k.Frontend.Notify(core.Fault{Err: err})
			return err
		}
		if strings.TrimSpace(userMsg) == "" {
			turnCancel()
			continue // never pollute the transcript with empties
		}

		session := k.Session
		session.Append(core.Message{Role: core.RoleUser, Content: userMsg})

		// L6: the turn fan-out, before the first Assemble.
		for _, mw := range k.Middleware {
			if obs, ok := mw.(core.TurnObserver); ok {
				obs.TurnStart(turnCtx, session)
			}
		}

		reason := core.TurnOver
	turn:
		for {
			// awaiting_model.
			msgs, err := k.Policy.Assemble(turnCtx, session)
			if err != nil {
				if turnCtx.Err() != nil {
					// a dead turn ctx at the seam is the user's interrupt (or
					// the session's end), not a provider fault: no Fault row —
					// the model never started — and the run re-prompts.
					reason = core.TurnInterrupt
					break turn
				}
				// treated like a transport fault: surfaced, turn aborted, session
				// intact, back to awaiting_input. A failing policy must not be
				// able to kill the REPL.
				k.Frontend.Notify(core.Fault{Err: err})
				reason = core.TurnFault
				break turn
			}

			events, err := k.Provider.Stream(turnCtx, core.Request{Messages: msgs, Tools: specs})
			if err != nil {
				if turnCtx.Err() != nil {
					// the stream's own error on a dead turn ctx reads the same:
					// a provider that checks its context at call time reports
					// the steer, not a fault.
					reason = core.TurnInterrupt
					break turn
				}
				// transport error: surfaced, turn aborted, session intact.
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
				usage     core.Usage // L8: the stamp's source (SPEC_COMPACT 4)
			)
			for ev := range events {
				switch e := ev.(type) {
				case core.TextDelta:
					k.Frontend.Notify(ev)
					text.WriteString(e.Text)
				case core.ReasoningDelta:
					// L5: forwarded and accumulated.
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
						// a cancelled turn reads the fault the same way:
						// the steering cancel is what tore the stream down.
						reason = core.TurnInterrupt
					} else {
						reason = core.TurnFault
					}
				default:
					// the compat rule: events the loop does not name forward
					// untouched; the Frontend tolerates what it does not know.
					k.Frontend.Notify(ev)
				}
			}

			switch {
			case faulted:
				// session preserved up to the last complete message;
				// back to awaiting_input.
				break turn
			case !done:
				if ctx.Err() != nil {
					turnCancel()
					return nil // run-context teardown, clean: not a turn
				}
				if turnCtx.Err() != nil {
					// L3: an interrupted teardown reads the same and breaks
					// the turn; the run continues.
					reason = core.TurnInterrupt
					break turn
				}
				// both contexts alive: a provider bug, loud, as before —
				// and the turn it ended is a fault.
				k.Frontend.Notify(core.Fault{Err: errors.New("loop: provider closed the stream without Done or Fault")})
				turnCancel()
				k.Frontend.Notify(core.TurnEnd{Reason: core.TurnFault})
				return errors.New("loop: provider closed the stream without Done or Fault")
			case len(calls) == 0:
				// turn over; the thinking that led to the answer survives. L8:
				// the ContextTokens stamp (SPEC_COMPACT 4's anchor) rides
				// Done.Usage's prompt+completion — the server's own count at
				// the moment this message completed; 0 when unreported.
				session.Append(core.Message{Role: core.RoleAssistant, Content: text.String(), Reasoning: reasoning.String(), ContextTokens: usage.Prompt + usage.Completion})
				break turn
			default:
				// executing_tools: sequential, in order, through the chain. L8:
				// the same ContextTokens stamp as the no-calls branch.
				session.Append(core.Message{
					Role:          core.RoleAssistant,
					Content:       text.String(),
					Reasoning:     reasoning.String(),
					ToolCalls:     calls,
					ContextTokens: usage.Prompt + usage.Completion,
				})
				for _, call := range calls {
					// L4: the bracket wraps the whole middleware chain; the
					// ToolResult carries the guarded result, exactly what the
					// session gets, and the loop measures the duration.
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
				continue // back to awaiting_model with the result transcript
			}
		}

		// L1: cancelled on every turn exit. L7: the boundary event, after
		// the turn's last other event.
		turnCancel()
		k.Frontend.Notify(core.TurnEnd{Reason: reason})
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
