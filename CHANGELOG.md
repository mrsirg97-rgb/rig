# Changelog

## [Unreleased]

- **the scheduler's doors come to the dashboard** (`specs/SPEC_SERVE.md`):
  `POST /api/scheduler/pause|resume|remove|update` and `GET
  /api/scheduler/runs?id=jN&n=` stand beside the create — each calls
  the store verb the `scheduler` tool calls (`Pause`/`Resume`/`Remove`/
  `Update`/`Runs`) with the `dashboard` attribution, behind the same
  walls as every write (POST, the Origin check, the body cap, the id
  checked to the tool's shape `jN`, the store's refusals by name: an
  unknown id, a removed one, an update with no change), and the reply
  is the store's voice, verbatim. The phone gets the row's hand: every
  job row carries its controls (pause or resume by state, remove, and
  the runs' audit trail) beside an update form that opens in place
  with the row's current fields (cadence, prompt, model, cwd, busy)
  and submits only what changed; the list re-reads after a move, no
  page reload. Below 720px the row stacks under full-width 44px tap
  targets (`.rowact`), the form is one column, the buttons stop
  propagation so a tap never opens two things, and remove asks once,
  in-page.

- **the scheduler gets `update`: change a job without losing its
  trail** (`specs/SPEC_STATE.md`): `{"action": "update", "id": "jN", …}`
  is partial — any of `prompt`, `cron`/`at` (mutually exclusive in one
  call, refused by name; create's refusals apply verbatim: a bad ISO, an
  invalid cron, a `once` without its `at`), `model`, `cwd`, `busy`,
  `name`. No fields → `update needs a change`; an unknown id, or a
  removed one, refuses by name; `name` keeps its store-wide uniqueness.
  One `update` op is appended and the fold overlays only the fields it
  carries: the id and the runs stay — remove + create is the rejected
  alternative (the id re-mints, the runs orphan, two crontab moves
  express one change; the trail surviving an edit is the point of having
  one). A cadence change rewrites the job's one crontab line under the
  same key; a paused job stays paused (its line rewritten commented) and
  the new line lands on resume; `pause`/`resume` stay their own ops and
  `update` never changes the state. The `/scheduler` line takes the verb
  free (the command is tool-backed): `update <id> [name <n>] [model <m>]
  [cwd <dir>] [busy <skip|force>] [cron <5 fields|once>] [at <ISO>]
  [prompt <the rest of the line>]` — prompt is last so a prompt that
  says "model" keeps its tail. The tool menu's cost is the one enum word.

## [0.17.1] — a read names its staleness

- **a read that finds a stale observation names it** (`specs/SPEC_CORE.md`):
  when the session's recorded FileState for a path no longer matches
  on-disk, the read's content opens with `[changed since your
  observation] <path> — re-read before acting on it`, and the fresh read
  re-records so it says it once — the drift refusal moved from the edit
  to the moment the model can still act on it.

## [0.17.0] — the soak's vitals

- **sessions, the soak's vitals** (`specs/SPEC_STATE.md` and
  `specs/SPEC_COMMANDS.md`, amended): a read-only native tool (the
  eighteenth) over the session store — `list` is the recent sessions
  (id, started, model, version, turns, faults; newest first), `summary`
  the vitals over the same slice (session and turn counts, the models
  with their versions, the fault count with the latest fault's first
  line, the aggregate cache ratio). The store gains the typed fault read
  (`SessionFaults`); `ListSessions` takes a named limit (`ListCap` the
  default and the maximum) and `SessionRow` carries the model, the
  version, and the fault count. `project` names another workspace (the
  state file is cwd-keyed, one per workspace — a subdirectory or a
  second worktree reads its own file); `n` caps the slice at 1..50.
  The operator gets `sessions summary` (this workspace, the tool's
  reply verbatim). Read-only: absent from the root's `mutatingNatives`
  (it never pauses at the gate) and from the concurrent read set (it
  opens a store, like todo/rem/scheduler, so it is not a pure
  observation).


## [0.16.1] — the kernel knows where it is

- **the python kernel is born in the session's working directory**: it
  inherited the rig process's cwd, so a session started one directory
  up ran kernel code whose relative paths silently pointed at the wrong
  place — a model named it from inside. `SetCwd` on the tool, wired at
  the root; the description says so.

- **grep's glob names its rule**: the schema line reads "a path glob
  (** spans directories; * does not cross /)" — `*.go` matching nothing
  cost a model a call; find already advertised the rule, grep now does.


## [0.16.0] — every row a project

- **rem, the deliberate project** (`specs/SPEC_STATE.md` and
  `specs/SPEC_COMMANDS.md`, amended): learn/reflect/recall/prune gain an
  optional `project` — a path, resolved through `store/scope`, replacing
  the session cwd for that call (worktree-safe, `~` expands at the
  `middleware/paths` boundary), so a session in `~/Projects` learning
  about `~/Projects/rig` files facts into a scope the repo recalls.
  `project` + `scope: global` refuses by name; the description carries
  one short Guidelines clause ("name project when the fact belongs to a
  repo you did not start in"). The operator gets `rem project <path>`:
  that project's live memories in `rem list`'s shape (empty names the
  project); show/forget stay id-addressed and file-wide — the 0.13.0
  forget wall stands.
- **todo is one store, scoped by project** (`specs/SPEC_STATE.md`,
  amended): the per-cwd partition is gone — one `todo/todo.sqlite`,
  every event row carries its `scope`, and the identity is never a
  filename. The scope is the repo (the short sha1 of the git common
  dir, extracted into `store/scope`), falling back to the cwd hash
  outside a repo: a session started in `~/Projects/working on
  ~/Projects/rig` files into the right queue, a renamed directory keeps
  its queue, and two worktrees of one repo share a plan. Minted ids stay
  `tN` per scope (two projects both have a `t1`); reads, folds, drift,
  and claims all filter by scope; the fold's compact and stale footer
  stay per scope. Bare read/create/every verb still resolves the scope
  from the session cwd, so the human and the agents in a directory share
  one plan.
- **the deliberate door**: every todo action gains an optional `project`
  (a path, resolved through `store/scope`; `~` expands at the
  `middleware/paths` boundary), and the operator gets `todo project
  <path>` — a one-off read of that project's queue, writes staying the
  model's or the session's own bare verbs. The empty reply names the
  queue it read (`(no tasks in <label>'s queue)`, SPEC_CORE).
- **migration, lossless**: every `<12-hex>.sqlite` in the todo dir folds
  into `todo.sqlite` with `scope = <that hash>` verbatim, in filename
  order (identity preserved without walking a hash back to a path), the
  legacy files and their `-wal`/`-shm` moved aside as `.migrated`; then
  rem's lazy re-scope moves a cwd-hash queue to the repo scope once, the
  `migrated:<oldScope>` marker, one transaction, counted once on stderr,
  and a no-op on the second open (the fold keys on the files existing).
  The dashboard's `/api/todo?cwd=` routes resolve through the same
  scope. Goldens regenerated (the schema grew `project`).
- **no colored inline text in a response** (`specs/SPEC_TUI.md` 11,
  amended): `*em*` and `` `code` `` render in the text color, marks
  dropped — bold keeps its weight, headings their accent, lists and
  quotes their furniture. The operator's call: the coloring read
  inconsistent, plain reads better. And the status row paints `auto`
  in the warn color beside `manual` — the permissive mode is the one
  worth a glance.


## [0.15.1] — the binary updates itself

- **`rig -update`** (`specs/SPEC_BUILD.md` 5): the binary's own
  installer beside `-version` — resolves the latest release by the
  `releases/latest` redirect (no API call), maps the platform into
  `rig_<os>_<arch>`, verifies the sha256 against `checksums.txt`
  before anything moves, and renames a 0755 temp over the resolved
  executable — atomic on one filesystem, a running rig keeps its old
  inode and the scheduler's next fire gets the new one. Already-latest
  is a no-op; an unwritable directory names itself and the sudo line;
  a platform with no asset and a build with no release tag each say
  so rather than downgrading.

## [0.15.0] — one store

- **the scheduler is one store** (`specs/SPEC_STATE.md`, amended): the
  cwd partition is gone — `scheduler/global.sqlite` holds every job, the
  crontab key is `jN` for all, ids are one sequence, `name` unique
  store-wide, and the `scope` arg leaves the tool's schema and the
  command's grammar (`cwd` stays the job's own field). `list` is one
  list grouped by each job's `cwd`, this directory first; an empty list
  names the store. A one-time schema-1→2 migration folds every
  `<hash>.sqlite` into `global.sqlite` (re-minted ids, runs
  re-keyed, crontab lines rewritten from `cwd-<hash>:jN` to the new
  `jN`, old files moved aside as `<hash>.sqlite.migrated`), counted once
  on stderr and a no-op on the second open. Run logs live under
  `runs/<id>/`. Goldens regenerated (the schema is on the wire).

