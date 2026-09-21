# tool/todo

## What it is

Adapts `store/todo` to the loop's tool surface: session attribution from
the threaded ctx; replies exactly as the store shapes them. The adapter
owns one question: *whose queue is this call*, answered in a fixed order
and nowhere else —

1. the `project` field, if given: it resolves the queue (repo scope via
   `store/scope`, else the directory's own bucket). On a write it then
   binds the session, once the action succeeded (`→ bound to <label>`);
   on a `read` it is a peek and the session stays where it was; the
   `bind` action is the declaration and records whatever the read does;
2. else the session's recorded binding (`session_project`);
3. else the launch directory, when it is a repo;
4. else the launch directory's bucket, where a write refuses with the
   rule and a read answers labelled.

`~` is expanded at the `middleware/paths` boundary. Nothing is inferred
from the paths a call names: a session that reads three repos keeps its
plan in one queue.

## What it includes

- `Tool`: a `core.Tool` over the todo store's verbs.

## How it is consumed

- Registered at the root as a native tool.

## Gotchas

- Replies are the store's shapes, verbatim: the adapter does not
  re-voice; the store's teaching refusals carry the protocol.
- `project` is resolved through `store/todo.ProjectOf` (`scope.Key`/
  `scope.Label` inside): a subdirectory and a second worktree reach the
  repo's one queue, a non-repo directory its own bucket. Naming it on a
  write binds the session — so a session launched in `~` can work one
  repo's queue by naming it once — while naming it on a read just reads:
  an agent glancing at a neighbour's queue does not move its own plan. A
  failed write changes nothing, the binding included; `bind` with no
  project reports where the queue is and touches nothing.
- Outside a repo a bare write refuses (`todo: no project: … is not a
  repo …`) rather than quietly filling a bucket every session on that
  directory shares; reads stay open. A session with no id at all (`anon`)
  binds nothing: the attribution is shared, so a binding under it would
  leak one caller's project onto another's.
- `prune` is the door for the done rows the summary keeps counting; the
  log keeps them.
- Complete on your own unclaimed pending task implicitly claims and
  completes (auto-started); foreign-claim and blocked-by-dependency
  refusals carry through unchanged.
- The read contract is the lean one (SPEC_TODO_LEAN): read returns the
  actionable queue (done folds into the summary line), read all:true
  returns the history, and a transition echo is the affected row plus
  the summary; never the full queue. Create keeps the full (filtered)
  queue because after a merge the whole queue is the news.
- The description is shape only (SPEC_STREAMLINE 1): the state machine,
  the claim rules, and the compaction rule ride the store's voices; the
  replies teach on contact, the standing context does not double-teach.
