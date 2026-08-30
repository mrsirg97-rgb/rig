# state: generated stores for session, todo, rem, scheduler

rig's persistent state is four small schemas. lift (`~/Projects/lift`)
compiles a schema into its service layer: domain types, static row scanners,
`Get`/`Insert`/`Update`/`Delete` accessors over a serializable transaction,
sqlite DDL, and seek-and-fold read chains. rig hand-writes the metadata
that describes each schema and the thin `core.Tool` adapter over each
generated service. Everything between is projected, never typed by hand.

This is off the roadmap on purpose: it is not one deliverable, it is the
substrate roadmap deliverables 2, 3, 4, and 9 stand on (todo, rem,
scheduler, sessions), plus the session store the headless workers need first. It is
mostly data modelling and porting.

Reference for the compiler: `~/Projects/lift/DESIGN.md` (the four tags,
cameras), `~/Projects/lift/cmd/templates/` (domain, ddl, sqliteread),
`~/Projects/lift/cmd/rem/metadata/` (hand-written-shape metadata to copy the
tag syntax from), `~/Projects/lift/sqlx/sqlx.go` (the transaction seam).
Reference for the schemas: `~/Projects/pane/TODO_SPEC.md`,
`~/Projects/pane/docs/TASK_TREE_SPEC.md`, `~/Projects/pane/docs/REM_SPEC.md`,
`~/Projects/pane/docs/SCHEDULER_SPEC.md`, `~/Projects/pane/extensions/*.types.ts`,
`~/Projects/pane/extensions/scheduler/store.ts`.

## goals

- One way to persist state: sqlite files, `database/sql`, one driver, one
  transaction seam, generated accessors. No per-tool storage code.
- The session transcript becomes rows, so a headless `-p` worker leaves an
  autopsy behind (messages, tool calls, results, usage, faults) even when the
  process is killed mid-turn.
- todo, rem, scheduler keep pane's schemas and semantics exactly. Same
  design, different runtime. The event-log discipline (append-only events,
  projection rebuilt from the log, minted ids, tombstones) is preserved.
- Adding a container is metadata plus regenerate. Adding a store is a
  directory plus a registration line. The design test still holds.

## non-goals

- No ORM, no reflection at runtime, no query builder. Generated static SQL.
- No mesh, no rust, no pgread cameras. Local sqlite only.
- No schema migrations framework. `CREATE TABLE IF NOT EXISTS` plus a
  `meta.schema_version` row per store; a version bump is a named change.
- No shared "state" package the loop imports. `core/` and `loop/` stay
  stdlib and never learn a store exists. Session in memory stays a plain
  struct; persistence is a leaf that observes it.
- No hand-written SQL beyond `extra.sql` (below). If a query is missing,
  the answer is a camera or a chain, not an ad hoc string in a tool.

## layout

```
rig/
  store/
    sqlx/          the transaction seam, copied verbatim from lift/sqlx
    lazy/          the lazy accessor runtime, copied verbatim from lift/lazy
    open.go        Open(path) -> sqlx.DB: driver, WAL, busy_timeout, foreign_keys,
                   ddl + extra.sql applied, schema_version checked
    state/         session transcript store
      metadata/    hand-written four-tag metadata (source of truth)
      domain/      GENERATED
      ddl/         GENERATED
      extra.sql    hand-written: indexes, checks the DDL camera does not emit
      gen.json     lift engine config for this store
      source.json  lift source config for this store
      state.go     minting, projection helpers, the recorder the loop feeds
    todo/          same shape (tree reads fold in Go; no sqliteread camera needed)
    rem/           same shape; plus fts.sql (FTS5 virtual table, triggers)
    scheduler/     same shape
  tool/
    todo/          core.Tool adapter over store/todo: schema, description,
                   arg decode, pane's error voice; nothing else
    rem/
    scheduler/
```

Generation runs from `~/Projects/lift/cmd` and points at rig:

```
cd ~/Projects/lift/cmd
go run main.go -config=$RIG/store/todo/gen.json -source=$RIG/store/todo/source.json
```

