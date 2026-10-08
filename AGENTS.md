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
- No comments in Go, implementation or tests: one rule everywhere,
  because a small model reads the repository as one corpus and cannot
  hold "allowed here, forbidden there"; it sees commented tests and
  writes commented code. A test's name carries its invariant; the
  `PACKAGE.md` carries the English; design rationale lives in the spec.
  The only `//` lines are compiler directives (`//go:embed`). Exempt:
  generated code, and the `metadata` packages; generation input whose
  doc comments lift reads.
- Follow Go best practices and design interface first when applicable.
  Interfaces make rig modular, which is the goal: depend on the seams in
  `core`, wire once at the root, swap at registration with zero consumer
  changes.
- Keep things lean and terse. Follow the established patterns, and apply
  a new pattern only if it is genuinely better.
- The model's words are in `tool/registry.json`: every native tool's
  description (`what`, `guidelines`, `reply`) and schema live there and
  nowhere else; a tool embeds its `tool.Definition` and writes only
  `Exec`. A words pass is a diff on that file.
- Be security conscious at all times. This is a harness agents work in,
  with filesystem and shell access, running untrusted model output:
  deny by default, canonicalize untrusted input before acting on it,
  bound the work a caller can induce, and fail closed on uncertain
  state.
- Build for the daily driver. This harness is what you will be using:
  anything added here should be a feature you will want to reach for,
  and it should remove friction, not add ceremony.

## how the code looks

The reference is `~/Projects/lift/engine` (read `core/path.go`,
`core/scribe.go`, and `visit` in `core/agent.go`) and, in this tree,
`core/`, `evt/`, `loop/` and `tool/registry.go`. Pack them with `rem`
before opening a new package. The aim is to express a lot in a little;
that is harder than writing a lot, and it is the bar.

- **Interface first.** Every type reads as its interface. The struct is
  unexported, the constructor returns it, every signature takes the
  interface, and nothing outside the package names the struct. Nothing
  calls a concrete method, so the concrete can change under everyone.

  ```go
  type Definition interface {
  	Name() string
  	Enabled() bool
  	Description() string
  	Schema() json.RawMessage
  }

  type entry struct {
  	N string          `json:"name"`
  	E bool            `json:"enabled"`
  	W string          `json:"what"`
  	G string          `json:"guidelines"`
  	R string          `json:"reply"`
  	S json.RawMessage `json:"schema"`
  }

  func Def(name string) Definition
  ```

  Not this: `type Definition struct { name, what string; ... }` with
  methods. It worked until the first caller needed it comparable and
  the second shadowed a method on it.

- **One-method seams.** A seam is an interface with one method and a
  name that says what crosses it: `Scribe.Record(symbol)`,
  `ToolMiddleware.Wrap(next)`, `Extractor.Extract(ctx, file)`,
  `Decider.Decide(ctx, state, questions)`. A new behavior is a new
  implementer registered at the root, never a new branch in the caller.
  The loop never names a concrete type; that is the structural test.

- **Delegation by embedding.** Where a type is the other thing plus
  one difference, embed the interface and implement the difference.

  ```go
  type filled struct {
  	Definition
  	slot  string
  	value string
  }

  func Fill(d Definition, slot, value string) Definition {
  	return &filled{d, slot, value}
  }

  func (f *filled) Description() string {
  	return strings.ReplaceAll(f.Definition.Description(), f.slot, f.value)
  }
  ```

  The plugin door embeds `tool.Definition` and implements `Schema()`
  because that is its contract; lift's context scribe embeds
  `core.Agent` and simply has `Registry()`.

- **A thing is whole when it is constructed.** Its seams are
  constructor arguments. No `SetX` after `NewX`, no package-level
  variable a caller installs, no `init` that wires behavior. If a value
  is optional, take it as a variadic so the common call stays short:
  `NewContainer(name, props, table ...string)`,
  `Discover(scribes ...Scribe)`, `NewSymbol(source, parent, cardinality ...int)`.

  Not this: `file.SetIndexer(q)` (a global the tools read),
  `q.SetScorer(s)`, `q.SetPackCaps(a, b)`, `t.SetParallel(n)` (seams
  bolted on after the constructor). Each of those is a constructor
  argument that was not given one.

