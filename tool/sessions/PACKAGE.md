# tool/sessions

## What it is

Adapts the session-state store (`store/state`) to the loop's tool surface:
the soak's own vitals, read-only. `list` is the recent sessions, newest
first, one line each (short id, started, model, version, turns, faults);
`summary` is the vitals over the same slice; session and turn counts, the
models with their versions, the fault count with the last fault's first
line, and the aggregate cache ratio (cache_read over prompt, the status
row's arithmetic). It reuses the store's typed verbs (`ListSessions`,
`SessionUsage`, `SessionFaults`) and opens the project's state file itself,
so it reads any workspace, not only the session's own.

## What it includes

- `Tool`: a `core.Tool` over the state store's read verbs.

## How it is consumed

- Registered at the root as a native tool (`sessions`, the eighteenth).
  Read-only: it is absent from the root's `mutatingNatives`, so the
  approval gate passes it silently. It is also absent from
  `concurrentNatives`; it opens a store, like `todo`/`rem`/`scheduler`,
  so it is not a pure observation.

## Gotchas

- The project is a path, resolved to the state file the way the root does
  (`state.StorePath`), defaulting to the session cwd; `~` expands at the
  `middleware/paths` boundary. One state file per workspace, so a
  subdirectory and a second worktree read their own files, not the repo's
  one (unlike todo/rem, which are repo-scoped).
- A read that finds no state file answers the named empty without creating
  one (`(no sessions in <label>)`, SPEC_CORE); the label is
  `scope.Label` of the path.
- The slice is capped at `state.ListCap` (50): `n` below 1 or above the
  cap refuses by name. The cache ratio is the integer percentage the TUI
  status row computes (`cache_read*100/prompt`, 0 when prompt is 0).
- `summary` fans out over the slice: one `SessionUsage` and one
  `SessionFaults` per session, then aggregates in the tool (the store
  verbs stay per-session; the aggregation is the tool's, as the usage
  total is on the dashboard).
