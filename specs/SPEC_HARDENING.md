# looper: runtime hardening (roadmap deliverable 7)

The seams and events required for everything after this to be leaf work. The
loop change made once, deliberately, while the runtime is still the focus.
After this: compaction is a `ContextPolicy` (8), commands are a Frontend
verb (9), the TUI is a `Frontend` registration (10), and the loop is
byte-identical to the end of this deliverable.

This spec decides eight things, each with its reason. It names every change
to `loop/loop.go`. It does not change the loop's state machine:
`awaiting_input -> awaiting_model -> executing_tools -> awaiting_model -> ...
-> done`. It adds events to the stream, a turn context inside the states, and
one fan-out call at the turn boundary.

## goals

- The Frontend stops being blind: tool execution is visible as events
  (`ToolStart`, `ToolResult`), so pane's `● tool · detail` rows with
  durations are renderable from events alone.
- Reasoning is round-tripped: the model's thinking streams
  (`ReasoningDelta`), survives in the transcript (`Message.Reasoning`), and
  goes back over the wire (`reasoning_content`), so interleaved-thinking
  tool turns keep their context and the user does not see silence.
- Usage carries cache-read and cache-write; per-turn totals are computable
  by the Frontend from the events it already receives.
- Steering: input during a turn is queued and delivered on the next Input;
  an interrupt is a turn cancel plus re-entry into awaiting_input.
- Session resume: `[]core.Message` (and `Session.Files`) rebuilt from the
  state rows, one flag at the root.
- One agent-side extension mechanism, widened from the existing middleware
  seam: turn-boundary observation and system-prompt guidelines for any
  participant.
- The retry guard matches pane: bound keyed by tool name, cleared per turn.
- The compat rule is written down: events are added, never changed; a
  Frontend must tolerate events it does not know. This is what lets 1.0
  freeze.

## non-goals

- No TUI, no rendering kit, no styling: the CLI renders plain text lines.
- No compaction (that is 8; the `ContextPolicy` seam is untouched).
- No commands (that is 9; the `steer` command of 9 is this steering path
  made a verb).
- No new dependencies. `core/` and `loop/` stay standard library.
- No change to `-p` one-shot or `run-job` semantics: the worker's stdout is
  the assistant text and faults, byte-identical; one-shot ignores the new
  events.
- No new loop states, no parallel tool execution, no mailbox (single input
  slot), no config files.
- No change to the state schema (SPEC_STATE already designed this day:
  `messages.reasoning`, `usage.cache_read/cache_write`,
  `tool_calls.started_at/ended_at`).

## layout

No new packages. The change lands in:

```
core/           Event vocabulary +3 loop events (incl. TurnEnd), +1
                provider event; Message.Reasoning; Usage +cache fields;
                ToolMiddleware widened (function -> interface) with two
                optional capabilities; interrupt.go (WithInterrupt /
                InterruptFrom, the WithSession pattern); TestEvent test seam
loop/loop.go    the named changes L1-L7 below, nothing else
provider/openai reasoning round-trip, cache usage fields
middleware/guard name keying, per-turn clear, the retry note
frontend/cli    the steering slot, the execution bracket, the usage line
store/state     recorder taps move to the loop's events; files upsert;
                the Resume projection
kernel.go       Kernel.Middleware type follows the seam
cmd/looper      wire() drops the observe tap; -resume flag; guidelines
                collected into the system prompt
```

The design test holds: an extension after this is one file and one
registration line, exactly as before.

## the loop changes (named)

The state machine is unchanged. The changes:

- **L1, the per-turn context.** Each turn gets `turnCtx, turnCancel :=
  context.WithCancel(ctx)` created before `Input` and used for `Input`,
  `Assemble`, `Stream`, and every `exec`. `turnCancel()` is called on every
  turn exit path (fault, turn over, turn death, and the two clean returns).
  The run context keeps its meaning: a run-context cancel ends the session
  at the boundary, clean, as today. The loop threads the turn's cancel onto
  the ctx it hands to `Input` under a typed key, `core.WithInterrupt(ctx,
  cancel)` / `core.InterruptFrom(ctx)` (new `core/interrupt.go`, the
  `WithSession` pattern already in core): a Frontend cannot cancel a parent
  from a child reference, so the CancelFunc is the interrupt handle, not
  the context. The `Frontend` interface stays frozen-shaped (same two
  methods); the handle rides the ctx, exactly as the session does.