- **State you carry, not state you store.** lift's `Path` is one
  `[]string`: `Push`, `Pop`, `Seen`, `Current`, and `Symbol()` minting
  the node from the top. Cycle detection, identity and position fall
  out of it. Reach for data on the walk before a flag on the struct,
  and for a visited slice before a visited map with a mutex.

- **Sorted output everywhere.** Every slice a method returns leaves
  sorted (`Registry.Containers()`, `Dimension.Symbols()`, the todo
  queue, the model table). It costs five lines a type and buys
  determinism, which is what makes the wire diff and the drift tests
  possible.

- **One shape per concept.** One list shape for every slash command,
  one `Definition` for every tool, one fan-out for every decision
  question. When a second type appears for the same concept, collapse
  it; three wrappers around one call is the signature of agent code.

- **Bounds are economics, never numbers.** A cap derives from a row the
  operator already owns (the window, the reserve, the max output
  tokens, the result cap) or it is a setting in `settings.json` with a
  default. A bare constant needs a sentence naming what it buys. No
  clocks: when a poll, a timeout or a threshold suggests itself, ask
  which event already carries the fact (the process exit, the
  connection drop, the turn end, the sha that moved).

- **Say a lot in a little.** The refactor pass every few versions
  collapses what grew. A package is not done while it has a setter, a
  global, a duplicated query string, or a second type for one idea.
  Fewer lines that read as the design beat more lines that work.

## working on a change

- `make test`: `go vet ./...` then `go test -race ./...`.
  `make fmt-check`: `gofmt -l .` must be empty; CI runs both.
- The freeze gate (`go run ./cmd/freeze`) reads `specs/FREEZE.txt`:
  every path a PR touches must match a line, a path matching none
  fails naming the path and the file; `core/` and `loop/` stay open
  to pure addition only, and a reopening is a one-line diff to that
  file, reviewed in the PR.
- The wire job (`scripts/wire-check`) renders the request bodies at
  the merge-base and at the head and posts their diff as the job's
  summary; the stored goldens and the `-update` flag are gone (2.12.3).
- `CONTRIBUTING.md` is the process: spec first, tests before code.

## packages

- `core`: the kernel's contract surface: the seams (Provider,
  ContextPolicy, Tool, ToolMiddleware, Frontend, Command), the wire
  types, the streaming-event vocabulary, and the session type. Types
  only, no behavior.
- `loop`: the turn runtime: the one place turn ordering is written
  down; fault- and cancel-aware.
- `evt`: the event loop (SPEC_EVT): libevt's shape, Go-centric; one
  consumer, many producers, closures ordered by priority then arrival.
  The turn loop is its consumer (phase 2, 0.12.0): every step of a turn
  is a closure on the loop goroutine.
- `kernel.go` (root package `rig`): the composition kernel: the
  dependency bag the loop drives, assembled from options.
- `cmd/rig`: the binary and composition root: flag/env/file config
  resolution, the store wiring (the rem migration), plugin discovery.
  The only package that imports the whole tree.
- `cmd/freeze`: the freeze gate program the CI freeze job runs: every
  path a diff touches must match a line of `specs/FREEZE.txt` (a `/`
  line is a directory prefix; `reopen <path> <version>` reopens a
  frozen path by name); a touched path matching no line fails, naming
  the path and the file.
- `command`: the user-command leaf: the slash-command set (`compact`,
  `new`, `project`, `sessions`, `models`, `steer`, `todo`, `scheduler`,
  `plugins`, `decision`, `rem`, `effort`, `role`, `approve`, `swarm`,
  `theme`), testable with fakes: no kernel, no stores, no provider;
  `swarm` owns the vocabulary, the controller owns the goroutines
  (SPEC_SWARM).
- `config`: the config-loading layer: four-layer resolution (flags >
  env > file > embedded defaults) and the models table out of code and
  into a file.
- `models`: the per-model table: window, compaction numbers, role,
  effort; env overlay and loud row invariants.
