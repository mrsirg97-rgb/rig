# rig design

rig is a minimal agent loop machine: it executes the agent loop against the
seams you wire in, exactly once, faithfully. It is not a framework. What it is
a machine for, in one sentence: a faithful turn loop — assemble context, stream
the provider's output, execute what it asks for, feed it back, repeat — with
every dependency held at a typed seam.

The [core spec](../specs/SPEC_CORE.md) governs this document where they disagree.

## architecture

```
 user ◄──────────────────────────►  frontend/cli        seam: Frontend
                         │
                         ▼
                  loop.Run(ctx, kernel)   the concrete turn runtime
                  │  ├─ seam: ContextPolicy ─► policy        (per-turn Assemble)
                  │  └─ seam: Provider      ─► provider/openai (stream out, events in)
                  └─ seam: Tool ─► ToolMiddleware chain
                                   └─ perm.Allowlist → guard.Bound (first-listed = innermost)
                                        │
                                        ▼
             tool/bash · tool/file · tool/fs · tool/todo · tool/rem · tool/scheduler · tool/python · tool/web

 cmd/rig (composition root): wires every seam once at startup; flags and env only.
 store/state: the recorder wraps the Frontend — it sources its rows from the
              loop's events, and -resume rebuilds a session from the state store.
```

### the seams

| seam             | shape (abridged)                                        | role                                        | swapped where          |
|------------------|---------------------------------------------------------|---------------------------------------------|------------------------|
| `core.Provider`  | `Stream(ctx, messages) (EventStream, error)`            | model access; streaming events in           | `kernel.WithProvider`  |
| `core.Tool`      | `Name/Description/Schema/Exec(ctx, args)`               | capability; stdlib-agnostic                 | `kernel.WithTools`     |
| `core.Frontend`  | `Input(ctx) (string, error)`; `Notify(Event)`           | human I/O and the event sink                | `kernel.WithFrontend`  |
| `core.ContextPolicy` | `Assemble(ctx, session) (messages, error)`          | context construction per turn               | `kernel.WithPolicy`    |
| `core.ToolMiddleware`| `Wrap(core.ToolExec) core.ToolExec` (+ optional `TurnStart`, `Guidelines`) | cross-cutting tool-side policy, observed, taught | `kernel.WithMiddleware`|

`ToolMiddleware` is an interface; `ToolMiddlewareFunc` adapts a plain
`func(core.ToolExec) core.ToolExec` to it (the `perm` wrap-only shape is
unchanged). `TurnStart` (turn observation) and `Guidelines` (system-prompt
prose) are assertion-checked capabilities, not required methods: the loop
fans out `TurnStart` once per turn, and the root collects `Guidelines` into
the system prompt before the policy is built. One mechanism, not a second.

The loop names none of the concrete types: `loop.Run(ctx, k *rig.Kernel)` takes
the kernel interface and resolves seams at runtime. Swapping any dependency is a
change at the composition root and nowhere else.

### the turn

```
 user message ─► Assemble (ContextPolicy) ─► Stream (Provider)
      ▲                                        │
      │                                        ├─ ReasoningDelta / TextDelta ─► Notify
      │                                        ├─ ToolCall ─► ToolStart ─► exec chain ─► ToolResult ─► back into the stream
      │                                        ├─ Fault ─► Notify + the turn aborts, session intact
      └────────────────────────────────────────┴─ Done ─► TurnEnd{over|fault|interrupt} ─► next user message
```

Every turn exit inside the run emits `TurnEnd{Reason}` (`over`, `fault`,
`interrupt`) after the turn's last other event; a run-context end emits none.
The loop fans out `TurnStart` to registered observers before the first
Assemble, once per turn.