`gen.json` `"name"` is the absolute output root (`$RIG/store/todo`),
`"module"` is `github.com/mrsirg97-rgb/rig/store/todo`, `"runtime"` is
`github.com/mrsirg97-rgb/rig/store` (see decisions); `source.json`
`"sourceDirectory"` is `$RIG/store/todo`, `"name"` is `metadata`. Both
external paths are verified to work with the current engine (2026-08-15):
generated files land under the store, generated imports resolve to rig's
module. A `go generate` line per store pins the command.

## the seam

```go
// store/sqlx, copied from lift verbatim
type DB struct{ *sql.DB }
func (db DB) Tx(ctx context.Context) (context.Context, *sql.Tx, error)          // serializable
func (db DB) TxReadOnly(ctx context.Context) (context.Context, *sql.Tx, error)
func TxFrom(ctx context.Context) (*sql.Tx, error)                               // fails closed
```

- Generated accessors call `sqlx.TxFrom(ctx)`. A tool's `Exec` opens one
  transaction at its top (`db.Tx(ctx)`), threads the returned ctx through the
  domain calls, commits on success, rolls back on any error. One call, one
  transaction: pane's "every call is one transaction" rule, kept.
- The session rides the same ctx (`core.WithSession`) it does today. Two
  typed keys, two values, no collision. The loop is untouched.
- Each store is its own sqlite file and its own `sqlx.DB`, opened once at the
  root and handed to the tool constructor: `todo.New(db)`. Paths follow pane:
  one `todo/todo.sqlite` (every row carries its project scope; its section's
  migration folds the old per-workspace files), `global.sqlite` where pane
  has a global scope (rem), and the scheduler's one
  `scheduler/global.sqlite` (its section's migration folds the old
  per-workspace files).
- `Open` sets `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`,
  runs `ddl.Statements()` then `extra.sql`, then checks
  `meta.schema_version`; a mismatch is a loud refusal naming both versions.
  Open + integrity check on start; a corrupt file is quarantined aside
  (`<file>.corrupt-<ts>`) and a fresh one created, per REM_SPEC C, never
  silently truncated.

## the schemas

Metadata is written by hand in lift's four-tag grammar (`primary`, `alias`,
`link`, `dictionary`) with `// table:"..."` on the struct. Copy the syntax
from `lift/cmd/rem/metadata/*.go`. Every link pairs with a plain alias on
the same column: the domain camera skips `HasLink` properties in the
column list and expects the paired alias to carry it; an unpaired link
silently drops the FK from the generated INSERT. It is lift working as
designed, and the pairing is how every dbmeta-generated metadata file is
shaped. Column names and types below are pane's;
where pane's SQL carries a constraint the DDL camera does not emit, it moves
to `extra.sql` (indexes) or to Go in the store package (CHECK-style
invariants, which pane also enforces in code).

Ids are minted in Go inside the serializable transaction (`max+1` over the
container, or a `meta` counter where reuse must never happen), because the
generated `Insert` takes the primary key from the caller. This matches pane
(`tN`, `jN`, `mN` minted by the store, never invented by the model).

### state (new; model this one from scratch)

The session transcript as rows. One file per session under
`<home>/sessions/<session_id>.sqlite`, or one file per workspace with
`session_id` on every row; decide in the first PR, name it. Containers:

- `sessions`: id (primary, minted, stable for the process), cwd, model,
  started_at, ended_at, exit (ok|fault|cancelled), version.
- `messages`: seq (primary), session_id (link sessions), role, content,
  reasoning (nullable; filled by the recorder, deliverable 7), tool_id (nullable),
  created_at.
- `tool_calls`: id (primary: the provider's call id), message_seq (link
  messages), name, args (TEXT json), result (TEXT, nullable until it lands),
  err (nullable), started_at, ended_at.
- `usage`: message_seq (primary, link messages), prompt, completion,
  cache_read, cache_write.