- `policy`: the ContextPolicy implementations: the passthrough and
  compact (trigger-based transcript summarization, the once-budget
  overflow recovery). Beside them the provider decorators: effort (the
  reasoning dial) and empty (the empty-turn guard, SPEC_EMPTY: a `stop`
  turn with no content and no tool calls is resampled twice with the
  identical request, then faults). Compaction writes nothing to rem: the
  summary is context, not memory (SPEC_STATE: rem is deliberate).
- `pathguard`: the one cwd-containment rule: canonicalize a working
  directory and refuse one outside the session's cwd or the rig home;
  the delegate and scheduler tools and the runner's fire-time
  revalidation all call it.
- `decision`: the decision seam (SPEC_DECISION): the typed question
  (choice, score, yes/no), the answer with its confidence and its
  decider, the `Decider` a decision server implements, the `Recorder`
  the gates hold; beside them the HTTP proposer, the bounded proposal
  queue, the bash site, the pack scorer the map's task pack scores its
  candidates through, the reviewer, and the decide tool the model
  hands its sorting to (a built-in entry of the live table when
  `decisionUrl` is set). An answer is a proposal an LLM reviews, never
  an action.
- `middleware/approve`: the manual tool-approval gate (SPEC_MODES 4):
  in manual mode a mutating call pauses for the operator's y/n at the
  frontend's ask door; a denial is a teaching refusal the model reads.
- `middleware/cutoff`: the refuse-before-execute link for a call the
  provider marked as cut off (`ToolCall.Cut`, SPEC_HARDENING 10): the
  partial args never run, and the refusal feeds back so the model
  re-issues on the next turn instead of a dead fault.
- `middleware/perm`: deny-by-default tool allowlist and the plugin
  provenance rule (model writes land in `plugins/pending/`).
- `middleware/guard`: the retry guard: bounds the model's repeated
  failing re-issuance of a tool per turn; the streak keys on identical
  args, a corrected call always executes. Beside it the two bounds
  (SPEC_HARDENING 9): `Rounds`, the per-turn cap on tool calls, and
  `Cap`, the wall that bounds every tool result.
- `middleware/paths`: the `~`-expansion boundary: one chain link that
  expands a leading `~` in the path-shaped arguments before any tool
  sees them, so every tool inherits it.
- `middleware/index`: the graph tap (SPEC_GRAPH): one chain link that
  touches the code map's indexer with the path of a successful read,
  write or edit; a nil indexer taps nothing.
- `middleware/toolset`: the root's live tool table: a per-turn fact,
  swapped atomically so a plugin reload or model switch takes effect on
  the next turn.
- `provider/openai`: the OpenAI-compatible streaming provider over
  net/http: plain JSON/SSE wire, per-model tool-call formats, the
  bounded header wait and the stream's idle bound (SPEC_HARDENING 11).
- `plugins`: python plugin discovery: one file under the rig home's
  `plugins/` is one tool, discovered and executed through the shared
  kernel.
- `testenv`: the suite's isolation from the operator's machine: `Main`
  points `HOME`, `XDG_CONFIG_HOME` and `RIG_HOME` at one throwaway
  directory per package run and puts a `crontab` shim first in `PATH`
  that refuses loudly; `OperatorHome` keeps the real home for
  read-only fixture probes.
- `store`: the SQLite persistence substrate: the open path, the
  pragmas, schema versioning, corrupt-file quarantine.
- `store/scope`: the project identity: the repo (the short sha1 of the
  git common dir, worktrees share) with a cwd-hash fallback; the
  partition key of the todo and rem stores.
- `store/fts`: the one FTS tokenization contract — the tokenize, the
  padded trigrams, the OR-query with the reserved operators quoted;
  the stdlib leaf `store/rem` and `store/graph`'s fuzzy arms share.
- `store/sqlx`: the `database/sql` seam: serializable transactions that
  ride the context; fails closed on an unbound read.
- `store/lazy`: the deferred results the generated accessors hand back.
- `store/state`: the session-state store: the observing recorder
  frontend, the `-resume` projection, the sessions listing.
