# store/scheduler

## What it is

The background-jobs store, Go over the generated substrate. SPEC_STATE's
"### scheduler" section is the spec. The event log is the spine; jobs is
a replayable projection rebuilt from the log inside every transaction and
never trusted. Removed jobs stay as tombstones so ids and names are never
reused and remove survives compaction. Runs are structured records in
their own container (SPEC_STATE's deviation): runs reads are chain reads
over it and run history survives compaction. Crontab is the scheduling
truth: tagged lines, surgical rewrites, foreign lines byte-identical,
written before the store commit; drift is surfaced in list.

## What it includes

- `scheduler.go` — the package doc, `SchemaVersion`, `DDL`, `Statements`,
  the `DB` alias.
- `cron.go` — the vixie cron parser and matcher.
- `crontab.go` — the tagged-lines crontab edit/merge.
- `verbs.go` — the command verbs (list/create/pause/resume/remove/runs).
- `runner.go` — the job runner (the worker spawn, bwrap jail, socket
  proxy).
- `jail.go` — the bwrap jail argv composition.
- `proxy.go` — the unix-socket proxy (the jail's one hole).
- `fold.go` — the fold/replay over the event log.
- `render.go` — the list/rendering.
- `metadata/scheduler.go` — hand-written metadata.

## How it is consumed

- `tool/scheduler` and the `command` scheduler verb call the store's
  operations; `cmd/rig` wires `runJob` through `runner.go` for the jailed
  worker.
- `store.Open` is applied with the scheduler's `DDL()`/`SchemaVersion`.

## Gotchas

- Jobs are a replayable projection: never trusted, rebuilt from the log
  inside every transaction.
- Removed jobs stay as tombstones (ids and names are never reused, remove
  survives compaction).
- Runs are chain reads over their own container; run history survives
  compaction (an event-args-only shape would have dropped it).
- Crontab is written before the store commit; drift is surfaced in list.