- **L2, turn death at the prompt.** `Input` returns an error, the run
  context is alive, and `turnCtx` is dead: this is an interrupt at the
  prompt, not a fault. The loop re-enters awaiting_input (no `Fault`
  emitted). Today this path emits `Fault` and returns the error; a run-
  context cancel or EOF still ends the run, as today.
- **L3, turn death mid-stream.** The stream closes without `Done` or
  `Fault` and the run context is alive: today this is a loud provider-bug
  error. Under L1 it is also how an interrupted teardown reads, so the loop
  breaks the turn (re-enters awaiting_input) when `turnCtx` is dead, and
  still fails loudly when both contexts are alive.
- **L4, tool-exec events.** Around each `exec` call the loop emits
  `ToolStart{Call}` before and `ToolResult{ID, Content, Err, Duration}`
  after, forwarding both to `Frontend.Notify`. The bracket wraps the whole
  middleware chain: `ToolResult` carries the guarded result, exactly what
  the loop appends to the session. `Duration` is measured by the loop.
- **L5, reasoning accumulation.** The stream switch gains
  `ReasoningDelta`: forwarded to `Notify` and accumulated; the assistant
  message appended in both the text-only and tool-call branches carries
  `Reasoning`.
- **L6, the turn fan-out.** After the user message is appended and before
  the first `Assemble`, the loop calls `TurnStart(ctx, session)` on every
  registered middleware that implements it.
- **L7, the turn-exit event.** The loop emits `TurnEnd{Reason}` on every
  turn exit inside the run: `over` (the turn completed), `fault` (a Fault
  or transport error crossed it), `interrupt` (the turn context died).
  It is forwarded to `Frontend.Notify` after the turn's last other event.
  A run-context cancel ends the run, not a turn, and does not emit it.
  The recorder's rule follows (decision 4): an unlanded partial at any
  `TurnEnd` is a partial and is discarded.

`directExec`, the fault paths, the boundary checks, and the duplicate-name
panic are unchanged.

## decisions

### 1. Tool-exec events

```go
type ToolStart struct{ Call ToolCall }
type ToolResult struct {
    ID       string
    Content  string
    Err      error // nil = success; the guarded result, named
    Duration time.Duration
}
```

Both are `Event`s, emitted by the loop (not the provider) and forwarded to
`Frontend.Notify` like the model's stream. The vocabulary's third loop
event, `TurnEnd{Reason}`, closes every turn (L7):

```go
type TurnReason string

const (
    TurnOver      TurnReason = "over"      // the turn completed
    TurnFault     TurnReason = "fault"     // a Fault or transport error crossed it
    TurnInterrupt TurnReason = "interrupt" // the turn context died (steering)
)

type TurnEnd struct{ Reason TurnReason }
```

Event order for a tool turn: `TextDelta* / ReasoningDelta* /
ToolCallEvent* / Done`, then per call, in order: `ToolStart / ToolResult`,
then the next model call's events. A turn closes with `TurnEnd` after its
last other event, whatever that last event was.

Why a loop event and not a provider event: the provider announces a call
(`ToolCallEvent`, during the stream); the loop executes it. The execution
is what the Frontend renders (`● bash` on start, outcome and duration on
result); pane's `● tool · detail` rows are execution rows, and only the
loop knows execution happened. Keeping the two distinct also lets the TUI
(10) show intent and execution separately if it wants to.

Consequences, all named:

- The state recorder's tool-result tap moves from the `Observe`
  middleware (the chain) to the `ToolResult` event (the stream). Same
  content: the bracket wraps the whole chain, so the event carries the
  guarded result the tap saw. `Observe` is retired, the root's chain
  becomes `[perm, guard]`, and `wire()` drops the `observe` parameter.
  SPEC_STATE already names this: "When deliverable 7 lands tool events and
  reasoning, the recorder switches sources; its schema does not change."
  No SPEC_STATE diff is owed.
