# tool/rem

## What it is

Adapts `store/rem` and `store/graph` to the loop's tool surface: runtime
shape checks loud at execute; session attribution from the threaded ctx
(`memories.source` defaults to the calling session id, `anon` when
unthreaded, and accepts free text when the caller passes one); replies
exactly as the stores shape them. The description carries the contract
sentences (SPEC_STATE: rem is deliberate — every rem operation is a
choice, nothing is written by a compaction, nothing is read into the
prompt by a session start; SPEC_GRAPH: pack before grepping for who
calls what).

## What it includes

- `Rem`: the tool as its interface (2.12.6): `tool.Definition`, `Exec` as
  the one JSON door — decode `action` and the fields, turn the wire's
  shapes that Go cannot name (`supersedes`, `ids`) into ids, route — and
  one method per verb: `Index(ctx, scope)`, `Pack(ctx, scope, target)`,
  `Learn(ctx, scope, content, kind, importance, source, supersedes)`,
  `Recall(ctx, scope, query, kind, k, includeSuperseded)`,
  `Reflect(ctx, scope, content, importance, source)` and
  `Prune(ctx, scope, verb, kind, ids, olderThanDays, importance)`, each
  over the rem store's operations and the graph store's map operations,
  each carrying the required `scope`: the
  reserved word `global` or a directory path, resolved through
  `store/scope` (worktree-safe; `~` expands at the `middleware/paths`
  boundary). The scope resolution, the `k`/`older_than_days`/`importance`
  bounds and the `pack`/`learn`/`reflect` required-field refusals live in
  the verb they belong to, so a Go caller and the model hit the same
  checks. A call without it refuses naming the rule — there is no
  cwd fallback in the tool — and a path that is not a directory refuses
  by name, the same words todo's scope refuses: a typo must not mint a
  memory scope keyed by a path that is not there. A path recall searches that project first
  and fills from global, as ever; a global learn, recall or prune is
  the global memory alone; `index` and `pack` refuse `global` by name
  (a map needs a directory). `pack` takes a
  `target`: a symbol (package-qualified or bare), a file path, or a task
  as a sentence (a target with a space that names no file) — the symbol
  and the file reply from the live files, the task replies with the
  candidates the map's lexical arms surface, the decision server's yes
  set loaded live and the declines listed by name (2.10.0). `index`
  maps the whole project through the queue.

## How it is consumed

- Registered at the root as a native tool, with the rem store's db and
  the graph queue. The `/rem` command (SPEC_COMMANDS 11) is the
  operator's verb surface over the memory store; the tool stays the
  model's multi-line surface over both stores.
- `Guide` is the one-link `ToolMiddleware` whose `Guidelines()` joins
  the system prompt when the decision server stands: in a mapped
  project, pack the task before reading files for it.
- The compaction `AutoReflect` seam is cut: compaction writes nothing to
  rem (SPEC_COMPACT 6).

## Gotchas

- Session attribution comes from the threaded `core.SessionFrom`: an
  unthreaded call attributes `anon`.
- `pack` registers no observation: it writes no file state into the
  session, so an edit's drift check still demands a real read after a
  pack.
- `index` never waits: the reply names the queued count and the map
  fills as the queue drains; a pack on a not-yet-mapped symbol says so.
