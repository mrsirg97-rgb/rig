# fts

The one FTS tokenization contract: the words a query splits into, the
padded trigrams a memory's shadow is sought by, the OR-query the FTS5
table reads, and the rank-fusion arithmetic both consumers score their
arms with. Pure functions, stdlib-only, no store behind them —
`store/rem` (the recall arms) and `store/graph` (the task pack's
lexical arm) both shape their text through this one leaf, which is why
a token, a gram or a reserved operator means the same thing in both
stores.

## How it is consumed

- `Tokenize` lowercases and splits on anything outside `[a-z0-9]`;
  empty tokens never come back.
- `gramsOfWord` pads a word with two spaces each side and slides a
  3-gram across it; `GramsOf` runs it per token and deduplicates
  in order.
- `Query` joins the tokens with ` OR `, quoting FTS5's reserved
  operators (`and`, `or`, `not`) so a remembered title never reads as
  a boolean.
- `RankWeight` is reciprocal-rank fusion's contribution —
  `1 / (ReciprocalRankK + rank)` over a 1-based rank — and
  `MinOverlap` is the fuzzy arm's floor: `max(FuzzyMinOverlap,
  ceil(FuzzyMinContainment · grams))`. Both callers arm their hits
  with 1-based ranks and fuse through these two functions only, so
  the k, the rank base and the containment floor cannot drift between
  the memory store and the code map.

## The deliberate tokenizer split

`store/rem`'s `memory_fts` ships `tokenize = 'porter unicode61'`;
`store/graph`'s `symbol_fts` ships bare `unicode61` (the fts5
default). The split is deliberate: recall matches prose, where the
porter stemmer carries an inflected query ("run" reaches "running");
the code map matches identifiers, which are matched verbatim, and the
fuzzy arm there is the trigram shadow, not stemming. Both tables'
inputs still flow through `Tokenize`/`GramsOf`/`Query` here — only the
index-side stemmer differs.
