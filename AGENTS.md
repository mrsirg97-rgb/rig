# RIG

## overview

rig is a minimal agent-loop machine: assemble context, stream the model's
output, execute what it asks for, feed the results back, repeat. It is
not a framework; it is one machine, built closed. The core (`core/`,
`loop/`) is stdlib-only, every dependency is held at a typed seam, every
induced work is bounded, and a single composition root (`cmd/rig`) wires
the whole tree once. The design test is structural: adding a tool, a
provider, a policy, a frontend, a middleware, or a command is one file
plus one registration line, and the loop never names a concrete type.

## working in this repository

- Every Go package carries a `PACKAGE.md` that is the spec file for that
  package: what it is, what it includes, how it is consumed, the gotchas.
  Read it before touching the package and keep it current when behavior
  changes. The governing specs live in `specs/`, written and agreed
  before the code; the `PACKAGE.md` points at its spec.
- No comments in implementation code; comments in tests are allowed.
  When reaching for a comment, first ask whether it belongs in the
  `PACKAGE.md` instead: design rationale lives in the spec, not inline.
  Only load-bearing notes stay in code.
- Follow Go best practices and design interface first when applicable.
  Interfaces make rig modular, which is the goal: depend on the seams in
  `core`, wire once at the root, swap at registration with zero consumer
  changes.
- Keep things lean and terse. Follow the established patterns, and apply
  a new pattern only if it is genuinely better.
- Be security conscious at all times. This is a harness agents work in,
  with filesystem and shell access, running untrusted model output:
  deny by default, canonicalize untrusted input before acting on it,
  bound the work a caller can induce, and fail closed on uncertain
  state.
- Build for the daily driver. This harness is what you will be using:
  anything added here should be a feature you will want to reach for,
  and it should remove friction, not add ceremony.

## packages

- `core` — the kernel's contract surface: the seams (Provider,
  ContextPolicy, Tool, ToolMiddleware, Frontend, Command), the wire
  types, the streaming-event vocabulary, and the session type. Types
  only, no behavior.
- `loop` — the turn runtime: the one place turn ordering is written
  down; fault- and cancel-aware.
- `kernel.go` (root package `rig`) — the composition kernel: the
  dependency bag the loop drives, assembled from options.
- `cmd/rig` — the binary and composition root: flag/env/file config
  resolution, the store wiring, plugin discovery. The only package that
  imports the whole tree.
- `command` — the user-command leaf: the slash-command set (`compact`,
  `new`, `models`, `sessions`, `steer`, `todo`, `scheduler`, `plugins`),
  testable with fakes: no kernel, no stores, no provider.
- `config` — the config-loading layer: four-layer resolution (flags >
  env > file > embedded defaults) and the models table out of code and
  into a file.
- `models` — the per-model table: window, compaction numbers, role,
  effort; env overlay and loud row invariants.
- `policy` — the ContextPolicy implementations: the passthrough and
  compact (trigger-based transcript summarization, the once-budget
  overflow recovery, auto-reflect into the memory store).
- `middleware/perm` — deny-by-default tool allowlist and the plugin
  provenance rule (model writes land in `plugins/pending/`).
- `middleware/guard` — the retry guard: bounds the model's repeated
  failing re-issuance of a tool per turn; the streak keys on identical
  args, a corrected call always executes.
- `middleware/toolset` — the root's live tool table: a per-turn fact,
  swapped atomically so a plugin reload or model switch takes effect on
  the next turn.
- `provider/openai` — the OpenAI-compatible streaming provider over
  net/http: plain JSON/SSE wire, per-model tool-call formats.
- `plugins` — python plugin discovery: one file under the rig home's
  `plugins/` is one tool, discovered and executed through the shared
  kernel.
- `store` — the SQLite persistence substrate: the open path, the
  pragmas, schema versioning, corrupt-file quarantine.
- `store/sqlx` — the `database/sql` seam: serializable transactions that
  ride the context; fails closed on an unbound read.
- `store/lazy` — the deferred results the generated accessors hand back.
- `store/state` — the session-state store: the observing recorder
  frontend, the `-resume` projection, the sessions listing.
- `store/todo` — the task-queue store: the event log is the spine, the
  task rows a disposable projection rebuilt every transaction, DAG
  validated at create.
- `store/rem` — the memory store: recall (FTS plus trigram, rank-fused),
  consolidation arithmetic, supersession.
- `store/scheduler` — the background-jobs store: the event log, the
  crontab as scheduling truth, the worker runner with the bwrap jail
  and the socket proxy.
- `store/{rem,scheduler,state,todo}/metadata` — hand-written container
  metadata: the source for the generated `ddl`/`domain` accessors. Edit
  and regenerate; never hand-edit the generated projections.
- `tool/bash` — bash(1) execution: real subprocesses, output surfaced
  and bounded.
- `tool/file` — the read, write, and edit tools: exact-match edit with
  provenance from the threaded session, so edit-after-external-change
  fails loudly instead of clobbering.
- `tool/fs` — the named filesystem tools: `ls`, `find`, `grep`, with
  small schemas a local model can reach for.
- `tool/diff` — the observation diff: `git diff` against the working
  tree, or the previous observation of the same call, over a pure Go
  diff engine.
- `tool/python` — the persistent IPython kernel: JSON-lines over stdio,
  one kernel per session, the namespace shared with plugin discovery.
- `tool/web` — `web_search` against a local SearXNG and `web_fetch`
  with the SSRF guard and extraction.
- `tool/todo`, `tool/rem`, `tool/scheduler` — thin adapters over their
  stores: session attribution and the store's shapes, verbatim.
- `tool/delegate` — the one-shot worker tool (SPEC_DELEGATE): spawn a
  headless worker on a task now, wait, and feed back its last message;
  a recorded run in the cwd-scope scheduler store, a resumable
  transcript.
- `frontend/cli` — the stdin/stdout frontend and the piped reference:
  plain text, command dispatch, the steering seam.
- `frontend/tui` — the terminal UI: the same events and commands in a
  live-region design; adds to the CLI's bytes, never changes them.
- `specs/` — the specs, written and agreed before the code (SPEC_CORE
  first); the governing documents the `PACKAGE.md` files cite.
- `docs/` — the architecture (`DESIGN.md`), setup, usage, TUI design,
  and the consolidation notes.
