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

- `scheduler.go`: the package doc, `SchemaVersion`, `Statements`,
  the `DB` alias.
- `cron.go`: the vixie cron parser and matcher.
- `crontab.go`: the tagged-lines crontab edit/merge.
- `verbs.go`: the command verbs
  (list/create/update/pause/resume/remove/runs) over the one
  `global.sqlite`; the crontab key is `jN` for every job, `name` unique
  store-wide, ids one sequence. `Create` takes the model from the
  caller (there is no package default anymore): an empty model refuses,
  naming the fleet's model and the job's own — unless the create carries
  `command` (a shell line run by `sh -c` in the job's cwd instead of a
  worker prompt), which refuses `model` and `busy` and needs neither.
  `timeout` (minutes, 1..1440, refused outside the range by name) bounds
  one fire and rides the create and update events; on update it is
  optional — absent means unchanged, `-1` resets to the runner default.
  `stall` (minutes, same range and reset) is the silence window beside
  it: NULL is "ceiling only", and a fire that writes nothing for longer
  than the window is killed as hung.
- `migration.go`: the one-time schema-1→2 migration: folds every
  `<hash>.sqlite`'s live jobs into `global.sqlite` (re-minted ids,
  runs re-keyed, crontab lines rewritten from `cwd-<hash>:jN` to the new
  `jN`), moves the old files aside as `<hash>.sqlite.migrated`, and is a
  no-op on the second open (no `<hash>.sqlite` remains; the fold keys on the files, not the version, so a fresh `global.sqlite` folds too).
  The schema-3, schema-4, and schema-5 column adds (`command`,
  `timeout`, `stall`) ride the same function as presence-keyed
  `ALTER TABLE`s, since it runs on every open.
- `runner.go`: the job runner (the worker spawn, bwrap jail, socket
  proxy); the spawn captures each stream to the first and last 128 KiB
  of a 256 KiB budget with a truncation marker, so a verbose worker
  cannot OOM the runner; the stored cwd is revalidated at fire time (the
  jail rw-binds it), a replaced, moved, or deleted cwd skipping the fire
  with a recorded reason. The spawn context is bounded by the row's own
  `timeout`, else `RunOpts.Timeout`, else `DefaultRunTimeout` (30 min).
  The `Spawn` seam carries an output observer: every byte the worker
  writes touches the row's `stall` window (a silent fire past it is
  killed as hung, the log naming the reason) and streams to a live
  `.stream` tail beside the canonical log, which is written whole at the
  end. A delegate passes no observer — interactive sessions keep the
  plain timeout.
  A command job's fire skips the busy probe and
  the jail: `sh -c` over the stored line with the process environment,
  in the job's cwd — the payload is the operator's own, the same trust
  the crontab line itself carries.
- `delegate.go`: the one-shot worker spawn (SPEC_DELEGATE): the busy
  rule, the ad-hoc record (a minted job row with no crontab line), the
  state-store bind and explicit identity for the resumable transcript, the per-session
  delegate-slot flock (one slot per `slots`; a call that finds the set
  full waits on a short poll for a slot until its context ends, the
  refusal naming the wait time) and the no-recursion marker.
- `jail.go`: the bwrap jail argv composition: `--clearenv` and the named
  `--setenv` list (PATH, HOME, RIG_HOME; `RIG_DELEGATE=1` for a delegate
  worker), each entry split into explicit VAR VALUE pairs (bwrap's
  `--setenv` takes two arguments; an entry without an `=` refuses), are
  the worker's whole environment, so the operator's exported
  secrets never reach a jailed worker.
- `proxy.go`: the unix-socket proxy (the jail's one hole), the socket
  chmod'd 0600 after listen so no other local user reaches the model
  endpoint through a running job.
- `fold.go`: the fold/replay over the event log.
- `render.go`: the list/rendering: one list grouped by each job's own
  `cwd` (this directory first, then the rest by path), the empty store
  named (`scheduler: no jobs (global.sqlite)`), and tagged crontab
  lines with no job row listed as orphans with the removal instruction.
- `metadata/scheduler.go`: hand-written metadata.

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
- Runs are chain reads over their own container: run history survives
  compaction (an event-args-only shape would have dropped it).
- Crontab is written before the store commit: drift is surfaced in list,
  and a line orphaned by a crash between the write and the commit is
  listed too (the runner refuses to fire it, naming the row).
- `update` is the definition change: one `update` op overlays only the
  fields the args carry; the id and the runs stay (remove + create
  re-mints the id and orphans the runs); a cadence change rewrites the
  one crontab line under the same key, a paused job's line rewritten
  commented and the new line landing on resume; `update` never changes
  the state (pause/resume stay their own ops).
- `done` is the once-fire's own op: the runner's `RecordRun` appends it
  after the `run` in the same transaction and the fold moves the job to
  `done`; the projection is never written directly (a direct write is
  undone by the next fold, the job reverting to `active` with a
  "no crontab line" drift). A done job's consumed line is not drift.
- A once job's `at` must be in the future (refused otherwise) and is
  stored normalized UTC; the crontab fields stay local (the daemon
  fires in local time), the list shows the stored `at` rather than a
  re-derived local fire, and `NextFire` computes in the caller's
  location. `RealCrontab` bounds each crontab call at five seconds.
- One store, `global.sqlite`: `cwd` is a job field (where it runs and how
  the list groups), not a storage partition; `ParseKey` accepts `jN` only
  (the migration rewrites the old `cwd-<hash>:jN` crontab keys).
- A job's kind is immutable: a command job stays a command job and a
  model job stays a model job; `update` overlays the payload its job
  already carries and a cross-kind change refuses by name (remove +
  create expresses it). The migration function runs on every open, so
  its steps key on presence — the command column on `pragma_table_info`,
  the legacy fold on the files — never on the stored version alone.
- `timeout` is a job field, not a kind field (command jobs carry one
  too), and the runner's clock reads the row at fire time: precedence is
  the row's own timeout, then `RunOpts.Timeout`, then
  `DefaultRunTimeout`; a NULL is "unbound by the job", never 0.
- `stall` is a job field too, and it is opt-in by design: NULL means
  "ceiling only" (no stall kill), because a command job that is silent
  by nature — a backup, a digest — must never be killed for not
  printing. A model job that should never sit mute states its own
  window. The liveness signal is bytes written, not the process: a long
  silent computation is not a stall, a hung provider is. The one-shot
  worker keeps its stdout answer-only and heartbeats on stderr
  (reasoning deltas, tool start/end lines, a 30s heartbeat while a tool
  runs), so a worker deep in a silent tool stays alive.
