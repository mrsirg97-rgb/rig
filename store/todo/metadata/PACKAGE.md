# store/todo/metadata

## What it is

Hand-written metadata for the todo store: the containers SPEC_STATE's
"### todo" section fixes; tasks, task_deps, and meta. Lift's four-tag
grammar is the language. Nullable columns are pointers.

`session_project` (the session's queue binding) is not a generated
container: it is mutable state beside the log, created by `extra.sql` and
read and written with plain SQL from `binding.go`. Generating it would
put a derived projection in the same breath as the spine it points at.

## How it is consumed

- Consumed by the `gen` tooling to project the generated `ddl`/`domain`
  accessors; not part of the runtime path.

## Gotchas

- Source for generated code: edit and regenerate; the generated
  projections are derived, not hand-edited.
- The sqlite camera leaves foreign keys off: referential integrity is
  eventual.
