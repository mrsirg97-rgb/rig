# store/scheduler

## What it is

The background-jobs store, Go over the generated substrate. SPEC_STATE's
"### scheduler" section is the spec. The event log is the spine; jobs is
a replayable projection rebuilt from the log inside every transaction and
never trusted. Removed jobs stay as tombstones so ids and names are never
reused and remove survives compaction. Runs are structured records in
their own container (SPEC_STATE's deviation): runs reads are chain reads
over it and run history survives compaction. Crontab is the scheduling
truth: tagged lines under `# rig-scheduler:<home>:<key>` (the 12-hex
short sha1 of the rig home path, so `~/.rig` and an embedder's home
sharing one crontab own disjoint lines and a key only means something
inside its home), surgical rewrites, foreign lines byte-identical,
written before the store commit; drift is surfaced in list.

## What it includes

- `scheduler.go`: the package doc, `SchemaVersion`, `Statements`,
  the `DB` alias.
- `cron.go`: the vixie cron parser and matcher.
- `crontab.go`: the tagged-lines crontab edit/merge; `TagHome` is the
  short-sha1 of the cleaned home path, `Scan` and the writer see only
  this home's `rig-scheduler` lines (another home's and every old
  `pane-scheduler` line are foreign to them), and the writer emits only
  the new tag. `oldTagLine` reads an old-tag line for the migration
  alone: its key, and the runner command between the cron fields and
  the key.
- `verbs.go`: `Create` over the one
  `global.sqlite`; `state.go`: the state verbs
  (pause/resume/remove/repair), `update.go`: the update verb, `runs.go`:
  the run record and the runs read, `show.go`: one job read. The
  crontab key is `jN` for every job, `name` unique
  store-wide, ids one sequence. `Create` takes the model from the
  caller: a named model stores verbatim, an empty model is the unnamed
  job (the fire resolves it at run time), and `update` takes the model
  as a pointer — absent means unchanged, null or empty clears to the
  unnamed job — unless the create carries
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
  The schema-3 through schema-7 column adds (`command`,
  `timeout`, `stall`, the budget/cost pair, `runs.model`) ride the same
  function as presence-keyed `ALTER TABLE`s, since it runs on every open. The crontab-tag migration
  rides it too: once per store (a `meta` marker), this home's old-tag
  lines — old lines whose key is a job in this store's event log — are
  rewritten to the `rig-scheduler` tag, every other line left byte
  identical, and a store with no jobs never touches the crontab shim.
- `runner.go`: the job runner (`RunJob`); `lock.go`: the fire lock and
  log pruning, `busy.go`: the swap gate (the resident set and the live
  free-slot read, `ResidentModel`/`FreeSlots`) with `FleetCapacity`,
  the not-resident refusal carried as `ErrNotResident` so a caller
  matches it with `errors.Is` instead of its text (the swarm's drain
  treats it as a stop, not a death),
  the wire-time capability read — the widest slot count the resident
  server runs, from one live read of the same `/slots` the gate reads,
  nothing resident the zero value — and the spend read,
  `spawn.go`: the real spawn and the capture
  (the worker spawn, bwrap jail, socket proxy);
  the spawn captures each stream to the first and last 128 KiB
  of a 256 KiB budget with a truncation marker, so a verbose worker
  cannot OOM the runner; the stored cwd is revalidated at fire time (the
  jail rw-binds it), a replaced, moved, or deleted cwd skipping the fire
  with a recorded reason. An unnamed job's model resolves at fire time —
  the resident model resolved to the models-table row whose id is the
  resident id or one of its llama-swap aliases (`resolveResidentModel`,
  `busy.go`), else `RunOpts.DefaultModel` (the root's settings' model),
  a fire with neither recording a skip that names it — the gate keeps
  the canonical id, while the row id rides the argv's `-model`, the
  fire log's `model=` line, and the run record's `model`; a resident
  with no row records a skip naming it and the known rows, and the
  fire never spawns. The spawn context is bounded by the row's own
  `timeout`, else `RunOpts.Timeout`, else `DefaultRunTimeout` (30 min).
  The worker's prompt rides the spawn context (`WithPrompt`), never
  argv: every spawn says `-p -` and `RealSpawn` pipes the prompt to the
  child's stdin (2.9.3; one argument is capped at 128 KiB on Linux).
  The `Spawn` seam carries an output observer: every byte the worker
  writes touches the row's `stall` window (a silent fire past it is
  killed as hung, the log naming the reason) and streams to a live
  `.stream` tail beside the canonical log, which is written whole at the
  end. A delegate passes no observer unless its `Stall` is set — the
  watch rides the same observer, so an interactive session keeps the
  plain timeout and a caller that states a window gets the kill.
  The runner's `RunOpts.Decisions` (SPEC_DECISION) records one final row
