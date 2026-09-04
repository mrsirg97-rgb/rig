# store/state

## What it is

The session-state store, Go over the generated substrate (SPEC_STATE).
The recorder is an observing Frontend that appends state rows for what the
loop already emits; the read side rebuilds a session from the log.

## What it includes

- `state.go`: `SchemaVersion`, `DDL`, `Statements`, `Migration`, the `DB` alias.
- `recorder.go`: the observing `core.Frontend`: forwards every
  Input/Notify untouched, appends rows for the loop's events.
- `resume.go`: the read-side projection: rebuild a `core.Session` from
  the log (SPEC_HARDENING decision 5).
- `sessions.go`: the sessions list rows (`ErrNoSuchSession`,
  `SessionRow`) and `ListSessions(ctx, db, limit)` (the named limit,
  `ListCap` the default and the maximum).
- `faults.go`: `SessionFaults(ctx, db, sessionId)`, the typed read of
  one session's fault rows, newest first.
- `path.go`: `StorePath(home, cwd)`, the workspace state file: one
  file per cwd under `<home>/sessions`, keyed by the first six sha1
  bytes of the cwd.
- `usage.go`: `UsageRow` and `SessionUsage(ctx, db, sessionID)`, the
  typed usage read (prompt/completion/cache tokens per message,
  transcript order) the dashboard and the `sessions` tool build from.
- `metadata/state.go`: hand-written metadata.

## How it is consumed

- The root wires `state.Recorder` as the kernel's `core.Frontend` (the
  loop's Notify sink); `-resume` uses the projection to rebuild a session.
- `command/sessions` reads `SessionRow`s and builds its voice on
  `ErrNoSuchSession` (`errors.Is`); its `summary` verb and the `sessions`
  tool (`tool/sessions`) are thin adapters over `ListSessions`,
  `SessionUsage`, and `SessionFaults`; no SQL in either.

## Gotchas

- `tool_calls` is keyed `(session_id, message_seq, id)`: model-minted call
  ids are not globally unique (two sessions, or two turns of one session,
  can mint the same id), and the result write is scoped to the session,
  the message, and the id. `RecordToolCall`/`RecordToolResult` take all
  three. The v1 store's `(id)`-only key is migrated on open (`Migration`):
  the `session_id` column is backfilled from `messages` and the table
  rebuilt with the composite key. A duplicate id within one message is
  minted to `id-2`, `id-3` by the recorder (the wire id stays on the
  message row); results arrive in call order and are matched through that
  mapping.
- The recorder sources its rows from the loop's events and forwards them
  to the inner frontend; it must not double-record or drop events.
- The files upsert rides the wired `Snapshot` seam (the file tool's
  snapshot, set by the root): the recorder persists the session's
  recorded file states without racing the tools' concurrent records.
  Without the seam the upsert is skipped.
- A turn starts with a user message: the defined turns count comes from
  the row's lifecycle. `SessionRow` carries the model and version (so the
  list and the summary build from the same slice) and the fault count;
  the fault rows themselves come from `SessionFaults`. `ListSessions`
  takes the limit by name and `ListCap` (50) is the ceiling; a glance,
  not an archive.
- `StorePath(home, cwd)` resolves the workspace's state file the way the
  root does (SPEC_SERVE 2): one file per cwd under `<home>/sessions`,
  keyed by the first six sha1 bytes of the cwd; the root and the
  dashboard share this one source. `SessionUsage` is the typed usage
  read beside `Resume` (one row per landed turn, transcript order) the
  dashboard renders as structure. The generation line, kept here and
  disabled in code: `GOGEN=$PWD; cd ../../../lift/cmd && go run main.go
  -config=$GOGEN/gen.json -source=$GOGEN/source.json`.
