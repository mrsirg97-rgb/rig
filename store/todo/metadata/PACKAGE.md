# store/todo/metadata

## What it is

Hand-written metadata for the todo store: the containers SPEC_STATE's
"### todo" section fixes; tasks, task_deps, and meta. Lift's four-tag
grammar is the language. Nullable columns are pointers.

`extra.sql` carries the two indexes the DDL camera cannot emit (the
text unique index and the ordering spine), both scoped per queue. The
`session_project` table that lived here beside them (the session's
queue binding) left in 2.12.0: the required `scope` parameter is the
binding, and stores that still carry the table carry it unread.

## How it is consumed

- Consumed by the `gen` tooling to project the generated `ddl`/`domain`
  accessors; not part of the runtime path.

## Gotchas

- Source for generated code: edit and regenerate; the generated
  projections are derived, not hand-edited.
- The sqlite camera leaves foreign keys off: referential integrity is
  eventual.