- `files`: session_id + path (primary), hash, mtime. `Session.Files`
  persisted, so a resumed session keeps its drift checks. `files` pairs its
  composite primary with `session_id` directly and carries no Session link
  while its siblings do; harmless (no FK is emitted) but named for
  consistency. Tool results live on `tool_calls`, not as role=tool rows.
  Session resume (deliverable 7's `state.Resume`, the root's `-resume`)
  projects `[]core.Message` back from the transcript rows; no schema
  change was owed, and none was taken; deliverable 9's `sessions` command
  reads the same rows. After a compaction (SPEC_COMPACT 5) the marker is
  the projection interface: resume starts from the last `[compaction]`
  row when one exists; the window is the summary row and everything
  after it, so the compacted shape rebuilds, not the full history with a
  summary after the tail it summarizes.
- `faults`: seq (primary), session_id, at, message.

The recorder is a leaf that receives what the loop already emits: it wraps
the `Frontend` (an observing Frontend that forwards to the real one) and
sources its rows from the loop's events; the transcript, `ToolStart`/
`ToolResult` (the guarded result, as the loop appends it), reasoning, usage
with the cache fields. It appends a row per event inside its own short
transaction, so a kill leaves every completed row readable. A `TurnEnd`
discards the unlanded partial of a turned turn; each turn boundary upserts
the file provenance. No loop change, and, with the middleware tap retired
(deliverable 7), the schema did not change either.

SPEC_COMPACT 5: a `Compacted` event lands the summary as a marked user
row (`role = "user"`, the content verbatim with the `[compaction] `
marker) plus a usage row against that row's seq, then re-lands the kept
tail after it; fresh rows (fresh seqs), the assistant calls with
recorder-minted fresh ids (the `tool_calls.id` primary key), name/args/
result verbatim, the earlier rows staying as the autopsy. No schema
change: the marker is the contract, and the rows are existing shapes.

