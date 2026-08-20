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
