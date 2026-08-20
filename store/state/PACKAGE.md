# store/state

## What it is

The session-state store, Go over the generated substrate (SPEC_STATE).
The recorder is an observing Frontend that appends state rows for what the
loop already emits; the read side rebuilds a session from the log.

## What it includes

- `state.go` — `SchemaVersion`, `DDL`, `Statements`, the `DB` alias.
- `recorder.go` — the observing `core.Frontend`: forwards every
  Input/Notify untouched, appends rows for the loop's events.
- `resume.go` — the read-side projection: rebuild a `core.Session` from
  the log (SPEC_HARDENING decision 5).
- `sessions.go` — the sessions list rows (`ErrNoSuchSession`,
  `SessionRow`).
- `metadata/state.go` — hand-written metadata.

## How it is consumed

- The root wires `state.Recorder` as the kernel's `core.Frontend` (the
  loop's Notify sink); `-resume` uses the projection to rebuild a session.
- `command/sessions` reads `SessionRow`s and builds its voice on
  `ErrNoSuchSession` (`errors.Is`).

## Gotchas

- The recorder sources its rows from the loop's events and forwards them
  to the inner frontend; it must not double-record or drop events.
- A turn starts with a user message; the defined turns count comes from
  the row's lifecycle.
