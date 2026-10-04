# store/rem

## What it is

The memory store, Go over the generated substrate (SPEC_STATE's "### rem"
section). Writes land through the generated accessors inside one
serializable transaction per operation; the store owns a small named raw
surface (the natural-key dedup seek, the recall arms, the browse
ordering, the prune selection, the supersession-clearing UPDATE, and the
fts rowid bookkeeping).

Rem is deliberate (SPEC_STATE): every rem operation is something chose:
the model learns/recalls/reflects/prunes through its tool, the operator
prunes through the `/rem` verb; nothing is written by a compaction and
nothing is read into the prompt by a session start.

## What it includes

- `base.go`: the shared substrate (raw statements, the repo scope, the
  id mint, supersession), `learn.go`: the write/read operations,
  `prune.go`: prune and consolidation, `read.go`: the `/rem` command's
  reads (`List`, `Show`, `Forget`), `render.go`: the hit render,
  `migrate.go`: the one-time migration.
- `path.go`: `FilePath(home)`, the store's file: `<home>/rem/rem.sqlite`.
- `recall.go`: the pure core: consolidation arithmetic, the lexical
  shapes of the two arms (FTS OR-union and trigram), reciprocal rank
  fusion (RRF, k=60). Zero I/O.
- `recall_db.go`: recall's DB-level path: the two named raw arms, fusion
  over their rankings, browse, and the effective-strength blend.
- `metadata/rem.go`: hand-written metadata for the rem store.

## How it is consumed

- `tool/rem` calls the read/write operations: the `/rem` command's
  closures (`List`/`Show`/`Forget`) are wired at the root
  (SPEC_COMMANDS 11). The compaction reflection seam is cut.
- Ids are minted from a meta counter inside the caller's transaction:
  strictly increasing, never reused (the AUTOINCREMENT rule, kept by
  minting). The mN id is a local handle, not a global identity: a mesh
  converges rows on content_sha256, which learn is already idempotent
  on, and each node mints its own mN.

## Gotchas

- Scope is a repo identity, not a cwd: `scopeKey(cwd)` hashes the
  absolute git common dir (two worktrees of one repo share one memory)
  or the cwd itself outside a repo. The `scopePath` git probe is memoized
  per cwd (pure, deterministic); a relative common dir resolves against
  the cwd, and an echoed option (old git passes unknown flags through,
  exit 0) is not a path; the cwd stands in.
- The deliberate project (SPEC_STATE) is resolved at the tool adapter,
  not here: `tool/rem` hands the store a `project` path as its `cwd`, so
  `writeScope`/`readScopes` are unchanged; the project a fact belongs
  to is a choice, not an accident of where rig started.
- The schema bump (1 → 2) carries `Migration(cwd)`: a one-time idempotent
  re-scope of rows under the old cwd-hash to the repo's, and a file-wide
  removal of `source = 'session compaction'` rows (never deliberate),
  counted once on stderr. The per-cwd re-scope is keyed on a `meta`
  marker (`migrated:<oldScope>`, `INSERT OR IGNORE`), so a shared file's
  other cwds migrate on their own next open and two openers racing the
  first migration both succeed; the whole step runs in `store.Open`'s
  migration transaction.
- `Forget(ctx, db, cwd, id)` removes only this project's or a global row;
  ids are file-wide, so another project's id is `ErrOtherProject`, named
  with its label. Supersedes targets are scoped the same way: a
  cross-project target refuses by name before any row is touched.
- Recall's effective computation uses exactly the consolidation inputs, so
  the two paths agree: effective-at-recall equals what consolidate would
  persist, and consolidating later cannot double-count.
- Recall and consolidation refuse a memory whose last_consolidated_at is
  unparseable or in the future (`rem: mN has an unreadable
  last_consolidated_at; prune it by id`); an empty one is never, not
  corruption.
- Recall is a write: it reinforces its hits inside its own transaction,
  so recalls serialize with learns; if a swarm ever contends on it,
  record the access after the read rather than dropping it.
- The trigram arm uses the pg_trgm convention (two-space padding): the
  fuzzy arm enforces a minimum absolute overlap and a containment floor.
  The tokenizing, the grams and the OR-query are `store/fts`'s — the
  one contract `store/graph` shares.
- Fusion is reciprocal rank (k=60) over the arms' rankings, deduped by
  memory id, annotated with the reaching arm. The fts arm ORs its tokens
  and orders by FTS5's own rank (bm25), so a long query degrades by
  relevance instead of to nothing; reserved words stay quoted, the arm's
  cap and the scope and kind filters are unchanged. The natural-key dedup
  digest is sha256, and the v2->v4 migration rehashes legacy rows and
  renames the column (`content_md5` -> `content_sha256`) so the field,
  the column, and the unique index all agree.
- The fts virtual table is not a container the grammar speaks, so its
  rowid bookkeeping is a named raw statement.
- Supersession pairs a nullable alias with its self-link (the pairing that
  keeps the FK out of the generated INSERT); the SET NULL behaviour lives
  in the store's prune.
- `FilePath(home)`: one file under `<home>/rem`, scoped by a column inside
  (SPEC_STATE); the root and the dashboard share it (SPEC_SERVE 2).
