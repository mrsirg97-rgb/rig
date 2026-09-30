# SPEC_TODO_EDGES: the board's two edges and the notes door

## 1. edges

A task carries two link fields, `requires` and `blocks`, each `id (tN) |
exact text | null`, one edge per field from the row's point of view.
`requires tN`: I cannot start until tN is done. `blocks tN`: tN cannot
complete until I am done. `dependsOn` is renamed; old payloads (create
events and compact snapshots) fold as `requires` at replay, verbatim.
An empty string is the same as omitting the field: no edge, no refusal
(a model filling every field does not invent a link to ""); `null`
clears an existing link (amended 2.1.6). Within one `create`, a bare
number N is the task at 1-based position N of that call's `tasks`,
tried after the id and the exact text: ids are always `tN`, so no link
that resolved before changes meaning, and a model numbering its own
plan links it in one call instead of one create per task (amended
2.1.9).

- `blocked(t)` = t.requires unfinished OR any task with blocks == t
  unfinished. Unfinished is pending, in_progress, review, failed: the
  only finish is done, so a dependency in review keeps its dependents
  blocked.
- `claim` takes only unblocked pending tasks (the order `next` shows);
  `complete` and `accept` on a blocked task refuse and name what it
  waits for (the ids, in queue order, with a status hint). Start on a
  blocked task stays legal: the board does not stop a session that
  begins prep work, it just refuses the finish.
- `create` refuses loudly and names the tasks: an unknown link
  (`requires 'x' not found`, then once per refusal the forms a link
  takes: `a link is tN from a reply, a sibling's exact text, or its
  number in this list`), a link to itself by text or position
  (`'x' cannot require itself`), and a cycle through either relation
  (`links would form a cycle: t1 -> t2 -> t1`). The graph is the waits-for relation:
  `requires` gives t -> required, `blocks` gives target -> blocker;
  `cyclePath` walks both.
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
