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

- `Todo`: the tool as its interface (2.12.4), the template for every
  native tool. It embeds `tool.Definition` (the words), declares `Exec`
  (the one JSON door) and one typed method per verb: `Create(ctx, scope,
  item)`, `Claim`, `Start`, `Complete`, `Fail`, `Release`, `Retry`,
  `Move`, `Prune`, `Read`, `ReadAll`, `ReadOne`, `Finished`, `Note`,
  `Notes`, `Accept`, `Reject`, each taking the scope and the verb's own
  fields and reading the session from the ctx. `Exec` decodes the call
  and routes it; it holds no logic of its own, so a Go caller and the
  model reach the same verb through the same checks. The struct is
  unexported and `New` returns the interface. `New` takes a `Mode` set
  once in main.go from the frontend kind (`-p` is a worker, everything
  else is interactive): the board verbs refuse in a worker, and the
  store's `Complete` gets the worker flag.

## How it is consumed

- Registered at the root as a native tool.

## Gotchas

- Replies are the store's shapes, verbatim: the adapter does not
  re-voice; the store's teaching refusals carry the protocol.
- `create` takes one task (2.12.4): `text`, and `requires` / `blocks`
  as `id (tN) | null`. An empty string is the same as omitting the field
  (no edge, no refusal), because a model filling every field with
  `requires: ""` was refused with `not found` and then believed a link
  needed a second call. A number, a text or anything but a string id or
  null refuses at the door by name (`requires must be a task id (tN)
  from a reply, or null`). Through 2.12.3 a create took a `tasks` array
  whose links could be a sibling's text or position; GLM numbered its
  plan and wrote `t1`, `t3`, `t4` for its own steps, which linked to the
  queue's oldest tasks. The `not found` refusal names the one form once.
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
- The operator's verbs (2.14.1, SPEC_WORKERS 7) are the registry's:
  `prune`, `accept`, `reject` and `move` ride the entry's `operator`
  key, so a delegated worker's menu omits them and `policy/operator`
  refuses them before the tool runs — the two the board door already
  refused and the two (prune, move) that only the middleware stops. The
  switch here is untouched; a session without the delegate marker —
  interactive or a scheduled fire — keeps every verb.
- The read contract is the lean one (SPEC_TODO_LEAN): read returns the
  present (open work first — failed included — then related and
  recent done, ten rows total, the hint naming what is hidden), read
  all:true returns the history (the operator's read), finished lists
  the n most recent done (default 10, cap 100), read with id renders
  one task summary-only and points at `notes`, and every write, create
  included, echoes the affected row plus the summary; never the full
  queue — `read` is the queue, and the refused create's queue is the one
  exception (2.11.10). Read no longer inlines note text: a task with
  notes shows `· N notes` and the `notes` action lists them with their
  session and time; the
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
