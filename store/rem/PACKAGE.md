# store/rem

## What it is

The memory store, Go over the generated substrate (SPEC_STATE's "### rem"
section). Writes land through the generated accessors inside one
serializable transaction per operation; the store owns a small named raw
surface (the natural-key dedup seek, the recall arms, the browse
ordering, the prune selection, the supersession-clearing UPDATE, and the
fts rowid bookkeeping).

## What it includes

- `rem.go` — the store: write/read operations, id minting, the raw
  statements, prune and supersession.
- `recall.go` — the pure core: consolidation arithmetic, the lexical
  shapes of the two arms (FTS and trigram), reciprocal rank fusion
  (RRF, k=60). Zero I/O.
- `recall_db.go` — recall's DB-level path: the two named raw arms, fusion
  over their rankings, browse, and the effective-strength blend.
- `metadata/rem.go` — hand-written metadata for the rem store.

## How it is consumed

- The `tool/rem` and the root's remembered segment call the store's
  read/write operations; `policy/compact`'s `AutoReflect` seam hands
  summaries to the store's reflection entry.
- Ids are minted from a meta counter inside the caller's transaction:
  strictly increasing, never reused (the AUTOINCREMENT rule, kept by
  minting).

## Gotchas

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
