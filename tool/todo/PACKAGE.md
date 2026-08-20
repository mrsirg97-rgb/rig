# tool/todo

## What it is

Adapts `store/todo` to the loop's tool surface: session attribution from
the threaded ctx; replies exactly as the store shapes them.

## What it includes

- `Tool` — a `core.Tool` over the todo store's verbs.

## How it is consumed

- Registered at the root as a native tool.

## Gotchas

- Replies are the store's shapes, verbatim — the adapter does not
  re-voice; the store's teaching refusals carry the protocol.
- The read contract is the lean one (SPEC_TODO_LEAN): read returns the
  actionable queue (done folds into the summary line), read all:true
  returns the history, and a transition echo is the affected row plus
  the summary — never the full queue. Create keeps the full (filtered)
  queue because a replacement's point is the new state.