- The CLI renders the execution bracket: `● <name>` on `ToolStart`,
  `<name> <outcome> <duration>` on `ToolResult` (pane's `○ ◐ ● ✕` minimal:
  `✓` success, `✕` failure). The `ToolCallEvent` line the CLI prints
  today (`[call] name`) is dropped: the bracket replaces it, and the model's
  intent and its execution are adjacent. `ToolCallEvent` stays in the
  vocabulary (provider-stream event; the recorder still sources pending
  calls from it).
- `ToolResult.Err` is the fed-back failure marker (non-nil = the result
  string is an error the model sees), so a Frontend or recorder can tell
  outcomes apart without parsing the string.

### 2. Reasoning round-trip

```go
type ReasoningDelta struct{ Text string } // provider stream: the model's thinking
// Message gains:
Reasoning string // assistant turns only; empty when the model did not think
```

The openai adapter maps `delta.reasoning_content` to `ReasoningDelta` in
stream order (llama.cpp emits the thinking before the answer) and carries
assistant `Message.Reasoning` back over the wire as `reasoning_content`.
The loop accumulates and appends (L5). The recorder writes the existing
`messages.reasoning` column (the API already takes the parameter; it passes
`nil` today).

Grounded live against the worker model (Qwen3.8-27B over the swap): the
stream carries `reasoning_content` deltas, and a prior assistant message
carrying `reasoning_content` raises prompt tokens (522 to 647 for a ~570
char block), so the chat template renders it back: the round-trip is real,
and interleaved-thinking tool turns (thinking that led to calls) are no
longer lost.

Why a field on `Message` and not an event-only thing: the transcript is the
source of truth and the wire is a projection of it. Reasoning that does not
survive in `Session` is silently dropped from the next request, which is
exactly the bug this fixes.

CLI: `ReasoningDelta` renders verbatim (the thinking is visible, no more
silence; the TUI styles it in 10). One-shot ignores it: the worker's stdout
is the answer.

### 3. Usage: cache fields and per-turn totals

```go
type Usage struct {
    Prompt     int
    Completion int
    CacheRead  int // tokens served from the server-side prompt cache
    CacheWrite int // zero until the transport reports one
}
```

Adapter wire mapping, grounded live: cache-read is
`usage.prompt_tokens_details.cached_tokens` (measured cold 0, warm 918 of
922 prompt tokens); cache-write is
`usage.prompt_tokens_details.cache_write_tokens` when the server reports
it, else 0. `total_tokens` is read and ignored (Prompt + Completion
suffice; named). `Done` keeps its shape: the fields ride the struct. The
recorder writes the existing `usage.cache_read/cache_write` columns (it
passes 0, 0 today).

Per-turn totals are the Frontend's: it sums the `Done.Usage` it observes
(pane's `collectUsage` pattern) and the CLI prints one line at `TurnEnd`
(the explicit turn boundary, L7):
`↑P ↓C · cache R{r} {hit}%`, pane's `formatTokens` shaping (raw under
1000, one-decimal k under 10k, rounded k under 1M, else M). One known
shape to read: the reasoning round-trip (decision 2) grows the prompt
prefix, so the first model call after a thinking turn reprocesses the
thinking block and that turn's hit rate dips. That is the point of the
round-trip (the thinking is preserved), not a footer regression. The hit-rate
formula is named: `hit = CacheRead / Prompt`, because the OpenAI-style wire
reports cached tokens as a subset of prompt (grounded: 918 of 922). Pane's
additive formula (`cacheRead / (input + cacheRead + cacheWrite)`) assumes
disjoint accounting (Anthropic-style); a transport that reports disjoint
would read the rate low. Named as a known shape, not silently mixed.

### 4. Steering

The contract, in three parts:

1. **The interrupt handle (loop, L1).** The loop threads the turn's cancel
   onto the ctx it passes to `Input`: `core.WithInterrupt(ctx, cancel)`
   (new `core/interrupt.go`, the `WithSession` pattern: the session already
   rides its ctx the same way). A Frontend recovers it with
   `core.InterruptFrom(ctx)` and holds it; cancelling it breaks the running
   turn (L2/L3) and the loop re-enters awaiting_input. The `Frontend`
   interface stays frozen-shaped: the handle rides the ctx, exactly as the
   session does. The turn-over signal is explicit: the loop emits
   `TurnEnd{Reason}` at every turn exit (L7), so a Frontend sees the
   boundary as an event (pane's `agent_end`, without the hook) instead of
   inferring it from `Done` or `ctx.Err()`.
2. **The slot (Frontend).** A Frontend may hold one queued message;
   `Input` must return it before blocking. One slot, latest wins: not a
   mailbox.
3. **The loop's delivery.** The loop already calls `Input` at every turn
   start; a queued message is therefore delivered on the next Input,
   whether the turn ended naturally or was interrupted. The loop gains no
   delivery method; delivery is inside `Input`.

Why no mailbox: the loop's contract is "Input returns one message", and a
queued message is one. A FIFO would queue messages that each become their
own turn, but nothing in 7 needs more than the latest intent: the user
typed a correction, it goes in now or at the boundary. If the TUI (10) or
the `steer` command (9) needs a queue, that is a Frontend-internal change
behind the same one-message contract, and it is proven to be small: the
slot is the Frontend's business, the loop never saw it.

CLI minimal version (the shape, proven before the TUI inherits it):

- A background line reader feeds the slot (buffered 1, latest wins);
  `Input` pops the slot before blocking. Sequential delivery, no locking
  beyond the channel.
- A line that arrives while the held turn ctx is live: queued and the turn
  is interrupted (cancel). That is steering now: the message lands at the
  re-entry.
- A line that arrives while the turn ctx is dead (between turns): queued
  only; delivered on the next `Input`.
- Ctrl-C (the root's `signal.NotifyContext`) keeps today's meaning: the
  session ends, clean. Steering is the typed-line path; the signal path is
  the exit path. Named, so the two are not conflated. One named lag (the
  current behavior, kept): the process stays alive until the in-flight step
  unwinds. A mid-tool turn unwinds quickly (the tool's process group is
  killed, the next model call sees the dead context). A mid-stream turn
  waits for the server's stream to close, because the response-body read is
  not interrupted by ctx cancellation while the server holds the connection
  (verified against the live swap: the loop sits in the event channel until
  the stream ends). The first signal is the exit; a second Ctrl-C is
  ignored (the signal has already fired).

Why the interrupt is a Frontend action and not a new loop entry: the loop
already treats a dead turn context as "back to awaiting_input" (L2/L3);
the Frontend only cancels the handle the loop handed it. No new method, no
mailbox, no loop state.

**The recorder rule (with L7).** Today the recorder discards its partial
assistant buffer only on `Fault`; its `Input` path lands anything pending
before the user row. Under this spec an interrupted mid-stream turn has no
`Fault`, so the partial would persist when the queued message arrives: the
"PARTIAL fresh" bug reversed. The rule: an unlanded partial at any
`TurnEnd` is a partial; the recorder discards it (subsuming the current
Fault-time discard; the fault row still lands on `Fault`). `TurnEnd` is
what makes the interrupt observable to every streaming consumer, not just
the recorder.

**The transcript shape after an interrupt, both cases named.**

- Mid-stream (during a model call): the partial text never lands (the
  assistant row is written on `Done`, which did not come). The transcript
  ends at the last complete message, as on a fault.
- Mid-tool (during execution): the loop runs the batch's remaining calls
  (each sees the dead turn ctx and returns its ctx error as the fed-back
  content), appends one tool message per call, and the turn dies at the
  next model call (L3). The transcript therefore keeps the assistant
  message with all its calls and a result for each (the cancelled calls
  carry the exec's error string). Both are defensible; both are the
  template-legal shape (every call has a response, or the turn ends at the
  last complete message), and resume (decision 5) reproduces whichever the
  rows show, faithfully, as-is.

### 5. Session resume

```go
// store/state, the read-side projection (SPEC_STATE's "projection rebuilt
// from the log", one function, replayable, deterministic):
func Resume(ctx context.Context, db store.DB, sessionID string) (*core.Session, error)
```

- The session row must exist; unknown id fails loud:
  `resume: no such session: <id>`.
- Messages are rebuilt in seq order: user rows to `RoleUser`; assistant
  rows to `RoleAssistant` with `Reasoning` (the column) and their
  `tool_calls` (id, name, args re-parsed to `json.RawMessage`); each call's
  result (nullable until it landed) to a `RoleTool` message with
  `ToolID`. Call order follows the row order.
- `Session.Files` is rebuilt from the `files` rows, so a resumed session
  keeps its drift checks (SPEC_STATE's promise).
- Dangling calls (an assistant row with a call whose result never landed,
  the kill-mid-turn shape) are kept, not dropped or synthesized: the
  template renders an assistant call without a response as a legal
  conversational state, and the next user message follows it. The
  projection is faithful; the transcript is not rewritten.
- The guard's counters, the python kernel, and the slot are per-process:
  resume starts them fresh. Named.

Root: a `-resume <id>` flag. The recorder adopts the existing session row
(its `ensure()` is already idempotent: "a pre-existing session row is
adopted, never re-inserted"), so the same id keeps its identity for todo's
claims and rem's sources. `-p` with `-resume` is a construction error, loud:
one-shot stays one-shot.

The named gap this closes: `RecordFile` has no production caller today, so
`files` rows are written by no one. The recorder gains the session
reference (the root passes it; the session is root-owned) and upserts a
full `files` snapshot at each turn boundary (on `Done` and on `Input`).
Idempotent upserts, short transactions, as every other row. Without this,
SPEC_STATE's "a resumed session keeps its drift checks" is owed by the
schema and unmet by the writer; with it, resume restores what the session
actually had.

### 6. One extension mechanism: the middleware seam, widened

The agent-side participant needs three things: intercept execution (the
guard bounds and refuses), observe the turn boundary (the guard clears its
budget at `turn_start`), and contribute system-prompt prose (pane's
promptGuidelines for leaves that are not tools). The candidates: a new
`core.Hooks` slice, or the `ToolMiddleware` seam widened to carry
capabilities. The spec picks the widened seam and rejects hooks.

```go
// core: the seam as it is, then as it becomes.
type ToolExec func(ctx context.Context, call ToolCall) (string, error)

// was: type ToolMiddleware func(next ToolExec) ToolExec
type ToolMiddleware interface {
    Wrap(next ToolExec) ToolExec
}

// ToolMiddlewareFunc adapts a plain function to the seam (the
// http.HandlerFunc shape), for participants that wrap only.
type ToolMiddlewareFunc func(next ToolExec) ToolExec

func (f ToolMiddlewareFunc) Wrap(next ToolExec) ToolExec { return f(next) }

// Optional capabilities; the loop and the root check by assertion.
// TurnObserver resets per-turn state (the retry guard's budget).
type TurnObserver interface {
    TurnStart(ctx context.Context, s *Session)
}

// GuidelineContributor adds system-prompt prose at the root, which
// assembles the system string the policy receives.
type GuidelineContributor interface {
    Guidelines() string
}
```

`Kernel.Middleware` follows the type (kernel.go). `perm` wraps only
(`Allowlist` returns the adapter; no-op capabilities). `guard` wraps and
observes (decision 7). The loop fans out `TurnStart` (L6); the root
collects `Guidelines()` into the system prompt before it builds the policy
(named root change, zero loop change: prompt assembly belongs to the
prompt, and the prompt string is the root's).

Why this and not `core.Hooks`:

- **One participant, one seat.** The primary consumer is the retry guard,
  and it intercepts: a bound that refuses without executing is a wrap.
  Under hooks the guard would live in two seams (interception in
  middleware, `turn_start`/`tool_result` in hooks) or move its bound into a
  result-mutating hook with no refusal, gaining a second hook point for one
  behavior. Widened, it is one object.
- **One slice, one registration line.** The design test: an extension is a
  file plus a registration line. Hooks would be a second slice
  (`k.Hooks`) and a second option (`WithHooks`): two lines for one class of
  participant, and the ordering between a guard's observation and a perm's
  refusal becomes a composition question with no home. `WithMiddleware`
  stays the only door.
- **Mutation fits the wrap.** pane's guard rewrites the tool result ("read
  it and change the call"); a wrap shapes `(content, err)` on its way out,
  which is exactly that. A hook observing a loop event needs the event to
  be mutable-in-place or return-shaped, inverting the loop's dataflow: the
  loop would ask participants to shape what the loop itself produced.

Rejected, named: `core.Hooks` (a second slice; splits the guard; inverts
dataflow for result mutation).

### 7. Guard alignment: pane's semantics, looper's bound kept

`middleware/guard` re-keys and re-scopes:

- **Keyed by tool name**, not name + args digest. The real failure mode is
  a model failing `edit` with drifting args: under name + args keying every
  drift is a new budget, and the bound never fires. Pane keys by
  `event.toolName`; so do we.
- **Cleared per turn**, via `TurnStart` (the widened seam, decision 6;
  pane's `turn_start`). A new user message is a new budget. Today the
  counter persists across turns.
- **Consecutive**: a success clears the tool's count (both systems agree;
  looper already does this).
- **At the bound, the note** (pane's voice, verbatim): the limit-th failure
  of a tool in a turn gets its fed-back content extended with
  `[retry-guard] <tool> failed <n>× in a row this turn. The error is
  above; read it and change the call, or stop calling this tool. Do not
  retry blindly.`
- **The bound refusal is kept** (looper's hard stop, which pane lacks):
  the next issuance of that tool in the turn is refused without executing,
  in today's voice: `bound exhausted: <tool> has failed <n> times; stop
  reissuing this call`. Named and justified: it preserves
  `TestToolFailureIsBoundedAndRecoverable` byte-for-byte, and the note
  makes the bound teach instead of merely stop.

With the limit at 3, a failing tool in one turn reads: failure, failure,
failure + note, refusal. The model gets the teaching at the bound and a
hard stop after it.

Named test change: `TestDistinctCallsAreCountedSeparately` codified the
old keying (distinct args keep separate budgets). Under name keying the
same tool's drifted args share the budget, so the case inverts and is
rewritten as the shared-budget invariant; the new named cases (failing
first) are `edit`-style drifting args hitting one bound, and a budget that
clears at the turn boundary.

### 8. The additive-events compat rule (into SPEC_CORE)

Events are added, never changed. A Frontend must tolerate an `Event` it
does not recognize; the default is to ignore it. The loop forwards the
kinds it names and ignores the rest (as it does today, implicitly; the
rule makes it a contract). This is what lets 1.0 freeze: a Frontend
written against 1.0 stays correct when 1.x adds `ToolStart` or
`ReasoningDelta` (noise to the old Frontend, never a misread), and a
provider adapter may emit a vocabulary the loop does not yet name without
breaking the stream.

The rule is testable because `core` carries a documented test seam:

```go
// TestEvent exists so the compat rule has a subject: an event the loop
// forwards but does not accumulate, and a Frontend must ignore.
type TestEvent struct{ Name string }
func (TestEvent) event() {}
```

The CLI and one-shot `Notify` switches already have no default case: the
rule is written down, not invented.

## testing

The contract: every existing named case passes byte-for-byte, or its
change is named above.

The existing event-order assertions count only the kinds they name
(`delta,delta,done` and the like), so the additive `TurnEnd` appended at
the turn's end does not disturb them; the cases below are new.

**Unchanged, byte-for-byte** (loop): `TestTextOnlyTurnOrdering`,
`TestToolRoundTripOrdering`, `TestFaultMidStreamPreservesSession`,
`TestTransportErrorSurfacesAndContinues`,
`TestClosedStreamWithoutDoneFailsLoud` (both contexts alive is still a loud
provider bug), `TestCancellationMidStream` (run context dead at the
boundary), `TestCancellationBetweenToolCalls` (run context dead tears the
stream down), `TestMalformedCallFedBackOnce`,
`TestOversizedToolResultStaysIntact`, `TestAssembleErrorAbortsTurnAnd
Recovers`, `TestUnknownToolNameFedBackOnce`, `TestMissingSeamsFailLoud`,
`TestDuplicateToolNamesPanic`, `TestDenialIsFedBackAndBounded`,
`TestToolFailureIsBoundedAndRecoverable` (the note lands on the bound-th
failure, which the case does not assert; the refusal and the counts are
asserted, and hold).

**Unchanged** (guard): `TestRepetitionIsBoundedWithoutSilentRetry`,
`TestSuccessfulReissuanceStaysUnbounded`,
`TestSuccessfulReissuanceResetsTheCount`. **Named change** (guard):
`TestDistinctCallsAreCountedSeparately` inverts (decision 7).

**New named cases** (failing first, per decision):

- Tool events: the bracket order for a tool turn
  (`Done / ToolStart / ToolResult` in sequence, `ToolResult` carrying the
  guarded refusal and `Err` non-nil, `Duration` > 0), with the transcript
  byte-identical to `TestToolRoundTripOrdering`; the recorder lands
  results from the event (no `Observe` tap in the chain); the CLI renders
  the bracket from events alone (the Done-when: pane's status rows
  renderable without loop internals).
- Reasoning: a scripted stream with `ReasoningDelta` before
  `TextDelta` (both the text-only and tool-call branches) lands
  `Message.Reasoning` in the transcript and forwards the deltas in order;
  the adapter test replays a `reasoning_content` chunk and asserts the
  `ReasoningDelta` ordering, and the request-body shape test carries an
  assistant `Reasoning` as `reasoning_content` (the F2 wire-shape
  precedent).
- Usage: the adapter maps `prompt_tokens_details.cached_tokens` to
  `CacheRead` (and the absent field to 0); the recorder writes the cache
  columns from `Done`.
- Steering: a turn-ctx cancel mid-stream breaks the turn and the run
  continues (the next Input is called; both turns land in the transcript;
  `Run` returns nil at EOF); a turn-ctx cancel at the prompt re-enters
  awaiting_input without a `Fault`; the CLI steering slot (a line during a
  live turn cancels the turn and is delivered on the re-entry; a line
  between turns is delivered without a cancel; the slot is single, latest
  wins).
- The interrupt mechanism: a Frontend recovers the cancel with
  `core.InterruptFrom` from the `Input` ctx and cancels it to steer (the
  handle the loop threaded with `core.WithInterrupt`); a Frontend without
  the capability is unaffected (the assertion returns false).
- `TurnEnd`: fires at every turn exit with the right reason (`over` after
  a completed turn, `fault` after a Fault, `interrupt` after a turn-ctx
  death) and after the turn's last other event; it is absent on a
  run-context end; the existing event-order assertions (which count only
  the kinds they name) still hold with it appended.
- The recorder rule: an interrupted mid-stream turn does not persist its
  partial text (the unlanded partial is discarded at `TurnEnd`, the
  "PARTIAL fresh" bug reversed); a mid-tool interrupt keeps the batch's
  results (each cancelled call's row carries the exec's error string) with
  the assistant message and its calls intact.
- Resume: the projection rebuilds a full transcript (user, assistant +
  calls + results, reasoning, files) byte-identical to what the loop built;
  a dangling call survives; an unknown id fails loud naming the id; the
  root `-resume` path (adopted session id, recorder reuse) and
  `-p` + `-resume` refusing at construction.
- Guard: drifting args of one tool share the bound (failing first under
  today's keying); the budget clears at the turn boundary; the bound-th
  failure carries pane's note verbatim.
- Compat: the loop forwards `TestEvent` untouched (no accumulation, no
  ordering break); the CLI and one-shot ignore it (no output, no panic).

The suite is green on a box with no model loaded: every case is scripted
or httptest; the live grounding (reasoning round-trip, cache fields) is a
verification note, not a test dependency.

## scope

What 7 is not: compaction (8) is the first non-passthrough
`ContextPolicy` and consumes nothing new here but the provider seam;
commands (9) attach to the Frontend and make steering a verb (`steer`);
the TUI (10) is a Frontend registration and owns the glass (glyphs,
footer, editor) over the same events. The loop at the end of 7 is the loop
of 8, of 9, and of 10: if one of them needs a loop change, 7 was
incomplete and is reopened first, per the roadmap.

The SPEC_CORE diff this spec implies (PR A carries it): the streaming
events section gains `ReasoningDelta`, `ToolStart`, `ToolResult`,
`TurnEnd` + `TurnReason`, `TestEvent`, and the compat rule; the wire
types gain `Message.Reasoning`; `Usage` gains the cache fields; the
ToolMiddleware section is the widened seam; the Frontend section gains the
steering contract (the `InterruptFrom` handle, the slot, `TurnEnd` as the
boundary signal) and unknown-event tolerance; the loop section gains
L1-L7; the layout gains `core/interrupt.go`; the root's chain drops the
recorder's `Observe` tap (SPEC_CORE's wiring example already shows only
perm + guard, so it stays); the testing section gains the named cases
above, including the recorder rule and the interrupt transcript shape.
SPEC_STATE is already in future tense about this deliverable ("the
recorder switches sources; its schema does not change") and owes no diff.