per skip past the job-row read (the pre-row skips have no scope and
record nothing); a store error never changes the skip.
A command job's fire skips the busy probe and
  the jail: `sh -c` over the stored line with the process environment,
  in the job's cwd — the payload is the operator's own, the same trust
  the crontab line itself carries.
- `delegate.go`: `Delegate` is `DelegateStart` and `Wait` (2.14.0,
  SPEC_DELEGATE 8). Everything that can refuse a delegation — the seams,
  the recursion marker, the residency gate, the ad-hoc record, the jail,
  the fleet pipe — happens in `DelegateStart`, synchronously, so a
  refusal is still an answer this turn; it returns a `Delegation`
  carrying the run id, the worker's session, its model and its log path
  (named at the start, not at the end, because the hand-back line names
  it) and a channel. The goroutine owns the spawn, the run log, the
  record, the pipe and the proxy, and the timeout context that only it
  may cancel; `Wait` reads the outcome once the log and the record are
  on disk. `Delegate` keeps the synchronous shape verbatim for the
  swarm, the review fire and the piped delegate tool, so there is one
  implementation of both.

- `delegate.go`: the one-shot worker spawn (SPEC_DELEGATE, the model
  resolution and gate of SPEC_WORKERS): the model resolves at claim
  time (named, else the resident model resolved to its models-table
  row, else `DefaultModel` — the session's default; an unreachable
  swap falls to the default as it always did, a resident with no row
  refuses naming it and the known rows), the gate is the live free-slot read (skipped for
  a remote row via the `Models` seam — the swap is never consulted;
  `WaitBusy` waits like a fire up to `Timeout`, the claim-time read
  refuses at once), the ad-hoc record (a minted once-job row with no
  crontab line, `done` once its fire lands), the
  state-store bind and explicit identity for the resumable transcript,
  the no-recursion marker, and the
  `Stall` watch: a set window kills a worker silent past it (the
  result marked `Stalled`, the stderr and the log naming the reason)
  while `Timeout` stays the spend ceiling (the seam's cap is 24h, the
  tool's own cap is 30 minutes). Every abnormal end is also recorded as
  the run's reason — `killed after timeout`, `killed after stall`,
  `canceled` (the spawn's base context ended), or `killed by signal N`
  (a signal the runner did not send) — and `RealSpawn` captures the
  signal from the wait status and carries `Pdeathsig`, so a signal
  death is never a bare exit -1.
- `jail.go`: the bwrap jail argv composition: `--clearenv` and the named
  `--setenv` list (PATH, HOME, RIG_HOME; `RIG_DELEGATE=1` for a delegate
  worker), each entry split into explicit VAR VALUE pairs (bwrap's
  `--setenv` takes two arguments; an entry without an `=` refuses), are
  the worker's whole environment, so the operator's exported
  secrets never reach a jailed worker.
- `landlock.go`: the domain arrives with the image, not in-process: an
  in-process `restrict_self` cannot cover the worker's own goroutines
  (it commits per-thread creds and Go's runtime has threads before
  main), so the runner spawns `rig -exec rig -p ...` and the exec'd
  worker's every thread inherits the wall.
- `proxy.go`: the unix-socket proxy (the jail's one hole), the socket
  chmod'd 0600 after listen so no other local user reaches the model
  endpoint through a running job.
- **The dial seam** (`runner.go` `Transport`): the `http.RoundTripper`
  the busy check's `RealFetch` and the socket proxy's reverse proxy use;
  nil is the production default (`http.DefaultTransport`). The suite's
  `TestMain` installs `testenv.Transport` here, which refuses any host
  that is not an httptest server, so a test cannot dial the operator's
  live swap by accident.
- `fold.go`: the fold state and the create event; `apply_verbs.go`: the
  verb and update event application, `compact.go`: the compaction event,
  `fold_tx.go`: the transaction machinery (the event append, the
  rewrite). The fold/replay over the event log. Jobs carry `budget`
  (dollars, 0 = unset) and runs carry `cost` (schema v6): a model job's
  fire at the cap records a skip naming the spend, and each fire and
  delegate records its worker's cost (read from the state store's cost
  column, SPEC_HOSTED 5).
- `render.go`: the list/rendering: one list grouped by each job's own
  `cwd` (this directory first, then the rest by path), the empty store
  named (`scheduler: no jobs (global.sqlite)`), and tagged crontab
  lines with no job row listed as orphans with the removal instruction.
- `show.go`: `Show` reads one job by id — the same `jobLines` block the
  list prints for it (crontab line, drift, running lock and all) plus
  one `last <run>` line from the runs container — so an agent holding a
  `jN` never lists the board to read a row; an unknown id and a removed
  one refuse by name and point at `list`.
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
- `NoToolsAllow` (`-allow none`) runs a worker with no tool at all: the
  allowlist denies every native tool and the plugin door shuts with it.
  A no-tools fire also skips the report-back suffix — a toolless worker
  cannot `rem`, so its stdout is the reply.
- The tag is home-scoped: the writer never touches another home's line
  (its `rig-scheduler` home differs, or its key is not this store's), so
  two homes sharing one crontab stay disjoint. An old `pane-scheduler`
  line belongs to nobody until the one-time tag migration claims it,
  and the migration claims a line only when its key is a job in this
  store's event log and its runner command is this binary's
  (`<self> run-job`): two homes with the same key never take each
  other's lines, whichever migrates first. A migration is given the
  same `Crontab` the runtime writes through; the web frontend's store
  cache takes the server's, so a test suite with a fake crontab never
  reaches the operator's.
- `update` is the definition change: one `update` op overlays only the
  fields the args carry; the id and the runs stay (remove + create
  re-mints the id and orphans the runs); a cadence change rewrites the
  one crontab line under the same key, a paused job's line rewritten
  commented and the new line landing on resume; `update` never changes
  the state (pause/resume stay their own ops).
- `repair` is the drift fix, a crontab write only: no event, no state
  change. With an id it re-derives that job's line (`UpsertLine` with
  the job's cron and the runner command the caller wired, then
  `SetPaused` to match the state), a removed or done job refuses
  (`nothing to repair`), and a job with no drift replies `'jN' is in
  sync` and installs nothing. With no id it walks every job (ids
  sorted), one reply line per repaired job with the drift verbatim
  from `driftOf`, `nothing drifted` when none. A crontab read failure
  refuses loudly: drift cannot be assessed, nothing is written.
- A fired once-job is `done` and the rule lives in the fold alone:
  `jobState.consumeFiredOnce` settles an `active` job whose `at` is set
  and whose `last_status` is `ok` or `fail`. It runs on the `run` verb
  and on the compact snapshot — a store compacted before the rule keeps
  the fire as `lastStatus` with no `run` event left to fold — so those
  rows settle at the next fold, with no migration and no direct write
  (the projection is never written directly: a direct write is undone by
  the next fold). The fire writes one event, `run`; the `done` op stays
  in the fold for logs written before the rule. A skip is no fire: the
  runner records skips for the drift it wants the list to keep naming (a
  paused row with a live line, a held lock), and a once-job that never
  ran stays live for the re-fire. A done job's consumed line is not
  drift.
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
  window. The liveness signal is bytes written or a frame on the fleet
  pipe, not the process: a long silent computation is not a stall, a
  hung provider is. The one-shot worker keeps its stdout answer-only,
  its stderr the reasoning deltas and the tool start/end lines, and
  heartbeats on the fleet pipe (`fleet.go`, 2.11.0) while a tool runs,
  so a worker deep in a silent tool stays alive.
- `fleet.go`: the fleet pipe, the one door a worker's typed messages
  cross. The parent (`Delegate`, `RunJob`) opens an `os.Pipe`, hands
  the write end through the spawn's context (`WithFleet`/`FleetFrom`,
  `RealSpawn` sets it as the first extra file, fd 3) and the child's
  member id in `RIG_FLEET`; it reads frames through a
  `broadcast` pipe transport and, in `Delegate`, publishes each as the
  worker's `Member` (`DelegateInput.Member`, nil = no pipe); the runner
  touches its stall watch instead, its origin 0 since it has no room.
  The parent closes its write end after the spawn and waits for the
  reader's end-of-file before returning, so no frame is lost behind the
  result — including the `tool_start` frames a worker publishes for its
  own calls (2.14.0), which is what lets a delegated worker's band row
  name the call it is in. The child side is `Fleet()`: with the env set it marks fd 3
  close-on-exec, so no tool's subprocess inherits the pipe, and returns
  the transport the one-shot frontend speaks through.