- **a dashboard write from its own front is same-origin**
  (`specs/SPEC_SERVE.md`, amended): the Origin check accepted only the
  bound address (`127.0.0.1`/`localhost`), so behind `tailscale serve`
  every POST from the phone was `origin mismatch (same-origin only)`.
  Same-origin is now the browser's definition — the bound address, or
  the request's own front (`X-Forwarded-Proto`/`-Host`, else `http://` +
  `Host`). A foreign Origin against the real Host still refuses.

- **an empty reply names its scope** (`specs/SPEC_CORE.md`, amended):
  `grep` with a root that defaulted to a subdirectory answered "(no
  matches)" and a model read it as "does not exist", then spent four
  turns doubting a file it had just read. `ls` names the directory
  (`(empty: /abs/dir)`); `find` and `grep` the pattern, the absolute
  root and the glob (`(no matches for /re/ under /abs/root, glob
  '*.md')`); `web_search` the query; `rem` recall the scopes and the
  query (`(no memories in rig, nor global for 'q')`); `todo` that the
  queue is this directory's; `/plugins` and `plugins list` the
  directory. The `ls`/`find`/`grep` schemas say the default root is the
  working directory. Goldens regenerated.

- **the dashboard's disable/enable doors** (`specs/SPEC_SERVE.md`, 12c):
  `POST /api/plugins/disable {name}` and `POST /api/plugins/enable {name}`
  beside `approve`, each calling `plugins.Move` and replying in the
  command's voice (`disabled 'x' (plugins -> plugins/disabled); hidden
  next turn`, `enabled 'x' (plugins/disabled -> plugins); live at the
  next plugins reload`), the same walls and name refusals as every
  write. The plugins page carries the phone rule: every loaded row a
  disable control, every disabled row an enable one, the list re-read
  after the move.

## [0.14.1] — the path boundary

- **`~` is the home at the tool boundary** (`specs/SPEC_FS.md`, amended):
  one chain link, `middleware/paths`, expands a leading `~`, `~/…`, or
  `~user/…` in the path-shaped arguments (`path`, `root`, `cwd`) before
  any tool runs — read/write/edit, ls/find/grep, and bash's, delegate's
  and the scheduler's `cwd` inherit it; the tools stay pure. A model
  named the footgun from inside rig (`ls ~/Projects/x` failed where the
  absolute path worked) and the fix's shape: at the boundary, not per
  call. Bytes pass untouched when nothing expands.

- **the scheduler's cwd section names its directory**: `cwd
  /home/x/proj: no jobs` instead of `cwd: no jobs`, so an empty section
  reads as "none here", not "none anywhere" — a model read the old line
  as a lie. The description says another directory's jobs are listed
  from there.

## [0.14.0] — plugin and plugins

- **no round cap by default** (`specs/SPEC_HARDENING.md` 9, amended):
  `rounds` is `0` in the embedded settings and `guard.Rounds(0)` counts
  but never refuses — a one-shot turn with a large model on a real task
  legitimately exceeds 200 calls now that reads overlap, and a cap that
  ends a correct run mid-work is worse than the runaway it guards
  against. The retry bound and compaction still stand. `rounds: N` (or
  `RIG_ROUNDS=N`) caps the turn for an operator who wants the wall.

- **the plugin surface is two natives** (`plugin` run|schema, `plugins`
  list|create|delete|reload; `plugin_schema` and `plugins_reload` fold
  in): delete is disable — the loaded file moves into `plugins/disabled/`
  (reversible with `/plugins enable`), one `Move` shared with the
  `/plugins` command; create and the web forge share one
  `plugins.WritePending` (the filename-stem rule, the native collision,
  the DESCRIPTION/SCHEMA/def run contract — a bad name, a missing
  contract, and a collision refuse identically through both doors).

- **a settings-file allow list needs `plugins`**: a home whose
  `settings.json` writes an `allow` key replaces the embedded default
  (which is the native set), so it must carry `plugins` or every
  `plugins` call is `permission denied` — the same rule that landed
  `plugin`/`plugin_schema` in 0.12.1.

## [0.13.0] — rem is deliberate

- **the dashboard's quick hits**: the plugin listings read every
  DESCRIPTION form — the parenthesized implicit concatenation seventeen
  of the operator's plugins use listed as "(no DESCRIPTION)" (one static
  extractor in `plugins`, shared by the dashboard and `/plugins`); the
  models view shows the context window and the output length only; a
  failed task carries `retry` beside the hands (`/api/todo/retry`,
  `todo.Retry`); lines hang their wrapped text under the text, not the
  glyph, on phones.

- **rem is deliberate** (`specs/SPEC_STATE.md`): every rem operation is
  something chose — the model learns/recalls/reflects/prunes through its
  tool, the operator prunes through the `/rem` verb; nothing is written
  by a compaction and nothing is read into the prompt by a session
  start. Two cuts, one new verb surface, one scope change.

- **the injection is cut**: the root's `remembered` segment (the
  `remstore.Recent` read, `rememberedK`, `renderRemembered`) is gone —
  no memory is read into the prompt at a session start. The system
  prompt's "Remembered notes are suggestions…" becomes "Memory is a
  tool: recall before re-deriving a project fact, learn deliberately
  what the next session should not re-derive, supersede by id when the
  code disagrees" (settings.json, the goldens, the config tests).

- **the auto-reflection is cut** (`specs/SPEC_COMPACT.md` 6): the
  `WithAutoReflect` seam, `store/rem.AutoReflect`, and the
  `autoReflectionImportance` constant are gone — compaction writes
  nothing to rem. The summary stays a marked user row in the transcript
  (context, not memory).

- **scope is a repo identity**: `store/rem` hashes the absolute git
  common dir of the cwd (`scopeKey`), so two worktrees of one repo share
  one memory; a non-repo dir scopes by cwd, hashed as today. The schema
  bump (1 → 2) carries a one-time idempotent migration
  (`remstore.Migration(cwd)` through `store.Open`'s migration hook):
  rows under an old cwd-hash scope now inside a repo re-scope to the
  repo's, and rows with source "session compaction" are removed (never
  deliberate), counted once on stderr. The migration is one transaction
  and survives two processes opening the file at once (`INSERT OR
  IGNORE` on the marker). Known bound: the re-scope is keyed on the
  launch cwd — rows learned from another directory of the same repo move
  when rig is next started there, since a hash cannot be walked back to
  its cwd. The git probe resolves a relative common dir against the cwd
  and treats an echoed option (git < 2.31 passes `--path-format` through,
  exit 0) as no path.

- **`store.Open` refuses what it cannot migrate**: an older file opened
  by a build that passes no migration is a named mismatch, not a silent
  version stamp; the newer-file refusal runs before any schema statement
  touches the file; the migrations and the version bump commit together.

- **`/rem forget` is scoped**: ids are file-wide, so a typo must not
  reach another repo's row — only this project's or a global memory is
  removed; another project's id is refused by name with its label.

- **the `/rem` command** (`specs/SPEC_COMMANDS.md` 11): `rem [list|show|
  forget]` over the same store — list the live memories (project then
  global, one line each), show by id, forget by id. The model's
  multi-line learn/recall/reflect/prune stays the tool; the operator's
  read and prune get a typed line. Rejected, named: `rem pin` — importance
  is only the per-access reinforcement multiplier, so the verb changed
  nothing the operator could see, and an operator write that is not a
  prune is off the surface.

## [0.12.2] — the two bounds

- **the description's shape** (`specs/SPEC_CORE.md`, the Tool section):
  every tool's description is the same four parts — what, a
  `Guidelines:` sentence on when and when not (it was missing from 13 of
  18), the reply's shape, at most one gotcha; storage mechanics leave
  the wire for the `PACKAGE.md`s; the scheduler's "pi session" / "pi
  model id" become rig's words. A case in `cmd/rig` pins the whole
  menu under 14,000 chars on the wire and refuses another harness's
  voice, so growth is a decision; the schemas of rem, scheduler, todo,
  and diff are the next lever, named.

- **no comments in Go, anywhere**: the sweep strips every comment from
  implementation and test code (147 files; generated code and the
  `metadata` packages exempt — lift reads their doc comments), the
  substance that was not already in a `PACKAGE.md` lands there (three
  new: the root kernel, `frontend/oneshot`, `middleware/approve`), and
  AGENTS.md carries the one rule with its reason: a small model reads
  the repository as one corpus. The freeze gate now compares
  comment-stripped sources, so a comment is never a change to the
  frozen surface. No behavior change: the stripper refused any file
  whose comment-free AST would differ, and the suite is green under
  `-race`.

- **the round cap** (`specs/SPEC_HARDENING.md` 9): `guard.Rounds(n)` counts
  every tool call in a turn (settings `rounds`, default 200, `RIG_ROUNDS`
  env with a loud invalid-value refusal) and past `n` refuses every
  further call without executing, in a teaching voice naming the cap and
  what to do — stop and report, or ask the operator. It caps the
  alternation the retry bound's per-args streak does not (SPEC_HARDENING
  7's named consequence) and a runaway batch: a concurrent run of 50
  reads counts 50, the cap on calls, not turns. The counter sits under a
  mutex (SPEC_EVT 6's concurrent chain) and `TurnStart` clears it like
  the bound. `core/` and `loop/` stay frozen at 0.12.0.
- **the result bound** (`specs/SPEC_HARDENING.md` 9): `guard.Cap(bytes)`
  (settings `resultCap`, default 64 KiB) bounds every tool result before
  the transcript, in one place — an oversized result truncates to the
  head and the tail with the loud `[TRUNCATED]` marker naming the full
  size and the teaching line "re-read a narrower range"; a small result
  is byte-identical. It closes the named field failure: a 287 KB read
  result that fed the 2026-08-21 compaction fault and sat in context all
  session. Every tool's own cap stays; this is the wall behind them.
- **read's narrower range**: `tool/file`'s read gains `offset`/`limit`
  line arguments (past-the-end and negative refusals loud), so the
  teaching has a real door.
- **wiring**: both are innermost after the bound (first-listed is
  innermost) in the root's chain; workers and delegated workers run the
  same `wire()`. The TUI shows a capped result's marker the way it shows
  bash's.
- **test-only data races closed**: `cmd/rig/plugins_test.go:87` (the fake
  plugin server read `replies` the test set without the mutex; the write
  now holds it) and `tool/delegate`'s in-flight wait (`len(spawn.calls)`
  read without the mutex while the spawn goroutine appends; the read now
  goes through a locked `count()`). CI's test step runs `go vet ./... &&
  go test -race ./...` so a race cannot return.

## [0.12.1] — the door is allowed

- **the plugin door was never allow-listed** (`specs/SPEC_GROWTH.md` 9,
  amended): `plugin` and `plugin_schema` joined the native set in the
  door round but not the embedded `allow` default, so every plugin call
  through the door since 0.9.0 was `permission denied: plugin is not in
  the allow-list` — on a home without an `allow` key (the embedded
  default), which is the daily driver's. The two names are in the
  default now, and a named case pins the rule the bug broke: the
  embedded allow is the native set, exactly.
- **the plugin switch is a directory** (`specs/SPEC_GROWTH.md` 9,
  amended): `settings.json`'s `plugins.enabled` inverted the default —
  on a home with no list, `disable` changed nothing and `enable X` hid
  every other plugin — and its filter ran only at wiring, so a reload
  undid a toggle until the next start. Retired. `/plugins disable
  <name>` moves the file into `plugins/disabled/` and reloads; `enable`
  moves it back; `plugins disabled` lists the zone; the dashboard shows
  all three zones; `max` now applies on every reload. A non-empty
  `plugins.enabled` refuses at load naming the move.

## [0.12.0] — the event loop

- **the loop is the engine's consumer** (`specs/SPEC_EVT.md` 7, the
  named reopening after 2a): every step of a turn is a closure on the
  loop goroutine; the Frontend's `Input`, the provider's stream, and
  every tool run are producers that post (input 90, stream 50, tool
  completion 50). The Frontend contract, the event bracket, the
  transcript, and every named loop case are unchanged — thirty-six
  cases pass byte-for-byte under `-race`; two new ones pin the one
  goroutine and the stale-event rule. The loop goroutine never blocks
  on a tool; `Assemble` and the `Stream` call still run on it (named).
  `loop` now imports `evt`, an in-repo stdlib-only leaf.

- **the batch** (`specs/SPEC_EVT.md` 6, the named reopening of the
  frozen loop): a turn's tool calls the kernel's `Concurrent` predicate
  admits run together (bounded by `Parallel`, default 8); any other call
  is a barrier in call order; results are emitted and appended in the
  order the model asked, each with its own duration. The root admits the
  pure reads (`read`, `ls`, `find`, `grep`, `web_search`, `web_fetch`,
  `diff`); everything with effects, a store, or the shared kernel stays
  sequential. The guard and the file tool lock the state they share
  across a run. A nil predicate is the 0.11 loop, byte-identical. The
  freeze gate carries the reopening by name; the re-freeze follows the
  merge.

## [0.12.0] — the event loop

- **the event loop, phase 1** (`specs/SPEC_EVT.md`): libevt's shape
  (`~/Projects/libtrdr`) made Go-centric as the leaf package `evt` —
  `Context`, `Event`, `Queue` (the 4-ary max-heap, priority desc then
  arrival, with the one addition `Update`), `Engine` (one consumer,
  many producers, a mutex and a cond where the C spun), `Scheduler`
  (the harness, the codes as errors), `Clock`. libevt's tests by name.
  Consumed by nothing yet: phase 2, named in the spec, makes the turn
  loop its consumer (parallel tool calls as goroutines posting ordered
  completions) and reopens SPEC_CORE.

## [0.11.0] — the delegate

- **the one-shot worker tool** (`specs/SPEC_DELEGATE.md`): `delegate`
  spawns a headless worker on a task now, in a cwd under the session's
  or the rig home, waits, and feeds back the worker's last message.
  One tool over the existing runner — the jail per the sandbox setting
  (fail closed exactly as workers do), the socket proxy, the worker
  command, the GPU busy rule with `busy:skip` semantics. A failed or
  timed-out worker is a tool error naming which; a timeout kills the
  worker's process tree.
- **the record and the transcript**: every delegation is a recorded
  run in the cwd-scope scheduler store under a minted ad-hoc key (no
  crontab line, nothing scheduled), so `scheduler runs` and the
  dashboard show it beside cron runs with its log path; the worker's
  transcript is its own resumable session in the state store (the
  jailed worker's sessions dir is bound in), named by the tool result
  for `sessions resume`.
- **the bounds, named**: one delegation in flight per session (a
  concurrent call refuses); a worker cannot delegate (the `RIG_DELEGATE`
  marker, refused by name); the embedded allow default gains
  `delegate`, a worker's allow-list omits it; `delegate` counts as
  mutating for the approval gate, whose prompt shows the task's first
  line.

## [0.10.2] — the todo's hands

- **start and done from the dashboard** (`specs/SPEC_SERVE.md` 15): a
  pending task carries `start` and `done`, an active one `done`; each is
  the store's own verb (`todo.Start`, `todo.Complete`) attributed to
  `dashboard`, the reply verbatim, the queue re-read — a task the model
  claimed refuses in the store's voice (fail it first to take over), as
  the tool would.
- **the models view in three rows**: name and role, the context line
  (window · max · reserve · keep · trigger), the effort ladder in the
  ramp's colors.
- **the mobile header stacks**: the workspace on its own row, add and
  browse on the row after.

## [0.10.1] — the dashboard in the TUI's grammar

- **the dashboard speaks the TUI's grammar** (`specs/SPEC_SERVE.md`
  11–14): every view is a tool block (`● name · detail` … `name ✓`),
  input is a `❯` prompt row, no panels; the models view in the `/models`
  table's line with the effort ramp; the memory tab and route removed;
  the plugins page split approved | pending with the forge — an embedded
  python editor (gutter, highlighting, no dependency) over three doors:
  source by zone, save into the pending zone (the contract checked, a
  native name refused), approve (the command's verb; an installed name a
  409 until `replace`); a folder browser for the workspace picker, rooted
  at home, directories only, symlinks resolved, capped; the mobile nav
  toggle hidden on desktop (the bug: it was hidden by a class the button
  did not carry).
## [0.10.0] — the dashboard's polish round

- **the polish round** (`specs/SPEC_SERVE.md`, phase 2): the local
  dashboard grows its two named phase-2 writes — a scheduler create
  (the same `scheduler.Create` verb a live session calls, attributed to
  `dashboard`, the runner command the root wires) and a plugin create
  (one contract file into `plugins/pending/`, the provenance rule's
  landing zone, refused by name against both zones). The plugin listing
  goes live (read per request, never cached), the cwd picker accepts
  new workspaces (client state over the server's list), and the sidebar
  collapses into a drawer below 720px.
- **the TUI homage** (phase 2, decision 10): the page adopts the TUI's
  visual grammar — the todo and scheduler text are parsed with the
  `frontend/tui` `tools_render.go` rules and rendered in the oled slots
  (the progress bar, the status glyphs, the dim metadata, the warn
  `drift:`), the sessions and transcript take the tool-block shape
  (the `✓ name · detail` opening, the `❯` prompt, the `→` result, the
  aggregated usage row), and unparseable text keeps the verbatim
  `<pre>` (the TUI's own fallback).

## [0.9.2] — the streamlined contract and the self-healing door

- **the contract split** (`specs/SPEC_STREAMLINE.md` 1): the standing
  context carries shape, the responses carry semantics. The todo,
  scheduler, and rem descriptions trim to the verbs and the arguments;
  the state machine, the claim rules, the auto-start, and the
  compaction sentence ride the voices that already teach them on
  contact. The pinned goldens regenerate in place (the SPEC_PLUGINS 8
  precedent); the wire carries the trimmed bytes.
- **the compaction fact** (2): the operation that crosses the
  1000-event threshold names the fold in its own reply —
  `· log compacted (N events folded into the snapshot)` — so the stale
  footer's quieting after the fold reads as explained, not as state
  loss. Below the threshold the replies are byte-identical to
  0.9.1's.
- **the minted-id voice** (3): the unknown-id refusal at every verb
  now reads `no task 'tN' (ids are minted by the tool; copy from a
  reply)`, the teaching on contact instead of standing prose.
- **the door's self-heal** (4, 5): the `plugin` and `plugin_schema`
  doors take a redo seam; an unknown name runs the root's reload once
  and re-resolves, a nil redo keeps today's refusal, and a failing redo
  is named in the refusal. The authoring dance loses its reload step —
  write to pending, the operator approves, the model calls the door —
  and the `/plugins create` template says so. `plugins_reload` stays
  the operator's explicit verb.

## [0.9.1] — the estimate tells the truth

- **calibration trusts a measurement, not a turn** (`specs/SPEC_COMPACT.md`
  4, amended): a delta under 2% of the anchor no longer moves the factor
  — a tool-loop turn's `reported − anchor` is the template's overhead and
  the reasoning it keeps or strips, none of it in the delta's bytes; the
  clamp ceiling is 2.0. The field shape: per-turn ratios of 44, 0.31,
  31.8, 0.02 pinned the factor at 4, the brain compacted 20 times at ~50k
  real tokens, and a 125k summary input read as 427k and faulted.
- **history reasoning leaves the estimate**: `Reasoning` counts on the
  last assistant message only — every chat template strips it from prior
  turns, so the server never counted the 8.3 MB one session carried; a
  `-resume` of it would have compacted everything on the first assemble.
- **the oldest slice that fits** (3, amended): an older prefix whose
  summary input does not fit the window is cut to the largest prefix that
  leaves the summary floor, one call, the remainder folding on a later
  pass — where it was a fault that stuck the session until `/new`. A
  single message that alone does not fit is still the loud failure.

## [0.9.0] — the plugin door and the enablement

- **the plugin door** (`specs/SPEC_GROWTH.md` 9, SPEC_PLUGINS 7's named
  "later decision once the count grows"): the count has grown, and the
  flat shape is the context problem. `toolset.Carry` stamps the natives
  plus one `plugin` door into every request instead of every per-plugin
  schema; `plugin_schema` fetches one plugin's contract on demand.
  Plugins stay real `core.Tool`s in the table, callable by the python
  tool and by the door's name. The door's `name` enum is the live
  plugin names (the swap's own list), cheap.
- **the enablement** (`settings.json` `plugins.enabled` + `max`, SPEC_CONFIG):
  a disabled name is not wired as a tool (the door's enum carries enabled
  plugins only), hidden entirely; `max` caps the enum. `/plugins enable
  <name>` / `disable <name>` toggle the file and reload — the
  models-switch semantics, next-turn.

## [0.8.2] — the plugins land

- **the allow-list's presence reversal** (`specs/SPEC_PLUGINS.md` 7,
  amended): an installed plugin's presence in `plugins/` root is itself
  the allow-list entry. The provenance rule forces a model's
  `write`/`edit` into `plugins/pending/`, so a plugin in the root can
  only have arrived by the operator's `/plugins` approve — that presence
  IS the admission, and the operator need not add a name to `allow`.
  The mechanism is a second door (`perm.AllowlistWithDoor`) wired to the
  live plugin table's `IsPlugin`: a name the table carries as a plugin
  passes though absent from the static list. Plugins only, never natives
  (the collision rule keeps the sets disjoint); nil door is today. Pinned
  with approved-passes / pending-refused / deleted-after-reload-refused /
  native-still-refused. `loop/` and `core/` stay byte-frozen.
- **the plugins are a live surface** (the local install): `listen`,
  `net_conn`, `syshealth`, `url_check`, and `plugin_scaffold` land as
  real tools — read-only probes, the SSRF-guarded fetch, and the
  scaffold that writes the next plugin into `pending/`. `plugin_scaffold`
  closes the loop: author, approve, reload, land.

## [0.8.1] — the walls speak

- **the system prompt names the walls** (`config/settings.json`): the
  default grows from two sentences to five — the harness enforces an
  allowlist, a retry guard, an approval gate, and a plugin landing zone,
  names each refusal, and a refusal is final for that call (change it or
  ask, never reach the same effect through another tool); remembered
  notes are suggestions, the code and the spec are the truth; python is
  a persistent kernel and a capability built twice belongs in a plugin.
  Operator-overridable as before (`system`, `RIG_SYSTEM`, `-system`).
- **python's action vocabulary is closed** (`specs/SPEC_PYTHON.md`,
  amended): `code` (or no action) runs the code, `vars`/`reset` are the
  host's, anything else is refused by name before the kernel is touched.
  The field failure: a model sent `action: "code"` on every call, the old
  dispatch forwarded it without the code, the host ran the empty string,
  and 457 calls came back `(no output)` ok — a silent success that ran
  nothing. `code` joins the schema enum.
- **the guard's spec says one thing** (`specs/SPEC_HARDENING.md` 7):
  the streak is per tool with a last-failed-args marker — identical
  retries cap, a changed call resets; the named consequence, alternating
  failing calls never trip the bound; the test is named for what it
  asserts (`TestDriftingArgsEachGetAFreshStreak`); `bound.go` carries no
  comments.
- **the flaky kernel test was a race** (`tool/python`): the unwritable-
  kernel host now closes stdin before it answers, so the client's next
  write is EPIPE by construction instead of sometimes waiting out the
  timeout on a loaded runner. Flaky is a bug, never a rerun.
- **distribution, proven**: v0.8.0 shipped through the tag path on the
  first try; the installer at `https://mrsirg97-rgb.github.io/rig/install.sh`
  verified end-to-end; the repo is public.

