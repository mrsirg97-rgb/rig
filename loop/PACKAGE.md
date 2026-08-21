# loop

## What it is

The one place turn ordering is written down. Per turn:

    awaiting_input -> awaiting_model -> executing_tools
    -> awaiting_model -> ... -> done

Faults abort the turn and return to awaiting_input; the loop never retries
silently. Cancellation is ctx at every await: a run-context cancel ends
the session at the boundary, clean.

## What it includes

- `Run(ctx, k *rig.Kernel)` — drives turns until the frontend dries up or
  ctx cancels. A concrete function by design: making the loop pluggable
  would make ordering emergent and undebuggable.
- `directExec(tools)` — the innermost exec: lookup and run.
- `batch` (`batch.go`, SPEC_EVT 2a) — the tool-call batch: runs of
  calls the kernel's `Concurrent` predicate admits are dispatched as
  goroutines (at most `Parallel`, default 8); a refused call is a
  barrier; `result(i)` dispatches lazily and waits in call order.

## How it is consumed

- `Run` is called with the assembled kernel; nil `Provider`/`Frontend`/
  `Policy` refuse loud. A nil `Session` is minted fresh.
- The execution chain composes in listed order; first-listed is
  innermost, which inverts the http-Handler convention deliberately:
  `WithMiddleware(perm, guard)` reads `guard(perm(...))`, not
  `perm(guard(...))`.
- Each turn carries a per-turn context (child of the run's), cancelled on
  every turn exit and threaded onto the Input ctx as the interrupt handle
  (`core.WithInterrupt`/`core.InterruptFrom`): a dead turn context with a
  live run context is an interrupt.

## Turn mechanics (the L-numbered behaviors)

- **L1** — per-turn context + interrupt handle threaded under a typed key.
- **L2** — turn death at the prompt is an interrupt, not a fault: the loop
  re-enters awaiting_input.
- **L3** — an interrupted teardown (stream dead + turn ctx dead) breaks the
  turn; the run continues.
- **L4** — the tool-execution bracket wraps the whole middleware chain; the
  `ToolResult` carries the guarded result, and the loop measures duration.
- **L5** — `ReasoningDelta` is forwarded and accumulated.
- **L6** — the turn fan-out (`TurnStart`) before the first `Assemble`.
- **L7** — the boundary `TurnEnd{Reason}` event, after the turn's last other
  event.
- **L8** — the `ContextTokens` stamp (`usage.Prompt + usage.Completion`)
  rides both assistant-append branches; 0 when unreported.
- **L9** — the batch (SPEC_EVT 2a): admitted calls overlap, barriers
  run alone in order, emission and the transcript are in call order,
  each `ToolResult` carries its own duration. A nil predicate is the
  sequential loop, byte-identical.

## Gotchas

- Empties never pollute the transcript: a blank user message re-enters
  awaiting_input.
- A dead turn ctx at the `Assemble`/`Stream` seam is the user's interrupt
  (or the session's end), not a provider fault: no `Fault` row, the run
  re-prompts.
- A failing `Assemble` or a transport error is surfaced as `Fault`, the
  turn aborted, the session intact, back to awaiting_input. A failing
  policy must not be able to kill the REPL.
- A closed stream without `Done` or `Fault` with both contexts alive is a
  provider bug: loud `Fault`, turn ended as `TurnFault`, and the run
  returns the error.
- Unknown tools and tool failures come back as a fed-back string plus an
  error, so the chain can bound the repetition and the loop feeds the
  string back to the model.
- The compat rule (events are added, never changed): events the loop does
  not name forward untouched; the Frontend tolerates what it does not know.
- A run-context cancel ends the run, not a turn, and emits no `TurnEnd`;
  every turn exit emits one.
- Inside a concurrent run the middleware chain and the tools run off the
  loop goroutine: a middleware with state locks it (the guard does), a
  tool that writes the session locks its writes (the file tool does),
  and every `Notify` and `Append` stays on the loop goroutine. Execution
  order is never rearranged — only emission is ordered — so a call with
  effects must not be admitted by the predicate.
