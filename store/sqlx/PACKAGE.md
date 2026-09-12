# store/sqlx

## What it is

The stdlib `sql` seam the generated stack executes through: a `DB` wraps
the pool, and `Tx` opens the serializable transaction that rides the
context. One constructor, one isolation, one read of the context.

## What it includes

- `DB`: wraps the stdlib pool; `Tx` (serializable) and `TxReadOnly`
  (serializable read-only) open a transaction and land it on the returned
  context under a typed key.
- `TxFrom`: the one read of the context the cameras make.

## How it is consumed

- The mesh fold and write routes call `DB.Tx`: GET routes take
  `TxReadOnly` (a read-only serializable snapshot).
- Generated accessors read the tx off the ctx (`TxFrom`) and scan their
  rows through the `lazy` seam.

## Gotchas

- `TxFrom` on an unbound context refuses loudly ("sqlx: no transaction
  bound, call DB.Tx first"); the stack fails closed on an unbound
  request.
- `Tx` and `TxReadOnly` wait out a `SQLITE_BUSY` begin: the driver's own
  busy timeout is one attempt, the seam retries with a doubling backoff
  while the caller's context lives, capped at thirty seconds total. The
  busy match reads the driver's typed error code first
  (`TestTheBusyRefusalCarriesTheDriverCode`) and the message second. A
  concurrent writer burst serializes instead of failing; the final
  refusal names the wait and the underlying busy error.
- The tx rides a typed, unexported context key (`txKey`), not a string, so
  no other value can collide with it.
