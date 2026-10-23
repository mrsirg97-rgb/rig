# loop

## What it is

The one place turn ordering is written down, and, since SPEC_EVT 2b,
the `evt` engine's consumer: every step of a turn is a closure run on
the loop goroutine; the Frontend's `Input`, the provider's stream, and
each tool's execution are producer goroutines that post. Per turn:

    awaiting_input -> awaiting_model -> executing_tools
    -> awaiting_model -> ... -> done

Between `executing_tools` and the next `awaiting_model` the loop asks the
frontend's `core.Inbox`, when it is one, for the returns that came back
while the step ran, and appends the block as a user message (2.18.2,
SPEC_DELEGATE 8): a turn that keeps calling tools reads its background
results at the next step, not when it finally ends. A cancelled turn
does not ask; the returns wait for `Input`.

Faults abort the turn and return to awaiting_input; the loop never retries
silently. Cancellation is ctx at every await: a run-context cancel ends
the session at the boundary, clean.

## What it includes

- `Run(ctx, k rig.Kernel)`: wires the tools and the chain, posts the
  first prompt, reads the provider, policy, session and frontend through
  the kernel's seat at every use (so a `/models` or `/new` swap lands on
  the next read), and blocks in the engine until a handler stops it (EOF,
  a dead run context at a boundary, the provider-closed fault). A
  concrete function by design: making the loop pluggable would make
  ordering emergent and undebuggable.
- `run` / `turn`: the consumer's state: the engine, the chain, the
  specs, the live turn (its context, accumulators, batch, cursor); the
  handlers `prompt`, `input`, `model`, `streamEvent`, `streamEnd`,
  `toolDone`, `advance`, `end`, `stop`. Priorities are the kernel's
  (`rig.PriorityInput` 90, `PriorityDelegate` 70 for a delegate's
  return, `PriorityWrite` 60, `PriorityRead` 50, `PriorityStream` and
  `PriorityTool` 50, `PriorityFleet` 30 below them for the room, and
  `PriorityReview` 10 below that for the decision review, SPEC_EVT 8);
  the engine is
  the kernel's when it has one (`rig.WithEngine`), minted here
  otherwise.
- `directExec(tools)`: the innermost exec: lookup and run.
- `batch` (`batch.go`): the tool-call batch: the kernel's priority
  function mints one priority per call — a write at 60 runs its exec
  inline and chains its own completion through the engine, so the
  writes serialize in the drain in index order; a read at 50 hands its
  exec to a semaphore-bounded goroutine (at most `Parallel`, default 8 —
  the ceiling on the reads' concurrent goroutines while the writes run
  inline, so a long read run cannot fork execs without bound; the 8 came
  with the batch itself as SPEC_EVT 2a's construction default, never
  measured against a workload, and the kernel's const is the shared
  spelling since the decide fan-out and the pack scorer took the same
  bound)
  and posts its completion at its own priority, so the reads overlap.
  The results emit in call order and the transcript does not move
  (SPEC_WRITES 6).

## How it is consumed

- `Run` is called with the assembled kernel: nil `Provider`/`Frontend`/
  `Policy` refuse loud. A nil `Session` is minted fresh.
- The execution chain composes in listed order: first-listed is
  innermost, which inverts the http-Handler convention deliberately:
  `WithMiddleware(perm, guard)` reads `guard(perm(...))`, not
  `perm(guard(...))`.
- Each turn carries a per-turn context (child of the run's), cancelled on
  every turn exit and threaded onto the Input ctx as the interrupt handle
  (`core.WithInterrupt`/`core.InterruptFrom`): a dead turn context with a
  live run context is an interrupt.

## Turn mechanics (the L-numbered behaviors)

- **L1**: per-turn context + interrupt handle threaded under a typed key.
- **L2**: turn death at the prompt is an interrupt, not a fault: the loop
  re-enters awaiting_input.
- **L3**: an interrupted teardown (stream dead + turn ctx dead) breaks the
  turn; the run continues. The reply the interrupt cut is appended as the
  assistant's message first (text and reasoning, never its calls), so the
  transcript only grows at its end (2.17.1).
- **L4**: the tool-execution bracket wraps the whole middleware chain; the
  `ToolResult` carries the guarded result, and the loop measures duration.
- **L5**: `ReasoningDelta` is forwarded and accumulated.
- **L6**: the turn fan-out (`TurnStart`) before the first `Assemble`.
- **L7**: the boundary `TurnEnd{Reason}` event, after the turn's last other
  event.
- **L8**: the `ContextTokens` stamp (`usage.Prompt + usage.Completion`)
  rides the three assistant-append branches — the interrupt's included
  (2.17.1); 0 when unreported.
- **L9**: the batch: writes serialize inline in the drain, reads
  overlap, emission and the transcript are in call order, each
  `ToolResult` carries its own duration. A nil priority function makes
  every call a write — the sequential loop, byte-identical.

## Gotchas

- A tool result whose exec failed keeps its content and gains the exec
  error on its own line unless the content already ends with it (the
  chain's refusals return the same string as both, and the model sees
  it once; an empty result carries the error alone): the model sees
  both the output and the reason, and a child that never ran can never
  reach it as a bare cwd line.
- Empties never pollute the transcript: a blank user message re-enters
  awaiting_input, and a completion with no text, no reasoning, and no
  calls appends no assistant row (a billed-but-empty completion drops
  its usage stamp with it).
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
- A run-context cancel observed at a prompt, input, or stream boundary
  ends the run, not a turn, and emits no `TurnEnd`; observed at the
  `Assemble`/`Stream` seams the run's death is the turn's (the turn ctx
  is the child), classified `TurnInterrupt` and emitting
  `TurnEnd{interrupt}` before the run stops. Every turn exit emits one.
- Every event captures its turn and checks it is the live one: a stale
  completion or stream event is ignored by name.
- The engine runs on a background context: the run ends from a handler
  at a boundary (the sequential loop's rule), never mid-drain.
- `Assemble` (a compaction inside it) and the `Stream` call block the
  consumer, as before; the interrupt reaches them through the turn
  context, not an event.
- Inside a concurrent run the middleware chain and the tools run off the
  loop goroutine: a middleware with state locks it (the guard does), a
  tool that writes the session locks its writes (the file tool does),
  and every `Notify` and `Append` stays on the loop goroutine. Execution
  order is never rearranged; only emission is ordered, and a call with
  effects is a write at 60, serialized inline in the drain.
- The batch feeds the engine, and the engine sorts (2.17.0, SPEC_WRITES):
  the kernel's priority function replaces the concurrency flag — a write
  is 60, a read is 50. A write's closure runs its exec inline, so the
  writes serialize in the drain in index order and chain through their
  own completions; a read's closure hands its exec to a semaphore-bounded
  goroutine, so the reads overlap; the engine's drain is highest-first,
  so the reads wait behind every write. The results still emit in call
  order: the transcript does not move.
