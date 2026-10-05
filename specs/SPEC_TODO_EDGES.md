# SPEC_TODO_EDGES: the board's two edges and the notes door

## 1. edges

A task carries two link fields, `requires` and `blocks`, each `id (tN) |
null`, one edge per field from the row's point of view.
`requires tN`: I cannot start until tN is done. `blocks tN`: tN cannot
complete until I am done. `dependsOn` is renamed; old payloads (create
events and compact snapshots) fold as `requires` at replay, verbatim.
An empty string is the same as omitting the field: no edge, no refusal
(a model filling every field does not invent a link to ""); `null`
clears an existing link (amended 2.1.6). A link names a task that
exists, by the id its own create replied with: `create` takes one task
(2.12.4), so the relation is real when it is written and there is
nothing to resolve. Through 2.12.3 a create took a `tasks` array and a
link could be a sibling's exact text or its 1-based position in the
array; a model numbered its five-step plan and wrote `requires: "t3"`,
which resolved to the queue's August t3 instead of its own third step,
and the chain it meant was never recorded. One task per create is
atomic: one call, one id back, one event in the log; independent tasks
go out as parallel calls in one turn, and a chain serializes because it
must. Old array payloads still fold at replay, verbatim, with the
sibling and position forms they were written in.

- `blocked(t)` = t.requires unfinished OR any task with blocks == t
  unfinished. Unfinished is pending, in_progress, review, failed: the
  only finish is done, so a dependency in review keeps its dependents
  blocked.
- `claim` takes only unblocked pending tasks (the order `next` shows);
  `complete` and `accept` on a blocked task refuse and name what it
  waits for (the ids, in queue order, with a status hint). Start on a
  blocked task stays legal: the board does not stop a session that
  begins prep work, it just refuses the finish.
- `create` refuses loudly and names the task: an unknown link
  (`requires 'x' not found`, then once per refusal the one form a link
  takes: `a link is a task id from a reply, "t12"; create the task
  first, then link to the id it was given`; the refusal shows the queue
  so the next call can link by id, and nothing lands), a link to itself
  (`'x' cannot require itself`, only reachable by re-creating an
  existing text with its own id), and a cycle through either relation
  (`links would form a cycle: t1 -> t2 -> t1`). The graph is the waits-for relation:
  `requires` gives t -> required, `blocks` gives target -> blocker;
  `cyclePath` walks both. The refusal is the only place a create reply
  shows the queue (2.12.8): an accepted create echoes its own row and
  the summary, the shape every other write has, and `read` is where the
  queue is.
- Same events, fold, compaction, replay. The compact snapshot carries
  both links; old snapshots' `dependsOn` folds as `requires`.

## 2. the read surface

One task line: `tN [ ] text`, then `· requires tN` and `· blocks tN`
(each if present), then `· waits for k` on a target: k is the number of
unfinished tasks that block it. The summary's `next:` skips blocked
tasks as before.

## 3. notes render

`read` no longer inlines note text. A task with notes shows one line
under it, `· N notes` (singular/plural). The `notes` action with `id`
returns the task's notes in order, each with its session and time,
headed by the task's link lines (requires/blocks); a task with none
replies `no notes on tN`. `read` with `id` renders one task,
summary-only, and points at `notes` in the reply. The swarm brief
(`TaskInfo`) still carries the full notes, workers need them.

Notes ride the log and the compact snapshot as before; the snapshot now
also carries each note's time (old snapshots fall back to the compact
event's time).
