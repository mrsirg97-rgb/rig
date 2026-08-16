# looper design

looper is a minimal agent loop machine: it executes the agent loop against the
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
             tool/bash · tool/file · tool/fs · tool/todo · tool/rem · tool/scheduler · tool/python

 cmd/looper (composition root): wires every seam once at startup; flags and env only.
```

### the seams

| seam             | shape (abridged)                                        | role                                        | swapped where          |
|------------------|---------------------------------------------------------|---------------------------------------------|------------------------|
| `core.Provider`  | `Stream(ctx, messages) (EventStream, error)`            | model access; streaming events in           | `kernel.WithProvider`  |
| `core.Tool`      | `Name/Description/Parameters/Exec(ctx, call)`           | capability; stdlib-agnostic                 | `kernel.WithTools`     |
| `core.Frontend`  | `Messages() chan<- core.Message`; `Observe(ctx, stream)`| human I/O                                   | `kernel.WithFrontend`  |
| `core.ContextPolicy` | `Assemble(ctx, session) (messages, error)`          | context construction per turn               | `kernel.WithPolicy`    |
| `core.ToolMiddleware`| `func(core.ToolExec) core.ToolExec`                 | cross-cutting tool-side policy              | `kernel.WithMiddleware`|

The loop names none of the concrete types: `loop.Run(ctx, k *looper.Kernel)` takes
the kernel interface and resolves seams at runtime. Swapping any dependency is a
change at the composition root and nowhere else.

### the turn

```
user message ─► Assemble (ContextPolicy) ─► Stream (Provider)
        ▲                                         │
        │                                         ├─ TextDelta ─► Notify (Frontend)
        │                                         ├─ ToolCall ─► ToolExec chain ─► ToolResult ─► back into the stream
        │                                         ├─ Fault ─► Notify + turn aborts, session intact
        └─────────────────────────────────────────┴─ Done ─► next user message
```

Turn-boundary semantics (the runtime's contract, enforced and tested):

- **Fault/transport error** (provider fault, stream error, *and* a failing
  Assemble): surfaced through `Frontend.Notify`, turn aborts, the session
  survives to its last complete message, the loop returns to awaiting input.
  A failing policy cannot take down the REPL.
- **Stream closed without Done or Fault**: `Run` returns a loud error. Silent
  termination is not an option.
- **Cancellation** (ctx teardown, e.g. Ctrl-C): the current step's work is
  cancelled at its next boundary and teardown surfaces cleanly (`nil`).

### middleware composition

`WithMiddleware(perm, guard)` composes **first-listed innermost**: the chain
executes perm first, guard second. This is a deliberate inversion of the common
`http.Handler` convention, chosen so that the listing order reads as the
execution order (`[deny, bound]` = deny runs first). It is what makes the
spec's pairing workable — the bound must sit outside the denial to count it.

### guard semantics (`guard.Bound`)

Per the spec, every tool call executes exactly once, always — the guard never
retries silently. What it bounds is the *model's* re-issuance of an identical
failing call:

- keyed by name plus args digest (sha256), counted **across turns**;
- after `limit` failures of the same call, the next identical issuance is
  refused without executing, naming the bound;
- a successful re-issuance (polling) never counts, and success **resets the
  count** — the bound tracks streaks, not history.

Denials are attributed (refusal string plus error) so they are countable; that
attribution is what makes the spec's two sentences — refusal fed back to the
model, repetition bounded — simultaneously true.

### session and persistence

The session is a versioned JSON document (unknown version → loud error, never
a guess): a message transcript, each entry optionally tagged with provenance
and/or an exec record, plus counters. Writes are validated before acting: the
file tool checks disk state against the last-read state and names the drift;
ambiguity ("occurs N times") is named and the write is refused. Provenance is
maintained per-session in memory, canonicalized through `filepath.Abs` so
`a.go` and `./a.go` are one key. The wiring side owns persistence; `Run` takes
the session from the kernel (a fresh in-memory session if nil).

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
   agnostic exec; loud at the boundary); register in `cmd/looper`'s
   `WithTools(...)`, and in the default allow-list if it should be permitted.
2. **A provider** — implement `core.Provider` (every stream ends in Done or
   Fault, ctx teardown excepted); `WithProvider(...)`.
3. **A context policy** — implement `core.ContextPolicy`; `WithPolicy(...)`.
   Day-one is passthrough; compaction is the planned upgrade (session JSON
   already carries the transcript).
4. **A frontend** — implement `core.Frontend`'s two methods (a TUI can sit
   beside the CLI); `WithFrontend(...)`.
5. **A tool middleware** — `func(core.ToolExec) core.ToolExec`, registered in
   listing order with its intended position (first-listed innermost).

## constraints

- **Zero third-party dependencies.** `go.mod` carries no require lines; stdlib
  only across core, loop, providers, tools, and middleware.
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
negotiation, memory beyond session persistence, and conversational features
the model does not ask for.
