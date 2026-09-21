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
row, the summary line, and; like every other reply; the stale footer
when staleness is live; the moment the model acts on recovered state is
the moment the warning matters most. Read returns the actionable queue
(done folds into the summary), ReadAll the history, Create the full
filtered queue. The operation that crosses the compaction threshold
names it in its own reply (`· log compacted (N events folded into the
snapshot)`), so the stale footer's quieting after the fold reads as
explained, not as state loss (SPEC_STREAMLINE 2). The unknown-id
refusal carries the minting voice at every verb (SPEC_STREAMLINE 3).

## What it includes

- `todo.go`: the store: operations, replay, position minting, the DAG
  validation, per-scope folds and one shared event-log sequence.
- `binding.go`: which queue a session works in. `ProjectOf(dir)` mints a
  `Project` from a directory (abs first: one place must not have two
  bucket keys), `Bind`/`BindingOf` record and read a session's binding in
  `session_project`, `RealSession` says whether a session can hold one.
  The binding is mutable state beside the log: the log decides what a
  queue holds, the binding only which queue a call touches.
- `path.go`: `FilePath(home)`, the store's file: `<home>/todo/todo.sqlite`.
- `migration.go`: the one-time 1→2 migration: folds the legacy
  per-cwd stores into `todo.sqlite` (scope = the file's hash) and rem's
  lazy re-scope of the launch cwd's hash to the repo scope.
- `metadata/metadata.go`: hand-written metadata (plus `extra.sql`, which
  now also carries the `session_project` table).

## How it is consumed

- `tool/todo` and the `command` todo verb call the store's operations.

## Gotchas

- tasks/task_deps are a disposable projection: rebuilt from the log inside
  every transaction, never trusted.
- Replay is total: malformed or inapplicable rows are skipped, never
  thrown.
- Positions are minted, never mutated in place: moves are events.
- Create is the only dependency-mutation point: the DAG is validated
  there at the boundary.
- Complete on the caller's own unclaimed pending task implicitly claims
  and completes: start+complete, both events appended, the echo noting
  the auto-start. Foreign-claim and blocked-by-dependency refusals stay.
- Release returns a claimed task to pending (the dead-claim door): it
  refuses the caller's own claim, an unclaimed task, a finished task,
  and a foreign claim younger than StaleClaimAfter (24h). Reap is the
  bulk door wired at session open: it frees foreign claims owned by
  ended sessions (the exact arm) and claims whose owner's last event
  on the task is older than the staleness window (the SIGKILL arm);
  the caller's own claims are never touched. Both append `release`
  events; the note names task and owner, silent when idle.
- The read contract is lean (SPEC_TODO_LEAN): Read renders the
  actionable queue; done rows fold into the unconditional summary line
  `(<label>] N/M done · next: tN · K failed)`, never "(no tasks in
  <label>'s queue)" on an all-done queue; ReadAll returns the history; a
  transition echo is the affected row plus the summary. Create keeps the
  full (filtered) queue: after a merge the whole queue is the news.
- One store, every row scoped: `FilePath(home)` is the one `todo.sqlite`,
  and every operation takes a `Project{Key, Label, OutsideRepo}`: the
  queue's identity (the repo's scope, `store/scope`, or the cwd hash
  outside a repo), its display label, and whether that hash is a bucket
  rather than a project. The store does not decide which project a call
  means; the caller resolves it (the tool holds the order, the
  `session_project` table holds a session's answer). Ids stay `tN` per
  scope; minted event seq is one sequence across scopes; compact folds
  and stale footers are per scope.
- Every summary names its queue (`[rig] 2/5 done · next: t3`, or
  `[ng (not a repo)]` for a bucket), and the empty reply says so too: a
  reply that could be read as two different queues carries the word that
  picks one (SPEC_CORE).
- `Prune` drops the done rows and is itself an event, so a replay drops
  the same rows and a later compact snapshot carries only what survived;
  failed rows stay (they still ask for a retry) and an idle prune appends
  nothing. The empty create stays the one destructive verb; `Create` is
  otherwise a merge on the text natural key, and its note counts the
  merge (`queue merged: 2 new, 1 already there`), never claims a wipe.
- Migration (SPEC_STATE §todo): folds every `<12-hex>.sqlite` in the
  todo dir into `todo.sqlite` with `scope = <that hash>` verbatim, then
  re-scopes the launch cwd's hash to the repo scope once (the
  `migrated:<oldScope>` marker), one transaction, counted on stderr. The
  fold keys on the files existing and is a no-op on the second open.
