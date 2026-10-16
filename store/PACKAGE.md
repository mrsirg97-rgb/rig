# store

## What it is

The sqlite persistence layer: every store package names its context with
`DB` (a `sqlx.DB` alias) and opens its own schema file. The substrate is
the generated accessors (the `ddl`/`domain` projections; generated, not
edited); this package owns the handwritten open path and the `sqlx`/`lazy`
seams the generated stack executes through.

## What it includes

- `Open(path, statements, wantVersion, migrate...)`: opens (or creates)
  the sqlite file, applies the pragmas, reads integrity, reads
  `meta.schema_version` and refuses before any statement runs when the
  file is newer than the build or older with no migration passed, then
  applies the schema statements in order and runs the migrations in one
  transaction (the version bump commits with them); the transaction
  re-reads `schema_version` inside itself and hands the fns the version
  it actually holds, so an opener whose captured version a concurrent
  winner moved past runs nothing and reports ''. Returns the migrations'
  report beside the handle.
- `DB = sqlx.DB`: the store handle.
- `lazy`: the deferred result under direct accessor execution.
- `sqlx`: the stdlib `sql` seam: serializable transactions on the ctx,
  and the array scanner.

## How it is consumed

- Each store (`rem`, `scheduler`, `state`, `todo`) calls `store.Open` at
  construction with its `DDL()` and `SchemaVersion`; the caller surfaces a
  quarantined file.
- The generated domain accessors read the bound tx off the context
  (`sqlx.TxFrom`) and hand results back as `lazy.Lazy`.

## Gotchas

- The pragmas ride the DSN (`_pragma=…`), applied by the driver at
  connection init, and ride it alone — a pragma `Exec`'d against one
  pooled connection would leave every other connection without it.
  The DSN splits at the first `?`, so a path carrying one opens a
  different file and Open refuses it, naming the path — rename the
  store rather than escape it. The cross-process posture (a
  runner writing while a session reads) is WAL + busy_timeout +
  `_txlock=immediate` (every transaction takes the RESERVED lock at begin).
- An existing corrupt file is quarantined aside as `<path>.corrupt-<ts>`
  and a fresh one created; quarantined names the aside so callers surface
  it. The `-wal`/`-shm` sidecars move with the main file (a fresh store
  must never adopt a stale WAL, and the quarantined pair is the
  operator's evidence): only a NotExist Stat skips the rename — any other
  Stat failure fails closed naming the path, never read as absence. Never silently truncated. The integrity probe
  is `PRAGMA quick_check` — every btree, not just the schema page — so
  page-level corruption quarantines too. A schema/policy error surfaces
  and is never quarantined.
- The migration transaction re-reads `schema_version` inside itself and
  hands the fns the version it holds, not the one captured before it
  began: two processes opening an old store concurrently apply each
  migration once, and the loser — whose captured version the winner
  moved past — runs nothing and reports ''. A fresh store minted at the
  build's version still runs them (the todo fold runs on a fresh store).
- `readVersion` inserts `schema_version` on first open: a mismatch refuses
  loudly naming both, and an upgrade without a migration is a mismatch.