- `store/todo`: the task-queue store: the event log is the spine, the
  task rows a disposable projection rebuilt every transaction, DAG
  validated at create; one file for every project, rows carrying the
  project scope. The swarm surface (claim/note/review/accept/reject)
  rides the same log; the gate keys on who completes (solo lands done
  with the pair, a worker submits for review); and the 2→3 migration
  pairs historical completes with accepts so a pre-review log replays
  exactly.
- `store/graph`: the code map (SPEC_GRAPH): one sqlite file per project
  under the rig home, scoped like todo, generated through lift; symbols,
  edges, files — addresses, never source text; the Extract seam with the
  Go extractor in-process and every other language behind a
  language-server client; the 2.7.0 queue shape maps a file after every
  read, write or edit, and rem's `index` and `pack` walk the map.
- `store/rem`: the memory store: recall (FTS plus trigram, rank-fused),
  consolidation arithmetic, supersession; scope is a repo identity
  (worktrees share), a one-time migration on the schema bump, and every
  operation is deliberate.
- `store/decision`: the decision store (SPEC_DECISION): one sqlite file
  under the rig home, scoped like todo; a row per decision with site,
  state, question, answer, confidence, decider, session, status (final,
  pending, approved, denied), reviewer, the reviewer's answer, and the
  outcome once known. The gates record final rows; the reviewer settles
  pending rows. Recording never changes a decision and a store error
  never fails a call.
- `store/scheduler`: the background-jobs store: the event log, the
  crontab as scheduling truth, the worker runner with the bwrap jail,
  the socket proxy, the per-job stall watch, and the live run tail.
- `store/{rem,scheduler,state,todo}/metadata`: hand-written container
  metadata: the source for the generated `ddl`/`domain` accessors. Edit
  and regenerate; never hand-edit the generated projections.
- `broadcast`: the fleet's message seams (2.11.0), lifted from the
  operator's module onto the event loop: a `Room` of `Member`s over a
  `Transport`, a `Message` with origin, health and a `core.Event`, none
  being a heartbeat; a send is a post at the room's priority and the
  queue is the durability; `Say` is the one voice a background
  subsystem notices with, and the member ids are named in the kernel.
- `tool`: the registry of the model's words: `registry.json`, embedded,
  one entry per native tool (`name`, `enabled`, `what`, `guidelines`,
  `reply`, `schema`); `Definition` is the interface a tool embeds for
  `Name()`, `Description()` and `Schema()`, the registry's entry its one
  concrete; `Fill` wraps the one tool whose schema names the default
  model (`scheduler`); `Names()` is the root's native list, in file
  order, enabled only.
- `tool/bash`: bash(1) execution: real subprocesses, output surfaced
  and bounded.
- `tool/execwrap`: the landlock subprocess seam: prepends the
  `RIG_EXEC_WRAPPER` helper (`rig -exec <argv>`) to a tool's argv when
  the env names one, so the landlock domain rides the tool's subprocess.
- `tool/file`: the read, write, and edit tools: read is the observation
  path (drift-checked), not cat or sed, for any file you may edit;
  exact-match edit with provenance from the threaded session, so
  edit-after-external-change fails loudly instead of clobbering.
- `tool/view`: the image tool (SPEC_VIEW): a path in, one marker line
  out, the bytes content-addressed under the rig home; registered only
  for a model row whose `vision` is true.
- `imagemarker`: the one image-marker contract: the line a `view`
  result is, and the blob path rule. One stdlib-only leaf beside
  `pathguard`, because `tool/view`, `provider/openai`, and
  `frontend/tui` disagreeing on those bytes breaks the prompt cache.
- `tool/diff`: the diff engine, no tool surface: the pure Go `Diff`
  (edit's drift refusal) and `Files` (the `git diff` read's `diff: true`
  appends); read and edit use the package.
- `tool/python`: the persistent IPython kernel: JSON-lines over stdio,
  one kernel per session, the namespace shared with plugin discovery.
- `tool/web`: the one `web` tool — search against a local SearXNG, and
  fetch with the SSRF guard and extraction.
