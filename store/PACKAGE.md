# store

## What it is

The sqlite persistence layer: every store package names its context with
`DB` (a `sqlx.DB` alias) and opens its own schema file. The substrate is
the generated accessors (the `ddl`/`domain` projections — generated, not
edited); this package owns the handwritten open path and the `sqlx`/`lazy`
seams the generated stack executes through.

## What it includes

- `Open(path, statements, wantVersion)` — opens (or creates) the sqlite
  file, applies the pragmas, reads integrity, checks `meta.schema_version`
  against `wantVersion`, then applies the schema statements in order.
- `DB = sqlx.DB` — the store handle.
- `lazy` — the deferred result under direct accessor execution.
- `sqlx` — the stdlib `sql` seam: serializable transactions on the ctx,
  and the array scanner.

## How it is consumed

- Each store (`rem`, `scheduler`, `state`, `todo`) calls `store.Open` at
  construction with its `DDL()` and `SchemaVersion`; the caller surfaces a
  quarantined file.
- The generated domain accessors read the bound tx off the context
  (`sqlx.TxFrom`) and hand results back as `lazy.Lazy`.

## Gotchas

- The pragmas ride the DSN (`_pragma=…`), applied by the driver at
  connection init — a pragma `Exec`'d against one pooled connection would
  leave every other connection without it. The cross-process posture (a
  runner writing while a session reads) is WAL + busy_timeout +
  `_txlock=immediate` (every transaction takes the RESERVED lock at begin).
- An existing corrupt file is quarantined aside as `<path>.corrupt-<ts>`
  and a fresh one created; quarantined names the aside so callers surface
  it. Never silently truncated. A schema/policy error surfaces and is
  never quarantined.
- `apply` inserts `schema_version` on first open; a mismatch refuses
  loudly naming both.