The `sessions` command (deliverable 9, SPEC_COMMANDS) and the `sessions`
tool (`tool/sessions`) both read this store, and both are thin adapters
over the same verbs; no SQL in either. The verbs: `ListSessions(ctx, db,
limit)` returns the workspace's session rows, newest first, each carrying
the model, the version, the turns count (the session's `role = 'user'`
rows minus the `[compaction] ` summary rows; transcript machinery, not
prompts), and the fault count. `limit` is named; `state.ListCap` (50) is
the default and the maximum (a glance, not an archive). An unclosed row
(`ended_at` NULL) renders as `exit open`; the one place that word
appears; the store's exit vocabulary stays `ok | fault | cancelled`.
`SessionFaults(ctx, db, sessionId)` returns one session's fault rows,
newest first, and `SessionUsage(ctx, db, sessionId)` its usage total. The
tool's `summary` action aggregates over the recent `n` sessions: the
session and turn counts, the distinct models with their versions, the
fault count with the latest fault's first line, and the aggregate cache
ratio (`cache_read*100/prompt`, the status row's arithmetic). Two small
recorder additions for the `new` / `sessions resume` handoff (SPEC_COMMANDS
4): `Ensure` (the session row exists before any row lands under the id,
idempotent) and `Retarget` (the retiring recorder is re-pointed before
its in-flight `Input` completes, so that row lands under the new
session). No schema change: the rows are existing shapes; the new verbs
are reads over them.

`-p` workers get their autopsy from this: the `sessions` command (roadmap
deliverable 9) or plain `sqlite3`.

### todo (port; TODO_SPEC.md A, Rev 2, TASK_TREE_SPEC.md A)

One store, every row scoped (the project identity, SPEC_STATE's scope
law; see the migration section): a queue is the repo's, not the
directory rig happened to start in, and the identity partition is never
a filename; it is the short sha1 of the git common dir (`store/scope`),
falling back to the cwd hash outside a repo.

- `meta`: key (primary), value.
- `events`: seq (primary, minted, strictly increasing), ts, op
  (create|start|complete|fail|retry|move|compact), args (TEXT json), session
  (nullable), scope (the queue's identity, nullable-false).
- `tasks`: scope + id (primary, `tN` per scope), text (unique per scope via
  extra.sql), status (pending|in_progress|done|failed), pos, created_seq
  (link events), updated_seq (link events).
- `task_deps`: scope + task_id + depends_on (primary: both link tasks within
  one scope), created_seq.
- `extra.sql`: `tasks_pos_seq` index on (scope, pos, created_seq): the unique
  index on (scope, text).
- Semantics kept verbatim, per scope: projection rebuilt from the log on
  every call and never trusted; replay is total and skips inapplicable rows;
  positions minted never mutated; move via events; claim semantics (start
  claims, foreign complete refuses, fail frees; completing your own
  unclaimed pending task implicitly claims and completes; start+complete,
  both events, the echo noting auto-started); compaction past 1000 events
  snapshots the queue and resets the epoch; dependsOn DAG validated at the
  boundary, cycles refused, completion gated, blocked skipped by `next`.
  Minted seq is one sequence across scopes (a shared events table), while
  ids stay `tN` per scope; the compact fold and stale footer are per scope.
- The empty reply names the queue it read (`(no tasks in <label>'s queue)`),
  never "this directory's queue" (SPEC_CORE's empty-reply rule).
- The FSM lives in `store/todo/todo.go` as Go, errors in pane's teaching
  voice; the generated domain is only the substrate it writes through.
  Two raw arms are owned and named as such: the event scan that rebuilds
  the fold (no ordered scan accessor is generated) and the projection
  rewrite (no bulk-replace accessor is generated).
- **Migration (1 → 2), lossless.** Todo rows carried no cwd: the filename
  was the identity, so the fold keys on the files existing (the
  scheduler's lesson: a fresh `todo.sqlite` folds too): every
  `<12-hex>.sqlite` in the todo dir folds into `todo.sqlite` with
  `scope = <that hash>`, verbatim, rows in event order, the legacy files
  and their `-wal`/`-shm` moved aside as `.migrated`. Rejected: walking a
  hash back to a path to re-key (a renamed directory orphans its queue).
  Then rem's lazy re-scope, the same shape exactly: on open, if
  `scope.Key(cwd) != scope.ShortHash(cwd)` and no `migrated:<oldScope>`
  marker, that cwd-hash's rows re-scope to the repo scope, `INSERT OR
  IGNORE` the marker, counted once on stderr, one transaction. The fold is
  in filename order, rows in event order; reproducible.

### rem (port; REM_SPEC.md D, E, F, G)

- `meta`, `memories` (id primary minted from a `meta` counter, never
  reused: REM_SPEC's AUTOINCREMENT rule, kept by minting since the DDL
  camera emits plain PRIMARY KEY), scope, scope_label, kind, content,
  source, importance, strength, access_count, superseded_by (link memories,
  nullable), created_at, last_accessed_at, last_consolidated_at,
  content_md5.
- `trigrams`: memory_id (link memories) + gram.
- `extra.sql`: unique (scope, content_md5): (scope, created_at); trigram
  indexes, and the ON DELETE SET NULL behaviour of `superseded_by` (the DDL
  camera leaves foreign keys off by design, so the store's prune clears
  supersession references itself before deleting, in the same transaction;
  named test).
- `fts.sql`: `memory_fts` FTS5 virtual table with `porter unicode61`, and
  the insert/update/delete triggers pinned in REM_SPEC E. FTS5 is compiled
  into modernc's sqlite; recall's shipped policy on capability absence
  degrades to fuzzy-only (REM_SPEC's named case), and a driver without
  the table fails loudly at schema application; unreachable under the
  bundled driver.
- Recall stays two arms plus reciprocal rank fusion, project-first
  global-fill, effective strength at recall, checkpointed decay at prune.
  These are Go over the generated substrate plus the two raw arms
  (`memory_fts` MATCH and the trigram join), which are the only raw queries
  the store owns and are named as such.
- **Rem is deliberate.** Every rem operation is something chose: the model
  learns/recalls/reflects/prunes through its tool, the operator prunes
  through the `/rem` verb. Nothing is written by a compaction; the summary
  is context, not memory (SPEC_COMPACT 6, cut), and nothing is read into
  the prompt by a session start (the root's remembered segment, cut; the
  system prompt names the rule).
- **The project a fact belongs to is a choice, not an accident of where
  rig started.** The model says the project the way it already says
  scope: `project` is a path on learn/reflect/recall/prune, resolved
  through `store/scope` and replacing the session cwd in
  `writeScope`/`readScopes` for that call (worktree-safe; `~` expands at
  the `middleware/paths` boundary). The why is the failure it fixes: a
  session in `~/Projects` learning about `~/Projects/rig` files facts
  into a scope nobody recalls from inside the repo; the directory rig
  happened to start in, not the repo the fact describes. `project` +
  `scope: global` refuses by name (a global memory has no project), and
  the label stays the resolved path's base name. The description carries
  one Guidelines clause: name `project` when the fact belongs to a repo
  you did not start in (mind the menu-budget case; the clause is short
  by design).
- **Scope is a repo identity, not a cwd.** scope = the absolute git common
  dir of the cwd (`git rev-parse --git-common-dir`, resolved against the
  cwd when git prints it relative; an echoed option or an empty line is
  not a path and falls back to the cwd), so two worktrees of one repo
  share one memory; outside a repo, the cwd itself, hashed as today
  (`shortHash`). The schema bump (1 → 2) carries a one-time idempotent
  migration, one transaction per open: rows under an old cwd-hash scope
  now inside a repo re-scope to the repo's, and rows with source "session
  compaction" are removed (never deliberate), counted once on stderr. The
  re-scope is keyed on the launch cwd; rows learned from another
  directory of the same repo move when rig is next started there (a hash
  cannot be walked back to its cwd). Two processes opening the same file
  at once both succeed (the marker insert is `OR IGNORE`; the transaction
  serialises them). Rejected: reading both scopes forever.
- The `/rem` command (SPEC_COMMANDS 11): `rem [list|show|forget]` over
  the same store; list the live memories (project then global, one line
  each), show by id, forget by id (this project's or global only; ids
  are file-wide, so another project's id is refused by name). A pin verb
  was rejected there: the operator reads and prunes; keeping is the
  model's learn/reflect.
- lift's `cmd/rem` is a different design (postgres, mesh, episodes and
  associations). Its recall projection and testkit are worth reading; its
  schema is not the one being ported. pane's rem is.

### scheduler (port; SCHEDULER_SPEC.md, scheduler/store.ts, and the
post-merge corrections)

- One file: `scheduler/global.sqlite`. The cwd partition is gone: the
  job's own `cwd` field (never empty, defaults to the creating session's
  cwd) decides where it runs and how the list groups, not a store
  partition keyed by the launch cwd. Every crontab key is `jN`; `name` is
  unique across the one store; ids are one sequence. The old
  `cwd-<12hex>:jN` keys and `<sha1(cwd)[:12]>.sqlite` files are migrated
  once on the schema bump (see the migration paragraph below).
- Migration (schema 1 → 2, one-time, counted once on stderr): for every
  `cwd-<12hex>.sqlite` under `scheduler/`, fold its live jobs
  (state != `removed`) into `global.sqlite`; re-mint ids (`j1`×N → the
  next free `jN` in the global fold), keep each job's `cwd` (it is on the
  row), bring each job's `runs` rows along re-keyed to the new id, rewrite
  that job's crontab line from `cwd-<hash>:jN` to the new `jN` (the lock
  key follows the crontab key), then move the old file aside as
  `<hash>.sqlite.migrated`. A file with no live jobs moves aside with no
  row written. Idempotent: a second open finds no `<hash>.sqlite` and does
  nothing. Rejected, named: reading both layouts forever; a `scope` that
  defaults to global but stays in the schema.
- `events`: seq, ts, op (create|update|pause|resume|remove|run|compact),
  args, session.
- `jobs`: id (primary, `jN`), name (unique among live jobs only, enforced in
  Go, no unique index), prompt, cron, at (nullable), cwd (never empty;
  defaults to the creating session's cwd), model, busy (skip|force), state
  (active|paused|done|removed), last_status (ok|fail|skip, nullable),
  last_ts, last_exit, created_seq, updated_seq.
- `runs`: seq (primary), job_id (link jobs), started_at, ended_at, status,
  exit, log_path. Pane records these as run events; a container makes
  `runs {id, n}` a chain read instead of a log scan.
- Crontab remains the scheduling truth (tagged lines, surgical rewrites,
  written before the store commit; drift surfaced in `list`). The runner is
  a small Go binary or the rig binary itself with a `run-job` verb; it
  needs `-p` one-shot mode (landed with the scheduler) and llama-swap's
  `/running` and `/v1/models` for the busy policy, unchanged.
- `update` is the verb that changes a live job's definition, in place: any
  of `prompt`, `model`, `cwd`, `busy`, `name`, and the cadence. The cadence
  is a 5-field cron, or an `at` that makes the job `once`; create's
  refusals apply verbatim (a bad ISO, an invalid cron, a `once` without its
  `at`), and a 5-field cron and an `at` in the same call are mutually
  exclusive, refused by name. No fields → `update needs a change`; an
  unknown id, or a removed one, is refused by name; a `name` change keeps
  the store-wide uniqueness (the job's own name is not a collision). One
  `update` op is appended and the fold overlays only the fields the args
  carry: the id and the runs stay. Rejected, named: remove + create; the
  id re-mints, the runs orphan, and two crontab moves express one change;
  the trail surviving an edit is the point of having one. A cadence change
  rewrites the job's one crontab line under the same key; a paused job
  stays paused (its line is rewritten commented) and the new line lands on
  resume. `pause` and `resume` stay their own ops; `update` never changes
  the state.
- The `defaultModel` fallback constant is cut (SPEC_CONFIG 12, with
  the settings' `defaultJobModel` key): `Create` takes the model from
  its caller, never a literal; the tool passes the fleet's model
  (SPEC_CONFIG 12) or the job's own, the dashboard's create fills the
  fleet's model (SPEC_SERVE), and a direct `Create` with an empty
  model refuses by name. The worker's model is the operator's: named
  by `workers.json`, defined by the operator's `models.json` row,
  baked into no binary. No schema change, no path change: the job row
  carries its model (set at create), and `run-job` fires the row's
  model; a fire needs no `workers.json`, so a fleet removed after
  jobs were created leaves them firing on their recorded model. The
  tools follow the config: no fleet, no worker tools registered
  (SPEC_CONFIG 12's presence rule); the store and the runner are
  unchanged underneath.

## interfaces

The generated service per store is what lift emits from `domain.template`:

```go
type MemoryDomain interface {
	GetMemory(ctx context.Context, memoryId int64) *lazy.Lazy[Memory]
	InsertMemory(ctx context.Context, row Memory) (*Memory, error)
	UpdateMemory(ctx context.Context, row Memory) (*Memory, error)
	DeleteMemory(ctx context.Context, memoryId int64) (*Memory, error)
}
```

The tool adapter is the only hand-written surface the model sees, and it
is pane's tool surface verbatim: `todo {create|start|complete|fail|retry|
move|read}` (`next` is not a verb: its semantics ride the render's next
pointer, blocked-skipping), `rem {learn|recall|reflect|prune}`, `scheduler {create|
update|list|pause|resume|remove|runs}`, and `sessions {list|summary}` (rig's own,
not pane's: a read-only introspection of the session store, absent from
the root's `mutatingNatives` and from the concurrent read set; it opens
a store, like `todo`/`rem`/`scheduler`, so it is not a pure observation).
Descriptions and schema property text are pane's promptGuidelines, lowercase, terse.

## decisions

- **`database/sql` is the seam, `modernc.org/sqlite` is the driver.** Pure
  Go, no cgo, static binary, FTS5 compiled in, and it is what lift already
  vendors and tests against. Justified once here; the roadmap's deliverable
  3 note defers to this spec.
- **The runtime import: decided, B.** `domain.template` used to hardcode
  `github.com/mrsirg97-rgb/lift/lazy` and `.../lift/sqlx`. lift now takes a
  `"runtime"` field in the engine config (landed 2026-08-15; defaults to
  lift's own module, so every existing project generates byte-identically)
  and the camera writes `{{ .Runtime }}/lazy` and `{{ .Runtime }}/sqlx`.
  rig copies `lift/lazy` (two files) and `lift/sqlx/sqlx.go` into
  `store/lazy` and `store/sqlx` once, unchanged, and every store's
  `gen.json` sets `"runtime": "github.com/mrsirg97-rgb/rig/store"`.
  rig stays self-contained: no lift require, and lift has no git remote
  to require it from anyway. The copied runtime is ~400 lines owned in two
  places; a change to it in lift is a named re-copy here, not a drift.
- **What the DDL camera does not emit** (AUTOINCREMENT, CHECK, DEFAULT
  expressions, indexes, foreign key actions, virtual tables, triggers) is
  not added to the camera. Indexes, FTS, and triggers go in `extra.sql` /
  `fts.sql`; invariants go in Go where pane also enforces them; id
  minting replaces AUTOINCREMENT. lift's grammar rule ("do not add grammar
  the basis already spans") applies to rig's use of it.
- **Event log first, projection second.** For todo and scheduler the
  `events` container is the fact and `tasks`/`jobs` are the projection,
  exactly as pane. The projection is rebuilt from the log inside the same
  transaction on every call. Whether that fold is a lift transform or a Go
  function in the store package is the implementer's choice per store;
  either way it is one function, tested by replaying pane's fixture logs.
- **The state store is a recorder, not a dependency of the loop.** It hangs
  off `Frontend.Notify`, with deliverable 7's loop events the tool rows are
  event-sourced and the middleware tap is retired; the schema had already
  been designed for that day (tool_calls has started_at/ended_at, messages
  has reasoning), so nothing in the store changed.
- **One transaction per tool call, serializable, opened in the adapter.**
  Not per turn, not per process. Cross-process safety (scheduler runner
  writing while a session reads) is WAL plus busy_timeout, as in pane.
- **Session id.** `core.Session` gains an `ID string` (minted at
  `NewSession`, ULID-style time-ordered, stdlib `crypto/rand`); the recorder
  and todo's claim semantics attribute to it. This is the one `core/` change
  in this spec and is named as such in its PR (SPEC_CORE session section
  updated in the same PR).
- **Generated getters return `(nil, nil)` on absent keys**: lazy.go's
  absent-read, by design; no panic. state.go additionally guards the nil
  row at its boundary with a named error (`state: …: no such row`).

## testing

- Generation is reproducible: a test in each store runs the pinned command
  against a temp output and diffs it against the committed generated files;
  drift fails the build with the regenerate command in the message.
- Store tests use a real sqlite file in `t.TempDir()`, never `:memory:`
  (WAL and busy_timeout behaviour matter). No mocked driver.
- Pane's named cases port by name: TODO_SPEC's testing strategy, TASK_TREE's
  DAG cases, REM_SPEC's recall and prune cases, scheduler-core and
  scheduler-runner tests. A ported test keeps pane's name so coverage can be
  compared side by side.
- Scheduler one-store: a job created from `/a` is listed and pausable from
  `/b` under the same `jN`; ids are one sequence; list from `/a` puts `/a`'s
  jobs first; the crontab line is `jN`; the migration folds two cwd stores
  with colliding `j1`s into distinct ids with their cwds intact and their
  crontab lines rewritten, then is a no-op on the second open; `run-job jN`
  fires the folded job in its own cwd.
- Recorder: kill a `-p` run mid-turn (context cancel inside a scripted tool)
  and assert every row that completed before the kill is readable; assert
  the session row is closed with `exit=cancelled` on the clean path.
- Corruption: a truncated file at Open is quarantined and named: FTS5
  absence at Open is a loud refusal.
- `go build ./...` from a clean clone of rig is a test: no lift import
  survives generation.

## scope

Four stores, four metadata packages, generated domain/ddl (plus sqliteread
where a tool reads trees), one seam, one driver, four thin tool adapters,
and one `core/` change (session id). Order of work: `store/sqlx`+`store/lazy`+`open.go`, then `state` (the
workers need it), then todo, rem, scheduler in roadmap order. The loop is
byte-identical throughout.
