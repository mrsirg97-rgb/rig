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
  `<home>/todo/<sha1(cwd)[:12]>.sqlite` per workspace, `global.sqlite` where
  pane has a global scope (scheduler, rem).
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
column list and expects the paired alias to carry it — an unpaired link
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
- `tool_calls`: id (primary; the provider's call id), message_seq (link
  messages), name, args (TEXT json), result (TEXT, nullable until it lands),
  err (nullable), started_at, ended_at.
- `usage`: message_seq (primary, link messages), prompt, completion,
  cache_read, cache_write.
- `files`: session_id + path (primary), hash, mtime. `Session.Files`
  persisted, so a resumed session keeps its drift checks. `files` pairs its
  composite primary with `session_id` directly and carries no Session link
  while its siblings do — harmless (no FK is emitted) but named for
  consistency. Tool results live on `tool_calls`, not as role=tool rows.
  Session resume (deliverable 7's `state.Resume`, the root's `-resume`)
  projects `[]core.Message` back from the transcript rows — no schema
  change was owed, and none was taken; deliverable 9's `sessions` command
  reads the same rows. After a compaction (SPEC_COMPACT 5) the marker is
  the projection interface: resume starts from the last `[compaction]`
  row when one exists — the window is the summary row and everything
  after it — so the compacted shape rebuilds, not the full history with a
  summary after the tail it summarizes.
- `faults`: seq (primary), session_id, at, message.

The recorder is a leaf that receives what the loop already emits: it wraps
the `Frontend` (an observing Frontend that forwards to the real one) and
sources its rows from the loop's events — the transcript, `ToolStart`/
`ToolResult` (the guarded result, as the loop appends it), reasoning, usage
with the cache fields. It appends a row per event inside its own short
transaction, so a kill leaves every completed row readable. A `TurnEnd`
discards the unlanded partial of a turned turn; each turn boundary upserts
the file provenance. No loop change — and, with the middleware tap retired
(deliverable 7), the schema did not change either.

SPEC_COMPACT 5: a `Compacted` event lands the summary as a marked user
row (`role = "user"`, the content verbatim with the `[compaction] `
marker) plus a usage row against that row's seq, then re-lands the kept
tail after it — fresh rows (fresh seqs), the assistant calls with
recorder-minted fresh ids (the `tool_calls.id` primary key), name/args/
result verbatim, the earlier rows staying as the autopsy. No schema
change: the marker is the contract, and the rows are existing shapes.

Deliverable 9 (SPEC_COMMANDS) reads this store from the `sessions`
command, one owed read landed there: `ListSessions` — the workspace's
session rows, newest first, capped at 50 (a glance, not an archive),
each with the turns count defined as the session's `role = 'user'` rows
minus the `[compaction] ` summary rows (transcript machinery, not
prompts), an unclosed row (`ended_at` NULL) rendered as `exit open` —
the one place that word appears; the store's exit vocabulary stays
`ok | fault | cancelled`. And two small recorder additions for the
`new` / `sessions resume` handoff (SPEC_COMMANDS 4): `Ensure` (the
session row exists before any row lands under the id, idempotent) and
`Retarget` (the retiring recorder is re-pointed before its in-flight
`Input` completes, so that row lands under the new session). No schema
change: the rows are existing shapes.

`-p` workers get their autopsy from this: the `sessions` command (roadmap
deliverable 9) or plain `sqlite3`.

### todo (port; TODO_SPEC.md A, Rev 2, TASK_TREE_SPEC.md A)

- `meta`: key (primary), value.
- `events`: seq (primary, minted, strictly increasing), ts, op
  (create|start|complete|fail|retry|move|compact), args (TEXT json), session
  (nullable).
- `tasks`: id (primary, `tN`), text (unique via extra.sql), status
  (pending|in_progress|done|failed), pos, created_seq (link events),
  updated_seq (link events).
- `task_deps`: task_id + depends_on (primary; both link tasks), created_seq.
- `extra.sql`: `tasks_pos_seq` index on (pos, created_seq); the unique index
  on text.