- `tool/todo`, `tool/rem`, `tool/scheduler`: thin adapters over their
  stores: session attribution and the store's shapes, verbatim. The rem
  tool's description carries the contract sentence (rem is deliberate).
  Every native tool is its interface (2.12.4 set the shape in
  `tool/todo`, 2.12.6 gave it to every native): the package declares a
  named interface — `Bash`, `Read`, `Write`, `Edit`, `View`, `Verdict`,
  `Delegate`, `Web`, `Sessions`, `Python`, `Rem`, `Scheduler`, `Plugin`,
  `Decide` — embedding `tool.Definition`, declaring `Exec` as the one
  JSON door, and one method per verb taking that verb's fields; the
  implementing struct is unexported and the constructor returns the
  interface. `Exec` decodes and routes and nothing else: required
  fields, mode gates and bounds live in the method, so a Go call and a
  JSON call meet the same check and one reply is one format. A wire
  shape Go cannot name (a todo `link`, an id or a list of ids; a
  scheduler `model`, absent, null or a name) is decoded at the door and
  reaches the method decoded, or as a pointer where absent and zero
  differ. A verb's target stays positional — the workspace, the path,
  the job id — with at most three of its own fields beside it, and the
  fields go in one input struct named for the verb (`CreateInput`,
  `LearnInput`) as soon as they are a bag of options rather than an
  order: a door whose ten arguments are six strings in a row is the wire
  again with type names on it.
- `tool/verdict`: the reviewer's one word (2.11.0): registered only in a
  worker that holds a fleet pipe, its call crosses as `core.Verdict`
  published as the worker's member; the swarm reviewer and the decision
  bite read it from the room, and nothing scrapes stdout for it.
- `tool/delegate`: the one-shot worker tool (SPEC_DELEGATE): spawn a
  headless worker on a task now, wait, and feed back its last message;
  a recorded run in the cwd-scope scheduler store, a resumable
  transcript; the optional `Notify` seam (SPEC_SWARM 7) emits the
  status snapshot for an interactive delegate.
- `swarm`: the drain-worker controller (SPEC_SWARM): the router and the
  settle as closures on the loop at the fleet's priority, the worker
  goroutines waiting on the world, the reviewer's verdict as a message; the
  supervisor is a member of the session's `broadcast` room and says
  everything there as `Notice` with source `swarm` and `SwarmStatus`
  snapshots; the dead claim is released via the todo store's Reap door.
- `tool/sessions`: the session-store introspection tool: `list` and
  `summary`, the vitals (which models ran, what failed, the cache
  ratio), and the store's schema migration on open.
- `frontend/cli`: the stdin/stdout frontend and the piped reference:
  plain text, command dispatch, the steering seam.
- `frontend/oneshot`: the one-shot (`-p`) worker frontend: the single
  prompt runs once, a faulted turn ends non-zero (the scheduler's
  worker path).
- `frontend/tui`: the terminal UI: the same events and commands in a
  live-region design; adds to the CLI's bytes, never changes them; the
  swarm band and the one-line transcript notices (SPEC_SWARM 7).
- `frontend/web`: the `rig serve` dashboard (SPEC_SERVE): one of the
  four frontends of the loop (tui, cli, web, oneshot, as
  `specs/FREEZE.txt` names them) over loopback-only net/http (the live
  session as a server-sent stream, in the TUI's grammar), token-gated,
  beside the reads of the rig home's stores and the todo, scheduler,
  and plugin-forge writes; installable as a home-screen app.
- `scripts/`: the CI job scripts: `scripts/wire-check`, the wire job,
  renders the request bodies at the merge-base and at the head and
  posts their unified diff as the job's summary (the stored goldens
  and the `-update` flag are gone, 2.12.3); the menu's aim and wall
  char budgets ride its environment.
- `specs/`: the specs, written and agreed before the code (SPEC_CORE
  first); the governing documents the `PACKAGE.md` files cite.
- `docs/`: the architecture (`DESIGN.md`), setup, usage, the plugins
  guide (`PLUGINS.md`), TUI design, and the embed guide; `docs/history/`
  keeps the dated notes (the launch roadmap, the consolidation read)
  as written — never cite them as current.
