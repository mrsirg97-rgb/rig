# store/todo

## What it is

The task-queue store, Go over the generated substrate (SPEC_STATE's "###
todo" section, SPEC_TODO_EDGES). The event log is the spine; tasks/task_deps
are a disposable projection rebuilt from the log inside every transaction
and never trusted. Replay is total: malformed or inapplicable rows are
skipped, never thrown. Positions are minted, never mutated in place;
moves are events. Create is the only link-mutation point; the
requires/blocks DAG is validated there, at the boundary, and refused
loudly naming the tasks (unknown link, self-link, cycle through either
relation).

The reply contract (the lean read): a transition echoes the affected
row, the summary line, and; like every other reply; the stale footer
when staleness is live; the moment the model acts on recovered state is
the moment the warning matters most. Read returns the present — open
work first in queue order, then the finished work related to it
(nearest hop over requires/blocks), then the recent finished, newest
terminal event first, until ten finished rows show in total
(DefaultFinishedShown), one dim hint line naming what is hidden and
the finished-list door; ReadFinished lists the n most recent finished
(default ten, cap FinishedListCap); ReadAll the history; Create the
full present. Retirement is a view, not a state: a hidden finished task
still satisfies requires, links resolve by id, and notes and show work
on any id. The operation that crosses the compaction threshold
names it in its own reply (`· log compacted (N events folded into the
snapshot)`), so the stale footer's quieting after the fold reads as
explained, not as state loss (SPEC_STREAMLINE 2). The unknown-id
refusal carries the minting voice at every verb (SPEC_STREAMLINE 3).

The swarm surface (1.3.9): `claim` takes the first pending task whose
dependency is done (the order `next` shows) and marks it active for the
caller, or with `status=review` the first task in review that no
reviewer holds (the status stays review, the owner becomes the
caller); `nothing to do` when nothing qualifies. `note` appends to any
task whatever the hold — notes are how agents talk about shared work —
and `read` shows the count (`· N notes`) while the `notes` action lists
them in order with their session and time, headed by the task's link
lines; `read` with `id` renders one task, summary-only, and points at
`notes`; `notes` reads nothing and appends nothing, and a task without
notes replies `no notes on tN`. The read's `waits for k` suffix is a
count of the unfinished tasks that block a target; the links name the
edges. The review gate keys on who completes: `Complete` takes a worker
flag — `worker=false` (an interactive session) lands the task done in
one call, writing the complete/accept pair so the log stays uniform and
replay is unchanged; `worker=true` (`rig -p`: delegate, swarm) submits
it for review. `accept` moves review to done and `reject` moves review
to pending, recording the reason as a note; both auto-claim an unowned
review task (claim+accept, the same idiom as complete auto-starting a
pending one), so a parent reviews its workers by read then accept/reject
with no claim step, and a foreign holder still refuses. `blocked` clears only on done (a dependency in review
keeps its dependents blocked); a task waits for its `requires` target and
for every unfinished task whose `blocks` names it, `complete` and
`accept` on a blocked task refuse naming what it waits for, prune still
drops done only, and the summary counts review rows (`· N in review`).

## What it includes

- `verbs.go`: the mutating operations (claim, note, notes, accept,
  reject, the review state), `read.go`: the read verbs, `tx.go`: the
  transaction machinery (the per-scope fold, compaction, the event
  append), `types.go`: the fold's domain (tasks, positions, the event
  row), `fold.go`: the event application, `create.go`: the
  requires/blocks DAG validation and cyclePath (both relations),
  `render.go`: the queue render; one shared event-log sequence.
- `task.go`: the structured reads beside the render: `Task` (one task's
  brief: text + notes with sessions) and `Counts` (the fold's
  per-status counts, read-only) — the swarm's brief and status band
  never parse the rendered reply.
- `binding.go`: which queue a session works in. `ProjectOf(dir)` mints a
  `Project` from a directory (abs first: one workspace must not have two
  keys), `Bind`/`BindingOf` record and read a session's binding in
  `session_project`, `RealSession` says whether a session can hold one
  (the anonymous attribution a threadless call gets is shared by every
  anonymous caller, so a binding recorded under it would leak one
  session's project onto another's). `Bind` on an unattributable session
  is inert, not an error: an unthreaded verb still works, it just cannot
  carry a binding forward. Inside a repo the queue's label is the common
  dir's own base, so a session in a subdirectory or a second worktree
  names the workspace it is in, not the folder it started in; a bare repo's
  common dir is the repo root itself, so the name is the root's own base.
  The binding is mutable state beside the log: the log decides what a
  queue holds, the binding only which queue a call touches.
