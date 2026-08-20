# store/scheduler/metadata

## What it is

Hand-written metadata for the scheduler store: the containers SPEC_STATE's
"### scheduler" section fixes — jobs, runs, and meta. Lift's four-tag
grammar is the language. Nullable columns are pointers.

## How it is consumed

- Consumed by the `gen` tooling to project the generated `ddl`/`domain`
  accessors; not part of the runtime path.

## Gotchas

- Source for generated code — edit and regenerate; the generated
  projections are derived, not hand-edited.
- The sqlite camera leaves foreign keys off; referential integrity is
  eventual.
