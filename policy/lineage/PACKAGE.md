# policy/lineage

## What it is

A model's reasoning kept to itself (SPEC_LINEAGE): a provider decorator that sends a turn's reasoning only to the
model row that wrote it. Stdlib only.

## What it includes

- `Own`: one row's record of the reasoning it wrote in a session (each stream's reasoning deltas joined), safe for
  concurrent use. `NewOwn(seed...)` seeds it (a resumed session's, from `state.TrailingReasoning`).
- `Decorator(inner, own)`: wraps a `core.Provider`. `Stream` sends a copy of the request whose assistant turns not
  in `own` carry no reasoning (a turn with `ReasoningDetails` is left as it is), and records what the stream
  reasons into `own` at its `Done`.

## How it is consumed

- The root wraps the row's provider with it in `buildPair`, inside `toolset.Carry` and outside `compact`, with the
  row's record (`ownFor`): every pair built for the row shares one record. A new session resets the records; a
  resume seeds the current row's.

## Gotchas

- The session is never changed: only the request's copy. A turn that lost its reasoning on the way to one row
  still carries it in the session and the store.
