# store/decision/metadata

## What it is

Hand-written metadata for the decision store: the containers SPEC_DECISION
fixes — decisions (the row per decision) and meta (versions). Lift's
four-tag grammar is the language. Nullable columns are pointers.

`extra.sql` carries the two indexes the camera cannot emit: the pending
drain reads by status, the learning reads scan a scope in time order.

## How it is consumed

- Consumed by the `gen` tooling to project the generated `ddl`/`domain`
  accessors; not part of the runtime path.

## Gotchas

- Source for generated code: edit and regenerate; the generated
  projections are derived, not hand-edited.
- The `question` column is the typed question's JSON; `state` is the
  site's own JSON. Neither is parsed here.