- Semantics kept verbatim: projection rebuilt from the log on every call and
  never trusted; replay is total and skips inapplicable rows; positions
  minted never mutated; move via events; claim semantics (start claims,
  foreign complete refuses, fail frees; completing your own unclaimed
  pending task implicitly claims and completes — start+complete, both
  events, the echo noting auto-started); compaction past 1000 events
  snapshots the queue and resets the epoch; dependsOn DAG validated at the
  boundary, cycles refused, completion gated, blocked skipped by `next`.
- The FSM lives in `store/todo/todo.go` as Go, errors in pane's teaching
  voice; the generated domain is only the substrate it writes through.
  Two raw arms are owned and named as such: the event scan that rebuilds
  the fold (no ordered scan accessor is generated) and the projection
  rewrite (no bulk-replace accessor is generated).

### rem (port; REM_SPEC.md D, E, F, G)

- `meta`, `memories` (id primary minted from a `meta` counter, never
  reused: REM_SPEC's AUTOINCREMENT rule, kept by minting since the DDL
  camera emits plain PRIMARY KEY), scope, scope_label, kind, content,
  source, importance, strength, access_count, superseded_by (link memories,
  nullable), created_at, last_accessed_at, last_consolidated_at,
  content_md5.
- `trigrams`: memory_id (link memories) + gram.
- `extra.sql`: unique (scope, content_md5); (scope, created_at); trigram
  indexes; and the ON DELETE SET NULL behaviour of `superseded_by` (the DDL
  camera leaves foreign keys off by design, so the store's prune clears
  supersession references itself before deleting, in the same transaction;
  named test).
- `fts.sql`: `memory_fts` FTS5 virtual table with `porter unicode61`, and
  the insert/update/delete triggers pinned in REM_SPEC E. FTS5 is compiled
  into modernc's sqlite; recall's shipped policy on capability absence
  degrades to fuzzy-only (REM_SPEC's named case), and a driver without
  the table fails loudly at schema application — unreachable under the
  bundled driver.
- Recall stays two arms plus reciprocal rank fusion, project-first
  global-fill, effective strength at recall, checkpointed decay at prune.
  These are Go over the generated substrate plus the two raw arms
  (`memory_fts` MATCH and the trigram join), which are the only raw queries
  the store owns and are named as such.
- `AutoReflect` ships in the store with no caller yet; compaction wires
  it (roadmap deliverable 8).
- lift's `cmd/rem` is a different design (postgres, mesh, episodes and
  associations). Its recall projection and testkit are worth reading; its
  schema is not the one being ported. pane's rem is.

### scheduler (port; SCHEDULER_SPEC.md, scheduler/store.ts, and the
post-merge corrections)

- Two files: `global.sqlite` and `<sha1(cwd)[:12]>.sqlite`, same as pane.
- `events`: seq, ts, op (create|pause|resume|remove|run|compact), args,
  session.
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
- The `defaultModel` fallback constant stays in the store, named (SPEC_CONFIG
  5, 8): the tool's default job model moved to the config chain (the file's
  `defaultJobModel` over the embedded value), and the tool always passes a
  non-empty model — the constant is the direct-`Create` path's safety net,
  not a second source. No schema change, no path change.

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
list|pause|resume|remove|runs}`. Descriptions and schema property text are
pane's promptGuidelines, lowercase, terse.

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
  off `Frontend.Notify`; with deliverable 7's loop events the tool rows are
  event-sourced and the middleware tap is retired — the schema had already
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
- **Generated getters return `(nil, nil)` on absent keys** — lazy.go's
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
- Recorder: kill a `-p` run mid-turn (context cancel inside a scripted tool)
  and assert every row that completed before the kill is readable; assert
  the session row is closed with `exit=cancelled` on the clean path.
- Corruption: a truncated file at Open is quarantined and named; FTS5
  absence at Open is a loud refusal.
- `go build ./...` from a clean clone of rig is a test: no lift import
  survives generation.

## scope

Four stores, four metadata packages, generated domain/ddl (plus sqliteread
where a tool reads trees), one seam, one driver, four thin tool adapters,
and one `core/` change (session id). Order of work: `store/sqlx`+`store/lazy`+`open.go`, then `state` (the
workers need it), then todo, rem, scheduler in roadmap order. The loop is
byte-identical throughout.
