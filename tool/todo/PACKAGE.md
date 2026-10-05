# tool/todo

## What it is

Adapts `store/todo` to the loop's tool surface: session attribution from
the threaded ctx; replies exactly as the store shapes them. The adapter
owns one question: *whose queue is this call*, answered by the required
`scope` and nowhere else — the reserved word `global` for the global
queue (`store/todo.Global`), otherwise a project directory resolved
through `store/todo.ProjectOf` (`scope.Key`/`scope.Label` inside): a
subdirectory and a second worktree reach the repo's one queue, a
non-repo directory its own workspace. A call without `scope` refuses
naming the rule — there is no cwd fallback and no session binding: the
parameter is the binding, and a session that reads three repos names
the scope each call means.

`~` is expanded at the `middleware/paths` boundary. The `start` and
`claim` echoes carry `· scope <path>` (or `· scope global`) on the
row's details, so the reply names where the work lives.

## What it includes

- `Tool`: a `core.Tool` over the todo store's verbs, including the
  1.3.9 swarm surface: `claim`, `note`, `notes`, `accept`, `reject` and
  the `status: review` claim filter. `New` takes a `Mode` set once in
  main.go from the frontend kind (`-p` is a worker, everything else is
  interactive): it is the gate's switch, passed to the store's
  `Complete` as the worker flag.

## How it is consumed

- Registered at the root as a native tool.

## Gotchas

- Replies are the store's shapes, verbatim: the adapter does not
  re-voice; the store's teaching refusals carry the protocol.
- The link fields are `id (tN) | exact text | position | null`; an empty
  string is the same as omitting the field (no edge, no refusal), because
  a model filling every field with `requires: ""` was refused with `not
  found` and then believed a link needed a second call. A link may name a
  sibling task's exact text in the same create: `resolveDep` runs over
  the batch, so one create of `{text: "gate"}` and `{text: "work",
  requires: "gate"}` links t2 to t1. A bare number, as a JSON number or a string (2.11.9), is the sibling's
  1-based position in that create, tried last (2.1.9): a small model
  numbering its plan wrote `requires: "1"`, was refused, and fell back to
  one create per task. The `not found` refusal names the link forms once.
  `null` clears an existing link.
- Outside a repo a scope'd write lands in the directory's own workspace:
  every session working there shares the queue, and claim is the door. A
  session with no id at all (`anon`) attributes to the shared `anon`, so
  no caller's claim leaks onto another's.
- The `scope` parameter is per call: nothing in the session moves when a
  call names another workspace, and the operator moves the session itself
  with `/project <path>` (`command`), not with a todo call.
- `prune` is the door for the done rows the summary keeps counting; the
  log keeps them.
- Complete on your own unclaimed pending task implicitly claims and
  submits (auto-started); foreign-claim and blocked-by-dependency
  refusals carry through unchanged. In Interactive mode complete lands
  done in one call; in Worker mode it ends in review and the parent's
  `accept` (or `reject` with the reason) finishes it. Accept and reject
  auto-claim an unowned review task, so the parent's flow is read then
  accept/reject with no claim step; a foreign hold still refuses. Notes
  never need the hold.
- Complete and start are idempotent at the state they ask for (2.1.7):
  complete on a done task and start on a task already in progress by
  this session (or unowned) reply with the echo a fresh call would give
  — the row and the queue summary — no error, no event. A foreign start
  of an owned task still refuses naming the claimer.
- The Worker-mode board door is at the tool's seam: the six
  board-transition verbs refuse there, so the store's own arms (the
  swarm controller calls the store directly) stay as they are and the
  spawned worker records findings instead of moving the board.
- The read contract is the lean one (SPEC_TODO_LEAN): read returns the
  present (open work first — failed included — then related and
  recent done, ten rows total, the hint naming what is hidden), read
  all:true returns the history (the operator's read), finished lists
  the n most recent done (default 10, cap 100), read with id renders one task
  summary-only and points at `notes`, and a transition echo is the
  affected row plus the summary; never the full queue. Create keeps
  the full present because after a merge the whole queue is the news.
  Read no longer inlines note text: a task with notes shows `· N notes`
  and the `notes` action lists them with their session and time; the
  swarm brief's `TaskInfo` still carries the full notes.
- The description is shape only (SPEC_STREAMLINE 1): the state machine,
  the claim rules, and the compaction rule ride the store's voices; the
  replies teach on contact, the standing context does not double-teach.
  Inside that shape the description decides and the fields explain: the
  one line of what the tool is, the verbs, and the reply's shape live in
  the description, and each field's description says what that field is,
  in the plain words the house uses. `blocks` is a field, not a sentence:
  it is described on its own, like `requires`, and no link's meaning is
  folded into the description's prose. The planning rule is repeated in
  the description on purpose: the system prompt decides when (its one
  sentence is read every turn), and the description is there when the
  model arrives at the tool.
