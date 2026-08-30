# store/rem/metadata

## What it is

Hand-written metadata for the rem store: the containers SPEC_STATE's
"### rem" section fixes; memories (the record), trigrams (the fuzzy
arm's shadow), and meta (versions and the id-minting counter). Lift's
four-tag grammar is the language. Nullable columns are pointers.

## How it is consumed

- Consumed by the `gen` tooling to project the generated `ddl`/`domain`
  accessors; not part of the runtime path.

## Gotchas

- This is source for generated code: edit it and regenerate; the
  generated `ddl`/`domain` projections are derived and not hand-edited.
- The sqlite camera leaves foreign keys off, so referential integrity is
  eventual; the SET NULL behaviour lives in the store's prune.