## [0.8.0] — the modes

- **`/effort`** (`specs/SPEC_MODES.md` 1): the session's reasoning
  budget in the row's own vocabulary (`models.json` `efforts`) — a
  provider decorator stamps the dial onto requests that carry none;
  the compaction summary's own effort survives untouched; unset is
  today's bytes. A model switch resets a level the new row does not
  name — loudly, in the `/models` reply — never stamping a level into
  a template that cannot speak it.
- **`/role`** (2): default, architect, or reviewer — the stance's
  prose sits between the system prompt and AGENTS.md (position is
  precedence: the contract reads after it and wins), rebuilt on the
  switch, next-turn.
- **`/approve`** (4): auto (today) or manual — every mutating tool
  call pauses for the operator's y/n at a TUI ask row (y runs, n
  declines, Esc declines and interrupts; a denial is a model-visible
  teaching refusal, the turn continues). The gate is a middleware
  wired with closures; the ask door is an optional frontend
  interface — `core/` and `loop/` byte-frozen, the Frontend seam
  untouched. The read set and the store tools pass silently; every
  plugin pauses. The default is settings.json `approve`; workers and
  the one-shot never ask.
- **the status is three rows** (3, amended): identity (`model ·
  used/window`), the stance (`effort · role · auto|manual` — the
  effort in pane's footer ramp colors, new `effort*` theme slots;
  the role abbreviated; manual in the warn), and the usage totals.