Turn-boundary semantics (the runtime's contract, enforced and tested):

- **Fault/transport error** (provider fault, stream error, *and* a failing
  Assemble on a live turn): surfaced through `Frontend.Notify`, turn aborts,
  the session survives to its last complete message, the loop returns to
  awaiting input. A failing policy cannot take down the REPL.
- **Steering (a dead turn ctx)**: the turn's cancel is threaded onto the
  Input ctx as the interrupt handle (`core.WithInterrupt`/`InterruptFrom`).
  A steer reads as an interrupt at every seam: at the prompt the loop
  re-enters awaiting input (no Fault); mid-stream it breaks the turn; at the
  pre-stream seams (`Assemble`, the `Stream` call — a provider or policy
  that checks its context at call time) it is `TurnEnd{interrupt}` with no
  Fault, because the model never started. The run re-prompts; the delivered
  line is the next user message.
- **Stream closed without Done or Fault, both contexts alive**: `Run` returns
  a loud error (a provider bug). Silent termination is not an option.
- **Cancellation** (run-ctx teardown, e.g. Ctrl-C): the session ends once
  the in-flight step unwinds, cleanly (`nil`); the loop never names which
  step — it only observes the context.

### middleware composition

`WithMiddleware(perm, guard)` composes **first-listed innermost**: the chain
executes perm first, guard second. This is a deliberate inversion of the common
`http.Handler` convention, chosen so that the listing order reads as the
execution order (`[deny, bound]` = deny runs first). It is what makes the
spec's pairing workable — the bound must sit outside the denial to count it.

### guard semantics (`guard.Bound`)

Per the spec, every tool call executes exactly once, always — the guard never
retries silently. What it bounds is the *model's* re-issuance of a failing
*tool*, aligned to pane's retry guard:

- keyed by **tool name** — drift in the arguments does not dodge the bound;
- **cleared at the start of every turn** (the loop's `TurnStart` fan-out)
  and on success: the bound tracks streaks within a turn, not history;
- the limit-th consecutive failure of a tool carries a note — the error is
  above, read it and change the call, or stop calling the tool — appended to
  the model-visible result, never replacing it;
- the next re-issuance is refused without executing, naming the bound.

Denials are attributed (refusal string plus error) so they are countable; that
attribution is what makes the spec's two sentences — refusal fed back to the
model, repetition bounded — simultaneously true.

### session and persistence

The session is an in-memory transcript with a minted, stable id; its
persistence is the state store (`specs/SPEC_STATE.md`), SQLite under the
config dir, WAL and a corruption quarantine. The **recorder** is a Frontend
wrapper (wired at the root, not the loop): it sources its rows from the loop's
events — transcript, tool calls and their guarded results, usage with the
cache fields, reasoning — appending per event in short transactions, so a
kill leaves every completed row readable. A `TurnEnd` discards the unlanded
partial of a turned turn (the old Fault-time discard, subsumed), and each turn
boundary upserts the file provenance. `rig --resume <id>` projects the
session back from the store in one read-only transaction — transcript in seq
order, assistant reasoning and calls (row order), landed results, dangling
calls kept, files rebuilt — and the recorder adopts the existing row, so one
identity serves todo's claims and rem's sources. The JSON Save/Load pair is
the test seam now, not the persistence path.

Writes are validated before acting: the file tool checks disk state against
the last-read state and names the drift; ambiguity ("occurs N times") is named
and the write is refused. Provenance is canonicalized through `filepath.Abs`
so `a.go` and `./a.go` are one key.

### induced-work bounds

| where            | bound                                                          |
|------------------|----------------------------------------------------------------|
| bash output      | 256 KiB, naming the truncation                                 |
| bash lifecycle   | `WaitDelay` (background children can't hold the turn), process-group teardown on cancellation |
| file read        | 1 MiB, naming the truncation                                    |

## extending

The design test: adding a tool, a provider, a policy, a frontend, or a
middleware is **one file plus one registration line** at the composition root,
and the loop never names a concrete type.

1. **A tool** — implement `core.Tool` in `tool/<name>/<name>.go` (stdlib-
   agnostic exec; loud at the boundary); register in `cmd/rig`'s
   `WithTools(...)`, and in the default allow-list if it should be permitted.
2. **A provider** — implement `core.Provider` (every stream ends in Done or
   Fault, ctx teardown excepted); `WithProvider(...)`.
3. **A context policy** — implement `core.ContextPolicy`; `WithPolicy(...)`.
   Passthrough and the compaction policy (`policy/compact`, per-model
   trigger) both ship; the policy may rewrite the session transcript — the
   one named mutation the seam carries.
4. **A frontend** — implement `core.Frontend`'s two methods (a TUI can sit
   beside the CLI); `WithFrontend(...)`.
5. **A tool middleware** — implement `core.ToolMiddleware` (or adapt a plain
   `func(core.ToolExec) core.ToolExec` with `ToolMiddlewareFunc`), registered
   in listing order with its intended position (first-listed innermost).
6. **A command** — implement `core.Command` (a user verb, dispatched by the
   Frontend before the loop sees the line); `WithCommands(...)`. One file in
   `command/` plus one registration line.
   Optionally implement the assertion-checked capabilities: `TurnStart`
   (observe the turn boundary; the guard clears its counts here) and
   `Guidelines` (contribute system-prompt prose; the root collects it).

## constraints

- **Stdlib-only core.** `core/` and `loop/` carry no dependencies; a leaf
  dependency is justified in its spec first. The store's one is
  `modernc.org/sqlite` (pure-Go driver, `specs/SPEC_STATE.md`); everything
  else — providers, tools, middleware, frontends — is stdlib.
- **Closed, typed seams.** Dependencies are compile-time explicit; nothing is
  loaded, discovered, or reflected at runtime. Unknown at a seam is a loud
  error, never a guess.
- **One process.** Modular monolith; cross-process only when demanded.
- **Default-deny at the boundary.** Tools execute only through the registered
  middleware chain; the root's default allow-list exists because a default-
  deny CLI would ship a dead agent — narrowing is the operator's act.
- **The loop never retries.** Repetition is the model's act; the loop's duty
  is faithful surfacing plus the bound.

## non-goals

Explicitly out, per the spec: sandboxing, plugin systems, multi-provider
negotiation, and conversational features the model does not ask for.
