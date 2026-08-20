# store/sqlx

## What it is

The stdlib `sql` seam the generated stack executes through: a `DB` wraps
the pool, and `Tx` opens the serializable transaction that rides the
context. sqlite and postgres both speak database/sql (postgres through
pgx/v5/stdlib), so the cameras never learn which — one constructor, one
isolation, one read of the context.

## What it includes

- `DB` — wraps the stdlib pool; `Tx` (serializable) and `TxReadOnly`
  (serializable read-only) open a transaction and land it on the returned
  context under a typed key.
- `TxFrom` — the one read of the context the cameras make.
- `ArrayScanner[T]` — adapts a `[]T` destination so database/sql can fill
  it (postgres array literal or JSON).

## How it is consumed

- The mesh fold and write routes call `DB.Tx`; GET routes take
  `TxReadOnly` (postgres skips the SSI predicate-lock bookkeeping for a
  READ ONLY serializable snapshot).
- Generated accessors read the tx off the ctx (`TxFrom`) and pass
  `ArrayScanner` destinations straight through to `Scan`.

## Gotchas

- `TxFrom` on an unbound context refuses loudly ("sqlx: no transaction
  bound, call DB.Tx first") — the stack fails closed on an unbound
  request.
- The tx rides a typed, unexported context key (`txKey`), not a string, so
  no other value can collide with it.
- `ArrayScanner.Scan` handles three inputs: postgres array literals
  (`{"a","b"}`), JSON (`["a","b"]`), and nil (clears the slice). The
  element-conversion switch is the price of a single generic adapter;
  slice columns are rare and this runs once per scanned row.
- `parseArrayLiteral` tracks quoted strings and escapes; an unterminated
  escape refuses.