- **the distribution surface** (`specs/SPEC_BUILD.md` 5): the first real
  tag ships through a release workflow (the tag asserted against the
  `Version` const before any asset is built, `CGO_ENABLED=0` cross-builds
  linux/darwin x amd64/arm64, checksums, provenance attestation, the body
  from the matching CHANGELOG section), a POSIX installer fetches and
  verifies the binary, and a static install site serves it.

## [0.7.0] — the reload and the forge

- **plugins register without a restart** (`specs/SPEC_PLUGINS.md`
  decision 8): `plugins_reload`, the fifteenth native — re-runs the
  discovery over the rig home's `plugins/` directory (the same loud
  skips, the same collision refusal, removal free: the list rebuilds
  from disk) and swaps the kernel's tool list at the root. The seam
  is named and built (`middleware/toolset`, pure core): the root owns
  one live tool table — a provider wrapper stamps the table's specs
  into every request before delegating, and a middleware, innermost
  first, resolves a call against the table before the chain's
  participants bound its result, falling through to the loop's own
  exec. A swap is one atomic write to that table: the next turn's
  request carries the new list and the new tool executes, by
  construction — the models-switch's semantics, **zero `loop/` and
  `core/` lines** (both byte-frozen against the branch's base), the
  loop's snapshot is the bootstrap and the table is the truth.
  `/plugins reload` is the operator's verb (the same re-discovery,
  from the command door); `/plugins create <text>` queues the
  authoring prompt (the steer precedent: the command queues a line,
  never dispatches a turn) — the forge's contract, the model
  authors, the plugin lands in the pending zone (SPEC_SANDBOX's
  provenance, the gate this decision waits on), the operator
  approves. **The approve's tail is the reload's**:
  `/plugins approve <name>` moves the file and re-registers, and the
  next `/plugins` listing follows the swap (the listing reads the
  root's state at call time). The reload imports into the running
  kernel, so a new plugin's functions are callable from the python
  tool immediately — the shared namespace, one process — and callable
  as a tool on the next turn. The no-plugins wire's golden pin moves
  with the set: the fixtures regenerate in place (the 0.2.0 wire
  baseline, the 0.7.0 native set — 15, `plugins_reload` among them),
  as the earlier releases' did. `core/` and `loop/` zero diff.
  Version 0.7.0; still pre-1.0.

## [0.6.0] — the worker jail

- **the scheduled worker runs jailed** (`specs/SPEC_SANDBOX.md`
  decisions 1, 3, 5): the `run-job` runner spawns the worker under
  bubblewrap's `--unshare-all` profile — the spec's block, verbatim,
  composed by a pure function (`store/scheduler/jail.go` pins it
  line-for-line): the ro system, the fresh `/proc`/`/dev`/`/tmp`, the
  job's cwd rw, the operator home's kernel directory and the rig
  binary ro, the socket's one bind, and the worker payload. The
  worker is netless except **one unix socket** — the runner's socket
  proxy (stdlib `net` + `httputil`, no new Go dependency) listens on
  it and forwards to the swap endpoint, the OpenAI `/v1` prefix
  applied exactly once, whatever the operator's spelling; the socket
  is removed after the run, nothing answers. The worker's rig home is
  the **scratch home** `<job cwd>/.rig-job`: the worker's stores land
  inside the jail, and a worker that cannot write the operator's
  stores cannot poison the next session's transcript — the
  operator's `~/.rig` is byte- and mtime-untouched after the run.
  **Fail closed is the default**: `sandbox: "jailed"` refuses the run
  loud (recorded as a skip, the outcome row carries it) when bwrap is
  absent, on a non-linux platform, or where the operator home's
  kernel directory is absent — the refusal names bwrap and the
  profile and teaches both settings keys; `sandbox: "off"` is the
  operator's explicit act, runs the worker as before with exactly one
  loud line per worker run. `sandboxBinds` rides the profile as extra
  binds (an absolute path, ro by default, `:rw` opts one in — the
  operator's venv need). The provider dials a `unix:` base URL
  (socket transport, the OpenAI path clean on the wire). The
  interactive REPL never consults the sandbox code (the fake PATH
  shim's marker stays untouched, the golden path is byte-for-byte
  the model's reply). The real-jail fixture jobs gate on a box that
  can run unprivileged bwrap (the probe is the profile's mechanics;
  the skip names the box's
  `kernel.apparmor_restrict_unprivileged_userns`); a bare box skips
  cleanly, no flake. SETUP names bubblewrap as the jailed worker's
  one environment dependency (the package is `bubblewrap`, the
  binary `bwrap`) and the Ubuntu 24.04 sysctl's named workaround.
  `core/` and `loop/` zero diff. Version 0.6.0; still pre-1.0.

## [0.5.0] — the plugin provenance rule

- **creation separated from installation** (the forge's gate,
  `specs/SPEC_SANDBOX.md` decision 2): `~/.rig/plugins/`, top level,
  stays the TRUSTED set the discovery loads; `~/.rig/plugins/pending/`
  is the FORGE's landing zone — invisible to discovery by the existing
  top-level rule (no loader change at all), created at startup, silent
  and idempotent (the write tool makes no directories, so the model's
  first pending write must not depend on the operator's mkdir). The
  perm middleware gains one path rule beside the allow-list: the
  model's `write` and `edit` refuse a target inside `plugins/` that is
  not inside `plugins/pending/`, and the refusal teaches —
  `permission denied: <path> is in plugins/ outside plugins/pending/
  (plugins install by the operator's /plugins approve; write to
  plugins/pending/)`. The rule is the guard for the honest path, not
  the boundary: bash can still move a file there (the operator's shell
  is the operator's); the worker jail is the boundary, the provenance
  rule is the workflow. `/plugins pending` lists the zone with each
  file's DESCRIPTION (the file's top-level string literal, read without
  running the file — a pending file is untrusted); `/plugins approve
  <name>` moves one to the top level with the atomic rename — a name
  that collides with a native tool refuses with the existing rule's
  voice (the existing rule at the new door), and a file of the name
  already installed refuses too (a clobber is not an operator's verb by
  accident). Approval is the operator's verb: it never runs from a tool
  call (the command door is Frontend-side by construction). The reload
  is SPEC_PLUGINS decision 8's, and the forge (SPEC_PLUGINS 8) is
  unblocked by this. `core/` and `loop/` zero diff. Version 0.5.0;
  still pre-1.0 — the freeze's discipline and the tag's criterion
  unchanged.

## [0.4.0] — the rig home and the python plugins

- **the rig home** (the `~/.config/rig` move, `specs/SPEC_CONFIG.md` 11):
  the config home is `~/.rig` — the `.pi`/`.omp` convention, not the XDG
  one. The resolution is stated once: **`$RIG_HOME` > `~/.rig`** — the
  env var (non-empty) is the home, the operator's spelling used as-is;
  unset, `~/.rig`. `RIG_HOME` is the one new env var (the config spec's
  non-goal's named amendment); `XDG_CONFIG_HOME` is no longer consulted.
  The migration is once and deterministic: at startup, if the resolved
  home is **absent** and the old `~/.config/rig` **exists**, the old
  directory is **renamed** to the resolved home (atomic — both live
  under `$HOME`) and exactly one line says so; a present home wins,
  whatever the old directory holds, a failed rename refuses the
  start loud, and it is a **default-path event**: under an explicit
  `RIG_HOME` the migration never runs (the override is isolation, not
  a move order — an absent override stays absent, the old home stays
  put). The stores, the python kernel's materialised host
  (`~/.rig/kernel/`), and everything else ride the home; the root and
  `tool/python`'s host resolution carry the same one rule, named.
  `config.Load(dir, cwd)` keeps its seam; the invariant's companion
  holds: a fixture run has neither home, so the 0.2.0 wire stays
  byte-exact.

- **python plugins** (the pre-1.0 extension surface,
  `specs/SPEC_PLUGINS.md`): one file under `~/.rig/plugins/`, one tool
  per file, the name the filename stem. The file's contract is three
  names: `DESCRIPTION` (str), `SCHEMA` (dict, the wire's
  `function.parameters`), `run(args: dict) -> str`. Discovery at startup
  through the **shared python kernel** — the same persistent kernel as
  the `python` tool, one process, the namespace shared on purpose (the
  model's python can call plugin functions directly; plugin state
  persists across calls) — imports each file, reads the three names,
  and registers the tool on the existing `core.Tool` seam,
  indistinguishable from a native tool on the wire (the wire's head is
  the 14 natives in order; the plugins ride the tail, in file order). A
  file missing a piece or failing import is a **loud skip** (one line
  naming the file and the field — the kernel's own voice — startup
  continues); a name colliding with a native tool is a **loud refusal**
  at startup (native-wins would be silent shadowing — refuse instead);
  a kernel-level discovery failure refuses loud (fail closed). A call
  invokes the module's `run` with the model's args dict in the same
  kernel: the return `str()`s into the tool result, an exception is a
  tool error carrying the traceback tail, and the kernel stays alive.
  No plugins directory (or an empty one) is a no-op that never starts
  the kernel — the `golden_020` fixtures are untouched. `/plugins` is
  the standard set's eighth entry: the loaded (name, description, file)
  and the skipped (file, reason), in file order. The plugins are
  subject to the allow-list like any tool and are **not** in the
  built-in default (the operator allow-lists a plugin's name). The
  sandbox is named and deferred: pre-sandbox, trust the plugins as you
  trust your own python. `core/` and `loop/` zero diff; the `plugins/`
  leaf and the `tool/python` `Run` door (the raw reply the discovery
  and calls ride) are the only new surface. Version 0.4.0; still
  pre-1.0 — the freeze's discipline and the tag's criterion unchanged.

## [0.3.0] — config as a first-class runtime component

- **config loading** (the pre-10 runtime component, `specs/SPEC_CONFIG.md`):
  flags, env, and constants become a four-layer resolution per key —
  **flags > env > file > embedded defaults** — with the defaults moved out
  of code into the embedded `config/settings.json` and
  `config/models.json`. `config/` is a new stdlib leaf that parses; the
  root (`cmd/rig`) consumes; one `config.Load(dir, cwd)` per process,
  after flag parse and before any store is opened, on every entry mode
  (REPL, `-p`, `run-job` — the worker inherits its own job-cwd's
  `AGENTS.md`, not the creating session's). The user files under
  `~/.config/rig/`: `settings.json` (the existing knobs, flat, by their
  env names; `allow` a JSON array there, CSV in the env), `models.json`
  (the table out of code: `models.Defaults` removed, the user file merges
  over the embedded row by row — set fields replace, unset fields keep,
  new ids added with required numerics; rows gain `role`
  (interactive/worker, the `/models` column) and `effort` (the
  compaction summary call's reasoning effort, `""` = the policy's
  `medium`)), `AGENTS.md` (global then `<cwd>/AGENTS.md`, placed between
  the system prompt and the participants' guidelines), and `theme.json`
  (reserved for the TUI: the loader reads it raw, well-formedness only).
  Malformed or unreadable files refuse at start, naming the file and the
  field (the operator's JSON spelling); absent files are silent; unknown
  keys refuse. The two presence keys (`webFetchProxy`, `trafilatura`)
  keep the 0.2.0 set-empty semantics at every layer; `RIG_MODEL_*` now
  overlays the active id's row fields, set beats the row; the scheduler's
  default job model moves to the settings (`defaultJobModel`, file over
  embedded; the store keeps its constant as the direct-`Create` safety
  net). **The invariant**: with no user files, every entry mode is the
  0.2.0 bytes — pinned against golden request-body fixtures captured from
  the 0.2.0 build (the named exception: the `/models` role column).
  Version 0.3.0; `core/` and `loop/` zero diff.

## [0.2.0] — the feature-complete runtime

- **user commands** (roadmap deliverable 9, `specs/SPEC_COMMANDS.md`): the
  command seam and the first seven commands — the human's own verbs, over
  the same `core.Command` registration as the tools (`WithCommands`),
  dispatched Frontend-side by the `/` prefix before `Input` returns to the
  loop (zero loop change; a frontend without dispatch stays byte-identical,
  `//` escapes the prefix, an unknown command is a loud refusal). `compact`
  forces the compaction policy's exported `Compact(ctx)` seam (the caller
  owns the `Compacted` delivery); `new` closes the session row `ok`, mints
  a fresh session and recorder, and re-targets the retiring recorder before
  its in-flight `Input` completes (the handoff); `sessions` lists (newest
  first, capped, turns = user rows minus `[compaction]` rows, unclosed rows
  render `exit open`), shows (the `-resume` projection, rendered plain),
  and resumes (validate-before-mutate over the real store); `models` lists
  the runtime table (the env-synthesized row included) and switches the
  active model by rebuilding the provider+policy pair at the root, effective
  on the next turn's request; `steer` is the deliverable 7 slot made a verb
  (queue-and-interrupt, latest wins); `todo` and `scheduler` parse the line
  into the same tool instances the model gets and print the reply verbatim.
  One-shot and `run-job` never dispatch: a command-shaped prompt is a
  prompt. The runtime is feature-complete (Version 0.2.0; the freeze
  discipline holds, the 1.0 tag waits for lived use); the loop is
  byte-identical through 9.

- **openai adapter: truncated tool calls** (fix surfaced by the compaction
  live e2e): a stream cut off by `max_tokens` can cut a tool call's
  `arguments` mid-JSON while still reporting `finish_reason: "length"`. The
  adapter now checks the accumulated args with `json.Valid` before emitting
  the `ToolCallEvent`; invalid args fault with the truncation and the finish
  reason named, no partial call in the transcript, no `Done`. Empty args
  (a no-arg call) stay legal. Named test
  `TestLengthFinishedTruncatedToolCallArgsFault`, complement
  `TestNoArgToolCallStillEmitted`; the invariant is recorded in the event
  contract (`specs/SPEC_CORE.md`).

- renamed looper -> rig

- **compaction** (roadmap deliverable 8, `specs/SPEC_COMPACT.md`): the
  first non-passthrough `ContextPolicy` — `policy/compact` wraps the
  passthrough (byte-identical below the trigger) and, at a window-relative
  per-model trigger, rewrites the older transcript into a summary through
  the same `core.Provider`, keeping a whole recent tail (`models` table:
  `local` and `qwen3.8-workers`, `RIG_MODEL_WINDOW`/`RESERVE`/`KEEP_RECENT`
  env synthesis, loud refusal at start). The trigger anchors on the
  server's own count (`Message.ContextTokens`, loop L8 stamps
  `Done.Usage`'s prompt+completion), calibrating only the delta. Overflow
  recovery: a provider decorator classifies a context-length fault and
  compacts-and-retries exactly once, never a silent loop. The summary is a
  marked user row (`[compaction] `) the CLI renders as one line, the
  recorder lands it plus its usage row and re-lands the kept tail
  (fresh seqs, fresh call ids), `-resume` projects from the last summary
  row, and the summary is handed to rem's `AutoReflect` (deduped,
  low-importance, scoped to the cwd). `-p` workers get the same wire and
  numbers; `Request.MaxTokens` carries the request-side reserve on the
  wire. **Refuse-loud clamp**: when `Window - est(request)` is below the
  minimum (the smaller of `Reserve/4` and 256) the decorator fails loud
  (a `Fault`, so `-p` exits non-zero) instead of the floor-1 one-token
  answer that logs success — a kept batch larger than the model can hold.
  The summary call carries a lower reasoning effort (`medium`) where the
  provider supports it (`Request.ReasoningEffort` on the wire in both
  shapes: top-level `reasoning_effort` for OpenAI-shaped servers and
  `chat_template_kwargs.reasoning_effort` for llama.cpp — measured on the
  swap, only the kwargs entry changes the think length): it is the one
  call whose thinking nobody reads. The summary prompt also says prose
  only, no tool calls, so a max-effort model can't answer the summary
  with a call. **The summary request is two messages**: a short system
  role, and one user message carrying the older prefix as a quoted
  `<transcript>` block (role-prefixed lines, tool calls and results
  included) followed by the prompt's instruction — the prefix is data,
  not a live conversation, so the model summarizes it instead of
  continuing it (a last "reply with only X" stays inside the block).

- **runtime hardening** (roadmap deliverable 7, `specs/SPEC_HARDENING.md`):
  the seams and events everything after this needed, in one named loop
  change (L1–L7). **Tool events**: `ToolStart{Call}` and
  `ToolResult{ID, Content, Err, Duration}` bracket each execution in the
  Event vocabulary (the bracket wraps the whole middleware chain, so the
  result carries the guarded verdict); the CLI renders `● name` and
  `name ✓|✕ duration` and the old `[call]` line is gone; the recorder's
  middleware tap is retired — it sources its rows from the loop's events,
  and the root's chain is `[perm, guard]`. **Reasoning round-trip**:
  `ReasoningDelta` streams (thinking precedes speech), `Message.Reasoning`
  accumulates in both assistant branches and rides the wire back as
  `reasoning_content` (the `messages.reasoning` column lands); the CLI
  renders it verbatim, one-shot ignores it. **Usage cache fields**:
  `CacheRead`/`CacheWrite` on `Usage`, mapped from
  `prompt_tokens_details` (absent → zero); the adapter now requests the
  stream's usage chunk (`stream_options.include_usage`, wire-shape asserted)
  — without it OpenAI and llama.cpp emit no usage at all; the CLI sums per
  turn and prints `↑P ↓C · cache R hit%` at the turn's end (pane's
  `formatTokens`). **Steering**: the turn's cancel rides the Input ctx as
  the interrupt handle (`core.WithInterrupt`/`InterruptFrom`); a line typed
  during a live turn interrupts the turn and is delivered on re-entry (one
  slot, latest wins); between turns it is served directly. Ctrl-C keeps its
  meaning: the session ends once the in-flight step unwinds. **Session
  resume**: `state.Resume(ctx, db, id)` rebuilds the session from the state
  rows in one read-only transaction (transcript in seq order, dangling calls
  kept, files rebuilt; unknown id loud); `-resume <id>` at the root, `-p`
  and `-resume` refuse at construction; the recorder upserts the files
  snapshot at each turn boundary, closing the `RecordFile` gap. **One
  extension mechanism**: `ToolMiddleware` widens from a function type to
  an interface (`ToolMiddlewareFunc` adapts; the `perm` wrap-only shape is
  unchanged), with assertion-checked `TurnObserver` and
  `GuidelineContributor` capabilities; the loop fans out `TurnStart` once
  per turn (L6) and the root collects guidelines into the system prompt
  before the policy is built. **Guard alignment**: the bound is keyed by
  tool name, cleared at every turn start (pane's retry-guard semantics),
  the limit-th failure carries pane's note verbatim (appended, never
  replacing), and the bound refusal is kept. **Turn boundaries**:
  `TurnEnd{over|fault|interrupt}` closes every turn inside the run and is
  absent on run-context end; a dead turn ctx at the pre-stream seam
  (Assemble, the Stream call) reads as an interrupt, not a fault. All 15
  existing named loop cases pass byte-for-byte; `-p` and `run-job` are
  unchanged.
- **tool/web** (roadmap deliverable 6): pane's web_search and web_fetch,
  ported. web_search: SearXNG JSON over net/http (the web-tools compose
  on loopback :8888; LOOPER_SEARXNG_URL to point elsewhere), results
  mapped to title/url/snippet (tags stripped, 300-char cap),
  maxResults 1..20 default 5, the 15s budget, "no results" loud.
  web_fetch: the guarded fetch — http(s) only, DNS refuses private and
  link-local space before any connection, every redirect hop re-guarded,
  hop cap, textual content types, declared-and-streamed 5 MiB byte cap
  with the loud marker, the 20 000-char cap with pane's elision marker,
  the 30s whole-fetch timeout, egress through the compose's tinyproxy
  (:8889; LOOPER_WEB_FETCH_PROXY, set empty = direct), and the
  unreachable-proxy fix-it voice. Extraction: trafilatura as a
  documented external (shared venv, then PATH; LOOPER_TRAFILATURA
  explicit, empty = off), degrading to pane's stdlib text pass — and
  announcing it in the content, where pane is silent. Stdlib only.
  Pane's 24 named cases plus 5 looper-side cases green against httptest
  servers; the suite is green on a bare box with the trafilatura-present
  arms skipping.
- **tool/python** (roadmap deliverable 5): pane's persistent IPython
  kernel, ported. One kernel per session; state (variables, imports, defs)
  survives across calls. Stdlib only: `os/exec` and the kernel's
  JSON-lines protocol over stdio, no third-party client; the host is
  pane's `kernel_host.py` verbatim, embedded and materialised on demand
  (pane's installed path preferred for interop, the choice logged at
  startup). Interpreter is pane's shared venv; the lazy bootstrap
  (single-flight, re-tryable, verbatim voice) is the default path's
  policy — `LOOPER_PYTHON` is the operator's explicit interpreter and the
  `NewWith` seam's contract is no bootstrap. Timeout kills the whole
  kernel and says so (pane's voice, half-up rounding); an unexpected death
  is announced on the next call once, with exit description and stderr
  tail; a deliberate restart leaves no note. Protocol state is per process
  (no stale buffer, no stderr leak); first-writer-wins delivery; EPIPE
  fails fast; `Setpgid`/`WaitDelay`/group kill. Pane's 21 named cases pass
  against a real kernel in pane's order; the suite skips cleanly on a bare
  box.
- **tool/scheduler** (roadmap deliverable 4): background jobs on the
  user's crontab, ported from pane. Two stores (global, per-workspace) with
  the event-log spine and `jN` ids never reused; crontab as the scheduling
  truth (tagged lines, surgical rewrites, foreign lines byte-identical,
  written before the store commit, drift surfaced in `list`); 5-field
  vixie validation and `once` + `at`; the `runs` container as the audit
  read. The runner is looper's own `run-job <key>` verb: flock per key,
  busy policy against llama-swap (`skip` | `force`, own-slot-loaded runs,
  unreachable fails closed), spawn under a timeout with process-group kill,
  run records and logs (newest 20), a once job consumed after its fire.
  Workers are `looper -p` with the swap endpoint passed explicitly; the
  default `-base-url` now matches the swap (8090).
- **`-p` one-shot mode** (`frontend/oneshot`): one prompt in, the response
  on stdout, faults propagate to a non-zero exit so a run record cannot
  log false success. Borrowed from roadmap deliverable 7 to give the
  scheduler its worker.
- **tool/rem** (roadmap deliverable 3): memory, ported from pane. learn
  (idempotent on scope + content md5), recall (FTS5 and trigram arms fused
  by reciprocal rank at k=60, effective strength at read, project-first
  with global fill, live-hit budget), reflect (distilled memory with its
  raw source), prune (consolidate as the checkpointed pass, remove/reduce
  by selection). Ids minted from a meta counter, never reused;
  supersession SET NULL cleared by prune in the same transaction; FTS and
  trigram rows written in code, no orphans; `AutoReflect` shipped for
  compaction to wire. `memories.source` defaults to the calling session id,
  free text allowed.
- **store/state** (SPEC_STATE): the session transcript as rows. `sessions`,
  `messages`, `tool_calls`, `usage`, `files`, `faults` in a workspace-shared
  sqlite file; a recorder on the Frontend and middleware seams lands every
  completed row inside its own short transaction, so a killed worker leaves
  its autopsy. `core.Session.ID` minted at `NewSession` and shared by the
  loop and the transcript.
- **store/**: the substrate under all of the above. lift-generated domain
  and DDL from hand-written four-tag metadata (`gen.json` with the runtime
  field; lift gained it, and portable `IN`-list batch getters, in the
  process), `sqlx` and `lazy` copied verbatim, `Open` with pragmas riding
  the DSN (`_txlock=immediate`, WAL, busy_timeout, foreign_keys on every
  pooled connection), schema version check, corruption quarantined aside,
  a generation-drift test per store. `modernc.org/sqlite` is the one
  require line.

- **tool/todo** (roadmap deliverable 2): the task-queue store — pane's
  semantics, Go over the generated substrate. Claim semantics (start
  claims, foreign complete/start refuses and names the claimer, fail
  frees); completion gated by dependencies with the blocker named, cycles
  refused with the path, blocked tasks skipped by next; move via
  minted-position events; auto-compaction past the threshold, the
  snapshot first; the stale footer; workspace isolation per working
  directory. Concurrent writers serialize at the database level:
  up-front write locks and per-connection pragmas carried in the DSN. The
  generated domain/DDL runs under the drift guard; extra.sql applied via
  `Statements() = DDL + the extra`.

- **tool/fs** (roadmap deliverable 1): named `ls`, `find`, `grep` beside
  bash/read/write/edit. Stdlib only; `.git` and binary skips; unreadable
  entries counted and named (`[skipped: N unreadable]`), an unreadable root
  stays a loud error; result caps named in the output, matched-line text
  capped at 512 bytes; bare find patterns match by name, the find -name
  reading; ctx honored at the walk boundary.
- **`Description` on the wire**: `core.Tool.Description()` +
  `ToolSpec.Description`, carried by the OpenAI adapter as
  `function.description`; descriptions take the house voice. The default
  allow-list grows to the seven-tool set.

## [0.1.0] — initial release

Shipped through the stacked-review process: each slice landed on its parent,
gated on `go test ./...` / `go vet ./...` / `gofmt -l`, merged down-stack.

- **core + loop** — the typed seams (`Provider`, `Tool`, `Frontend`,
  `ContextPolicy`, `ToolMiddleware`), wire types, the streaming-event
  vocabulary, versioned session JSON with provenance and exec records, the
  composition kernel, and the concrete turn runtime: faults (provider,
  transport, *and* a failing context assembly) surface through `Notify`,
  abort the turn, and leave the session intact; cancelled steps surface
  cleanly; a stream closed without Done or Fault fails loudly.
- **provider/openai** — the SSE streaming adapter over `net/http`: finish-
  marker gated delivery, tool-call accumulation by choice index, tool schemas
  transmitted as JSON objects, non-2xx and truncation surfaced as faults.
- **tool/bash** — `bash(1)` execution with induced-work bounds: 256 KiB
  output cap (truncation named), `WaitDelay` so background children cannot
  hold the turn, and process-group teardown on cancellation.
- **tool/file** — read/write/edit with `FileState` provenance, path-
  normalized drift checking (external modification named and refused;
  ambiguity refused, never guessed at), and a 1 MiB read cap.
- **middleware** — `policy` passthrough (day-one `ContextPolicy`); `perm`
  allow-list, deny-by-default at the boundary with attributed denials;
  `guard.Bound` — every call executes exactly once; the model's re-issuance
  of an identical failing call is counted across turns (name plus args
  digest), refused without executing at the bound, and cleared on success.
- **frontend/cli** — the stdin/stdout REPL over the `Frontend` seam: plain,
  greppable rendering; blank lines no-op; Ctrl-C cancels the turn at its next
  boundary and the session survives.
- **cmd/looper** — the composition root: `looper.New(...)` wires every seam
  in one call; configuration via flags or `LOOPER_*` env, no config files;
  `--version` reports the release.
