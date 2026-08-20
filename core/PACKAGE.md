# core

## What it is

The kernel's contract surface. It defines the seams the loop, root, and
adapters implement; it holds no behavior of its own. Everything here is a
type, an interface, or a context helper.

## What it includes

- **Seams**: `Provider` (stream one turn), `ContextPolicy` (assemble
  messages), `ToolMiddleware` (+ `ToolMiddlewareFunc` adapter),
  `TurnObserver`, `GuidelineContributor`, `Command`, `Frontend`.
- **Wire types**: `Message`, `ToolCall`, `ToolSpec`, `Request`, `Usage`,
  and the `Event` vocabulary (`TextDelta`, `ReasoningDelta`,
  `ToolCallEvent`, `Done`, `Fault`, `ToolStart`, `ToolResult`, `TurnEnd`,
  `TestEvent`, `Compacted`, `Compacting`).
- **State**: `Session` (transcript + `FileState` provenance), JSON
  save/load.
- **Helpers**: `WithInterrupt`/`InterruptFrom` (turn cancel under a typed
  ctx key), `WithSession`/`SessionFrom` (session under a typed ctx key).

## How it is consumed

- `Provider.Stream` returns a `<-chan Event`; the provider closes it after
  `Done` or `Fault`. `Request` carries messages plus name/schema-only tools;
  execution stays in the kernel.
- `ToolMiddleware` wraps `ToolExec` (the `http.Handler` shape), composed in
  listed order at the root: first-listed is innermost. Optional
  capabilities (`TurnObserver`, `GuidelineContributor`) are assertion-
  checked at the loop and root.
- `ContextPolicy.Assemble` returns the messages one turn sees. v1 is the
  passthrough; the compact policy rewrites the transcript at its trigger.
- `Command.Run` gets the dispatcher's context, the verbatim args line, and
  the env the root built; returns the reply or the refusal as an error.
- `Frontend.Input` is a blocking pull; `Notify` is a fire-and-forget
  observation of the turn stream.
- `Session` rides ctx into the exec chain so file tools keep `FileState`
  without reaching around the `Tool` interface.

## Gotchas

- `Event` is a sealed marker (`event()`); the loop's switch on it is
  exhaustive by convention, not by the compiler. Add an event = update the
  loop's switch.
- `Fault` ends the stream with an error; the loop preserves the session up
  to the last complete message. A closed channel without `Done` or `Fault`
  is a provider bug.
- `ToolResult.Err` non-nil marks a fed-back failure, distinct from a
  success string; consumers must not parse the string to tell outcomes
  apart.
- `TurnEnd` closes every turn; a run-context cancel ends the run, not a
  turn, and emits nothing. An unlanded partial at any `TurnEnd` is a
  partial and is discarded.
- `ContextTokens` is server-reported, 0 when unreported; it is the
  compaction anchor and never rides the wire.
- `Request.MaxTokens` and `ReasoningEffort` are additive: 0/empty = the
  provider's default, and a provider that ignores them is fine.
- `Load` normalizes a missing `Files` key to an empty map (never nil), and
  refuses unknown JSON fields.
