# store/rem

## What it is

The memory store, Go over the generated substrate (SPEC_STATE's "### rem"
section). Writes land through the generated accessors inside one
serializable transaction per operation; the store owns a small named raw
surface (the natural-key dedup seek, the recall arms, the browse
ordering, the prune selection, the supersession-clearing UPDATE, and the
fts rowid bookkeeping).

Rem is deliberate (SPEC_STATE): every rem operation is something chose —
the model learns/recalls/reflects/prunes through its tool, the operator
prunes through the `/rem` verb; nothing is written by a compaction and
nothing is read into the prompt by a session start.

## What it includes

- `rem.go` — the store: write/read operations, id minting, the raw
  statements, prune and supersession, the repo scope, the migration, and
  the `/rem` command's reads (`List`, `Show`, `Pin`, `Forget`).
- `recall.go` — the pure core: consolidation arithmetic, the lexical
  shapes of the two arms (FTS and trigram), reciprocal rank fusion
  (RRF, k=60). Zero I/O.
- `recall_db.go` — recall's DB-level path: the two named raw arms, fusion
  over their rankings, browse, and the effective-strength blend.
- `metadata/rem.go` — hand-written metadata for the rem store.

## How it is consumed

- `tool/rem` calls the read/write operations; the `/rem` command's
  closures (`List`/`Show`/`Pin`/`Forget`) are wired at the root
  (SPEC_COMMANDS 11). The compaction reflection seam is cut.
- Ids are minted from a meta counter inside the caller's transaction:
  strictly increasing, never reused (the AUTOINCREMENT rule, kept by
  minting).

## Gotchas

- Scope is a repo identity, not a cwd: `scopeKey(cwd)` hashes the
  absolute git common dir (two worktrees of one repo share one memory)
  or the cwd itself outside a repo. The `scopePath` git probe is memoized
  per cwd (pure, deterministic); a relative common dir resolves against
  the cwd, and an echoed option (old git passes unknown flags through,
  exit 0) is not a path — the cwd stands in.
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
  with its label.
- Recall's effective computation uses exactly the consolidation inputs, so
  the two paths agree: effective-at-recall equals what consolidate would
  persist, and consolidating later cannot double-count.
- The trigram arm uses the pg_trgm convention (two-space padding); the
  fuzzy arm enforces a minimum absolute overlap and a containment floor.
- Fusion is reciprocal rank (k=60) over the arms' rankings, deduped by
  memory id, annotated with the reaching arm.
- The fts virtual table is not a container the grammar speaks, so its
  rowid bookkeeping is a named raw statement.
- Supersession pairs a nullable alias with its self-link (the pairing that
  keeps the FK out of the generated INSERT); the SET NULL behaviour lives
  in the store's prune.
- `FilePath(home)`: one file under `<home>/rem`, scoped by a column inside
  (SPEC_STATE); the root and the dashboard share it (SPEC_SERVE 2).
