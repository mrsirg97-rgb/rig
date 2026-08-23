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

- `scheduler.go` — the package doc, `SchemaVersion`, `Statements`,
  the `DB` alias.
- `cron.go` — the vixie cron parser and matcher.
- `crontab.go` — the tagged-lines crontab edit/merge.
- `verbs.go` — the command verbs (list/create/pause/resume/remove/runs)
  over the one `global.sqlite`; the crontab key is `jN` for every job,
  `name` unique store-wide, ids one sequence.
- `migration.go` — the one-time schema-1→2 migration: folds every
  `cwd-<hash>.sqlite`'s live jobs into `global.sqlite` (re-minted ids,
  runs re-keyed, crontab lines rewritten from `cwd-<hash>:jN` to the new
  `jN`), moves the old files aside as `<hash>.sqlite.migrated`, and is a
  no-op on the second open (no `cwd-*.sqlite` remains).
- `runner.go` — the job runner (the worker spawn, bwrap jail, socket
  proxy).
- `delegate.go` — the one-shot worker spawn (SPEC_DELEGATE): the busy
  rule, the ad-hoc record (a minted job row with no crontab line), the
  state-store bind for the resumable transcript, the one-in-flight
  flock and the no-recursion marker.
- `jail.go` — the bwrap jail argv composition.
- `proxy.go` — the unix-socket proxy (the jail's one hole).
- `fold.go` — the fold/replay over the event log.
- `render.go` — the list/rendering: one list grouped by each job's own
  `cwd` (this directory first, then the rest by path), the empty store
  named (`scheduler: no jobs (global.sqlite)`).
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
- One store, `global.sqlite`: `cwd` is a job field (where it runs and how
  the list groups), not a storage partition; `ParseKey` accepts `jN` only
  (the migration rewrites the old `cwd-<hash>:jN` crontab keys).
