# store/todo

## What it is

The task-queue store, Go over the generated substrate (SPEC_STATE's "###
todo" section). The event log is the spine; tasks/task_deps are a
disposable projection rebuilt from the log inside every transaction and
never trusted. Replay is total: malformed or inapplicable rows are
skipped, never thrown. Positions are minted, never mutated in place;
moves are events. Create is the only dependency-mutation point; the DAG
is validated there, at the boundary, and refused loudly with the problem
in a teaching voice.

## What it includes

- `todo.go` — the store: operations, replay, position minting, the DAG
  validation.
- `metadata/metadata.go` — hand-written metadata (plus `extra.sql`).

## How it is consumed

- `tool/todo` and the `command` todo verb call the store's operations.

## Gotchas

- tasks/task_deps are a disposable projection: rebuilt from the log inside
  every transaction, never trusted.
- Replay is total: malformed or inapplicable rows are skipped, never
  thrown.
- Positions are minted, never mutated in place; moves are events.
- Create is the only dependency-mutation point; the DAG is validated
  there at the boundary.
