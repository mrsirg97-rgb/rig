# store/lazy

## What it is

The deferred result under direct accessor execution: a domain runs the
query on the tx and hands back a filled `Lazy`; the result decorated,
the error carried through the fill path.

## What it includes

- `ScanRow`: the minimal row the accessors scan (one `Scan` method,
  satisfied by stdlib and pgx rows); the scanners stay decoupled from the
  wire layer.
- `Lazy[T]`: `New`, `Fill` (single row), `FillAll` (a whole result),
  `Row`, `Rows`.

## How it is consumed

- The generated accessors construct a `Lazy` with a `Scan` closure, run
  the query on the tx, then `Fill`/`FillAll` the result.
- A nil row with a nil error is an absent read, matching domain `get`
  semantics.

## Gotchas

- Reading `Row`/`Rows` before the flush (not filled, no error) refuses
  loudly: "lazy: read before the flush". Fail closed on an unbound read.
