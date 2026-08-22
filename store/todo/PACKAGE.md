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

The reply contract (the lean read): a transition echoes the affected
row, the summary line, and — like every other reply — the stale footer
when staleness is live; the moment the model acts on recovered state is
the moment the warning matters most. Read returns the actionable queue
(done folds into the summary), ReadAll the history, Create the full
filtered queue. The operation that crosses the compaction threshold
names it in its own reply (`· log compacted (N events folded into the
snapshot)`), so the stale footer's quieting after the fold reads as
explained, not as state loss (SPEC_STREAMLINE 2). The unknown-id
refusal carries the minting voice at every verb (SPEC_STREAMLINE 3).

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
- Complete on the caller's own unclaimed pending task implicitly claims
  and completes: start+complete, both events appended, the echo noting
  the auto-start. Foreign-claim and blocked-by-dependency refusals stay.
- The read contract is lean (SPEC_TODO_LEAN): Read renders the
  actionable queue — done rows fold into the unconditional summary line
  `(N/M done · next: tN · K failed)`, never "(no tasks)" on an all-done
  queue; ReadAll returns the history; a transition echo is the affected
  row plus the summary. Create keeps the full (filtered) queue: a
  replacement's point is the new state.
- `StorePath(home, cwd)`: one file per cwd under `<home>/todo`, keyed by
  the first twelve sha1 bytes of the cwd — the root's and the
  dashboard's one source (SPEC_SERVE 2).
