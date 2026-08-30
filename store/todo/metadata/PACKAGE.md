# store/todo/metadata

## What it is

Hand-written metadata for the todo store: the containers SPEC_STATE's
"### todo" section fixes; tasks, task_deps, and meta. Lift's four-tag
grammar is the language. Nullable columns are pointers.

## How it is consumed

- Consumed by the `gen` tooling to project the generated `ddl`/`domain`
  accessors; not part of the runtime path.

## Gotchas

- Source for generated code: edit and regenerate; the generated
  projections are derived, not hand-edited.
- The sqlite camera leaves foreign keys off: referential integrity is
  eventual.