- `path.go`: `FilePath(home)`, the store's file: `<home>/todo/todo.sqlite`.
- `migration.go`: the one-time 1→2 migration: folds the legacy
  per-cwd stores into `todo.sqlite` (scope = the file's hash) and rem's
  lazy re-scope of the launch cwd's hash to the repo scope. Old payloads
  name the wait edge `dependsOn`: it folds as `requires`. `EdgeMigration`
  rebuilds the disposable `task_deps` projection with the edge kind
  column — the projection is rebuilt from the log inside every
  transaction and never trusted, so dropping it is safe; the log carries
  the edges.
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
- Create is the only link-mutation point: the requires/blocks DAG is
  validated there at the boundary (SPEC_TODO_EDGES).
- Complete on the caller's own unclaimed pending task implicitly claims
  and submits: start+complete, both events appended, the echo noting the
  auto-start. Foreign-claim and blocked-by-dependency refusals stay.
- Complete and start are idempotent at the state they ask for (2.1.7):
  complete on a done task and start on a task already in progress by the
  same session (or unowned) answer with the echo a fresh call would give
  — the row and the queue summary — write no event and run no
  compaction, so the model reads `[x]`/`[~]` instead of treating a
  refusal as a mistake. A foreign session's start of an owned task still
  refuses naming the claimer; done stays read-only for every other verb
  (start, fail, release, accept, reject).
- The review gate (1.3.9) keys on who completes: a worker's complete ends
  in review, an interactive one lands done with the pair; accept ends in
  done, reject returns to pending with the reason as a note. Accept and
  reject auto-claim an unowned review task, so the parent needs no claim
  step; a foreign holder still refuses, and `claim status=review` stays
  for reviewers who want to hold before deciding. Notes are free (no
  hold needed), bounded at MaxNoteLen (a note rides the event log and
  the compact snapshot, so an unbounded note is an unbounded log row),
  and replayable: they ride the event log and the compact snapshot. Historical completes (pre-1.3.9
  logs) replay as done through ReviewMigration's accept pairing, a
  one-time 2→3 migration that is a no-op on later opens.
- Release returns a claimed task to pending (the dead-claim door): it
  refuses the caller's own claim, an unclaimed task, a finished task,
  and a foreign claim younger than StaleClaimAfter (24h). Reap is the
  bulk door wired at session open: it frees foreign claims owned by
  ended sessions (the exact arm) and claims whose owner's last event
  on the task is older than the staleness window (the SIGKILL arm);
  the caller's own claims are never touched. Both append `release`
  events; the note names task and owner, silent when idle.
- The read contract is lean (SPEC_TODO_LEAN): Read renders the present
  — open work first (pending, active, review, failed), then related
  done (nearest hop), then recent done, `DefaultFinishedShown` rows
  total, one dim hint line when anything is hidden, naming the largest
  window (`todo list finished <min(done, FinishedListCap)>`); the
  summary line is unconditional
  (`[<label>] N open · K of M finished shown · next: tN`, the finished
  clause omitted when nothing is done), never "(no tasks in
  <label>'s queue)" on an all-done queue; ReadFinished lists the n
  most recent done, newest first, default ten, cap `FinishedListCap`;
  ReadAll returns the history (the operator's read); a transition echo
  is the affected row plus the summary. Create keeps the full present:
  after a merge the whole queue is the news. Retirement never changes
  semantics: a hidden done task still resolves by id, links resolve by
  id, and notes and show work on any id.
- One store, every row scoped: `FilePath(home)` is the one `todo.sqlite`,
  and every operation takes a `Project{Key, Label, OutsideRepo}`: the
  queue's identity (the workspace's scope, `store/scope`, or the cwd hash
  outside a repo), its display label, and whether that hash belongs to a
  non-repo directory — the flag lives on in the store and the binding
  table, nothing renders it. The store does not decide which project a
  call means; the caller resolves it (the tool holds the order, the
  `session_project` table holds a session's answer). Ids stay `tN` per
  scope; minted event seq is one sequence across scopes; compact folds
  and stale footers are per scope. The compact snapshot carries the
  events the counters were rebuilt from, so it carries the ids: ids are
  minted from the high-water mark and the create events that advanced it
  are about to be deleted; forget that and the next mint reissues an id
  some session still holds. A snapshot written before the counters
  existed reports neither and 0 means "keep minting from what is here":
  the mint skips the ids the snapshot still holds, which is the
  pre-counter behaviour.
- Every summary names its queue (`[rig] 2/5 done · next: t3`; a
  workspace outside a repo is `[ng]` like any other), and the empty reply
  says so too: a reply that could be read as two different queues carries
  the word that picks one (SPEC_CORE).
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
