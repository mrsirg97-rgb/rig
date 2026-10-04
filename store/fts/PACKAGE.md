# fts

The one FTS tokenization contract: the words a query splits into, the
padded trigrams a memory's shadow is sought by, and the OR-query the
FTS5 table reads. Pure functions, stdlib-only, no store behind them —
`store/rem` (the recall arms) and `store/graph` (the task pack's
lexical arm) both shape their text through this one leaf, which is why
a token, a gram or a reserved operator means the same thing in both
stores.

## How it is consumed

- `Tokenize` lowercases and splits on anything outside `[a-z0-9]`;
  empty tokens never come back.
- `GramsOfWord` pads a word with two spaces each side and slides a
  3-gram across it; `GramsOf` runs it per token and deduplicates
  in order.
- `Query` joins the tokens with ` OR `, quoting FTS5's reserved
  operators (`and`, `or`, `not`) so a remembered title never reads as
  a boolean.
