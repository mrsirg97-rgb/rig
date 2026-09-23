# Changelog

## [1.4.3]: the suite is walled off from the operator's machine

One fixture run once wrote a `local` session into the real `~/.rig`
(`sessions/cb051fd0065f.sqlite`, cwd `cmd/rig`), and a test could still
dial the embedded default swap (`127.0.0.1:8090`) — or detect the
operator's live server by binding that port and skipping when it was
busy. The suite now runs walled: every package that opens config or
stores isolates `HOME`/`RIG_HOME` to a throwaway directory, and the
scheduler's dial seam rides a test-only transport that refuses any host
that is not an httptest server before it dials.

- **TestMain isolation** (`testenv`, every package that opens config or
  stores): `HOME`, `XDG_CONFIG_HOME`, and `RIG_HOME` point at one
  throwaway directory for the whole package run, so a fixture that
  forgets its own scratch cannot write into the operator's `~/.rig`.
  The Go toolchain keeps the operator's caches (`GOPATH`/`GOMODCACHE`/
  `GOCACHE`), so the fixtures' `go build` calls stay warm and offline.
  The operator-home fixture probes (the lift checkout, the kernel venv,
  the `.bashrc` landlock probe) read `testenv.OperatorHome`, captured at
  package init, and never write.
- **The refusing dial transport** (`testenv`, `store/scheduler`): the
  `Transport` seam carries `testenv.Transport` in the suite; nil stays
  the production default (`http.DefaultTransport`). `RealFetch` and the
  socket proxy ride it, and any host that is not an httptest server
  created by `testenv.Server` is refused before the dial, so a test
  reaching for the embedded default swap fails loud instead of touching
  the operator's live server.
- **The embedded-default test no longer detects the live server**
  (`cmd/rig`): the swap chain's "neither takes the embedded" case used
  to bind `127.0.0.1:8090` and skip when the port was busy — detecting
  the operator's server — then fired the real worker at it. It now runs
  `RunJob` in-process with a recording fetch seam and a recording spawn,
  asserting the busy check and the worker argv carry the embedded
  default, with nothing bound and nothing dialed.
- **Tests**: the isolation invariant per package (the suite never sees
  the operator home), the refusal (a non-server host refused, an
  httptest host dialed), and `RealFetch` riding the seam.

## [1.4.2]: the spawned worker stops touching the board, and the plugin door pauses

The manual gate and the swarm's worker door both had a gap the model could
walk through: the `plugin` tool ran any live plugin ungated, and a spawned
`rig -p` worker could claim a board entry and complete it — or accept or
reject a review — despite the brief. The gate now pauses the door, and
Worker mode is read/note-only.

- **The plugin door pauses** (`cmd/rig`): `plugin` joins the mutating set,
  so a `plugin run` asks the operator in manual mode exactly like calling
  the plugin by name does. Both entries are pinned in the mutating
  predicate, and a chain test pins that the ask fires.
- **Worker mode is read/note-only** (`tool/todo`): claim, start, complete,
  fail, accept, and reject refuse with one message naming the supervisor —
  findings go in the task's note and in rem. The store's own arms stay as
  they are: the swarm controller and the delegate parent use the store
  directly and are unaffected.
- **Tests**: each refused verb, the door's gate, and the board never
  moving; the golden fixtures regenerated for the new todo description.

## [1.4.1]: the delegate worker dies on the silence, not the clock

The scheduler already kills a fire that writes nothing (1.3.8), but the
delegate path kept one wall-clock bound for both jobs: a swarm worker
still producing output — a long suite, a deep report — was killed at
the 30-minute ceiling while it worked. The liveness bound and the spend
bound are now separate here too.

- **`Stall` on `DelegateInput`** (`store/scheduler`): the silence
  window, 0 = today (no stall kill, the plain timeout). The delegate
  wires it to the same `stallWatch` the runner uses, touched by the
  `Observe` stream: a worker that writes nothing for longer than the
  window is killed as hung, its stderr naming
  `[runner: killed after stall]` and the result marked `Stalled`. The
  timeout stays the spend ceiling; a producing worker is never killed
  for the clock.
- **The spend ceiling is the caller's** (`store/scheduler`,
  `tool/delegate`): the seam's cap moves from the runner's 30-minute
  default to the scheduler's 24h bound, and the interactive tool keeps
  its own 30-minute `timeoutMs` cap at the tool boundary, so a model's
  induced spend is unchanged. The swarm sets `Stall 10m` and `Timeout
  2h`: a worker keeps its slot while it writes, and a silent one is
  gone in ten minutes.
- **`stallMs` on the tool** (`tool/delegate`): the optional silence
  window beside `timeoutMs`; unset is today's behavior. A stalled
  worker errors naming the stall (`delegate: the worker stalled after
  … (process tree killed)`), the trailer unchanged.

## [1.4.0]: the swarm: a fleet of drain workers, supervisor-side

`/swarm` turns the session's queue into a shared work board a fleet
drains. The supervisor is the session's own process: each drain worker
claims a task and spawns a one-shot `rig -p` through the delegate path
(the jail, the socket proxy, the recorded run), then finishes the task
itself — workers submit for review, reviewers parse the worker's
`verdict:` line and accept or reject. The parallelism is the GPU slots,
not the worker count.

- **`/swarm`** (thirteenth command): bare lists the supervisor's
  workers (`w1 worker qwen3.8-workers · task t3 · heartbeat 2s ago ·
  done 1 failed 0`); `swarm <n> [role=worker|reviewer] [model=<id>]`
  starts n drain workers on the session's bound queue — against a
  running swarm a start adds workers, so a worker swarm gains a
  reviewer mid-drain; `swarm stop` cancels the swarm, releases the
  in-flight claims, clears the rows.
- **The drain loop is supervisor-side Go**: `todo claim` → brief (task
  text and notes, with their sessions) → delegate spawn → `complete`
  (worker mode, submits for review) or the reviewer's verdict protocol
  (`accept` / `reject <reason>`, the reason riding the reject note).
  Three consecutive empty claims end a worker; an empty claim while
  another worker is mid-task does not count.
- **The dead claim**: a worker that dies mid-task has its claim released
  through the todo store's Reap door and is retried once; a second death
  fails the task (workers) or rejects it with the reason (reviewers).
  No task is ever left held by a dead identity.
- **The delegate path, three amendments** (defaulted to today's
  behavior): `WaitBusy` — a swarm spawn waits at llama-swap for a GPU
  slot instead of refusing; `Observe` — the worker's stderr streams
  into `<scheduler home>/swarm/wN.stream`, the run stream the
  supervisor's heartbeat reads; `SpawnCtx` — the base context the spawn
  timeout wraps, so `swarm stop` kills the in-flight worker.
- **The one todo read**: `todo.Task(ctx, db, p, id, session)` returns
  the task's text and notes for the brief — the rendered queue is the
  model's surface, not a parser contract. The swarm's review release
  also fixed a store replay bug: the `release` event now folds a
  review claim (the holder clears, the status stays), with a replay
  test.
- **`workers.json`** gains an optional `reviewer` key (same row
  contract as `model`): the swarm reviewer's default model.

## [1.3.9]: the todo store becomes the swarm's shared board

The board is now the place sessions talk about shared work. Three
changes, all events on the existing per-scope log: a claim verb that
takes the next task atomically, notes that any session may attach to any
task, and a review gate between active and done.

- **`claim`**: takes the first pending task whose dependsOn is done (the
  order `next` shows) and marks it active for this session; the reply is
  the task's echo, or `nothing to do`. With `status=review` a reviewer
  takes the first task in review that no reviewer holds. The take is
  atomic: two sessions claiming one task, exactly one wins (the
  serializable transaction plus the FSM check against the freshly
  rebuilt projection).
- **`note <id> "…"`**: appends a note to any task — no hold needed,
  notes are how agents talk about shared work — and `read` renders them
  in order with their session, one indented line each. A note must name
  a task and is bounded (`MaxNoteLen`, 1000 chars) because it rides the
  log and the compact snapshot.
- **The review gate keys on who completes**: an interactive session
  completing its own task lands it done in one call (the complete/accept
  pair is still written, so the log is uniform and replay is unchanged);
  a worker (`rig -p`: delegate or swarm) submitting it lands it in
  review (`[r]`), and `accept <id>` / `reject <id> "…"` decide. Accept
  and reject auto-claim an unowned review task — the same idiom as
  complete auto-starting a pending one — so a delegate's parent reviews
  its workers by read then accept/reject, with no claim step; a foreign
  holder still refuses. `blockedBy` still clears only on done, so a
  dependency in review keeps its dependents blocked; prune still drops
  done only; the summary counts review rows (`· N in review`).
- **Replay and migration**: `claim`/`note`/`accept`/`reject` are events
  on the log; notes ride the compact snapshot; a stale review claim
  releases back to unclaimed review, keeping the status. Schema 3
  (`ReviewMigration`) pairs every historical `complete` with an `accept`
  in event order, so a pre-review log replays exactly — the live board's
  109 finished tasks stay done instead of flipping to review.
- **The `todo` tool and `/todo`**: the new verbs ride the store's shapes
  (`todo claim [review]`, `todo note <id> <text…>`, `todo accept <id>`,
  `todo reject <id> <reason…>`), and `done` now submits for review.

## [1.3.8]: the scheduler kills a stalled worker, not a busy one

The daily optimizer kept getting murdered at an arbitrary minute: it
finished reports in 4-20 minutes and then, when the run drifted into
implementation work, it always outlived its budget. The wall clock is
the right bound for spend but the wrong bound for liveness, so the
scheduler now tells them apart.

- **Per-job stall kill**: `stall` (nullable minutes, same 1..1440 range
  and `-1` reset as `timeout`) is the silence window on one fire. A
  worker that writes nothing for longer than the window is killed as
  hung — the log names it (`[runner: killed after stall]`), a fail run
  like the timeout's note. `timeout` keeps its meaning: the hard
  wall-clock ceiling, the spend bound. NULL stall is "ceiling only", so
  a command job that is silent by nature never surprises.
- **Output is the liveness signal**: every byte the worker writes
  touches the window, so a long silent backtest with no stdout is never
  confused with a hung provider. The `Spawn` seam carries the observer;
  a delegate passes none (interactive sessions keep the plain timeout).
  And the one-shot worker now lives on the contract: stdout is the
  answer only, stderr carries the liveness — reasoning deltas as they
  stream, one line at tool start and end, and a heartbeat while any tool
  runs (30s cadence) — so a worker deep in a silent tool is never killed
  for not printing.
- **Live run tail**: a scheduled fire streams its output to
  `runs/<id>/<run>.stream` while it runs — `tail -f` a long job — and
  the canonical log is written whole at the end; the stream is removed,
  and a run the runner never got to finish leaves it behind. Log names
  now key on the run's start time, so the stream and the log share one
  base name.
- **Schema 5**: `jobs.stall` rides the same presence-keyed idempotent
  migration as `timeout` and `command`, and the field survives
  compaction like they do.
- **The hedge fund's j9** (the run that found this) now sets
  `stall=30` beside its `timeout=120`: silent for half an hour = hung,
  but a working session gets the full two hours.

## [1.3.7]: the docs and the landing page catch up

The 1.3.x surface landed in the code and the specs, but the README, the
setup and usage docs, and the landing page never learned the new tool
count: `view` joined the default allow list at 1.3.0 while the docs still
said 16 built-in tools, and the landing page had no image tool at all.
The package index also missed the leaves the 1.3.x surface added, and
the landing page's numbers were the 1.2.9-era snapshot. The docs now say
what the code does.

- **README**: the tool count names `view`'s vision gate beside the fleet
  condition, and `view` joins the layout's tool list.
- **docs/USAGE.md**: the default allow count is 17 tools, 19 with a fleet.
- **docs/SETUP.md**: the version examples move to 1.3.7; the knob table
  and the allow-list section name the 17 non-worker tools and the
  19-tool fleet default.
- **AGENTS.md**: the package index gains `tool/view`, `tool/sessions`,
  `tool/execwrap`, and `imagemarker` (the one image-marker contract).
- **docs/DESIGN.md**: `tool/view` joins the leaf diagram, and the
  middleware chain names `cutoff` and the `~` expansion at the outer
  edge.
- **site/index.html**: the landing page gains the `view` tool row and the
  seventeen/nineteen count, and its numbers move: the loop is 396 lines
  (loop.go + batch.go), 31k/47k lines of code/tests, 602 commits, and
  1.36B prompt tokens across 120 sessions at 99% served from cache (the
  session store's current totals, same store and metric as the 1.0.0
  receipts).

## [1.3.6]: the sessions tool migrates older project stores

The `sessions` tool opened a project's state file with the build's schema
version but no migration, so any store an older build left behind refused
with "schema version mismatch: the file carries 2, this build wants 3 and
carries no migration". The root migrates on session start; the read tool
never did, and the file stayed unreadable until a v3 session happened to
run in that workspace.

- **the sessions tool migrates on open** (`tool/sessions`): the store open
  carries `state.Migration()` — the same idempotent, transactional schema
  step the root runs — so a read of an older workspace upgrades the file
  first and lists the sessions instead of refusing. The tool stays out of
  `mutatingNatives`: the only write a read can do is the store's own
  bounded, versioned migration, and no session rows are touched.

## [1.3.5]: the suite never dials the real swap

One test could still reach the operator's swap. `TestDefaultJobModelMintsTheFleetAtStart`
ran `rig -p hello` with no `-base-url` and no `RIG_BASE_URL`, so the
embedded default (`config/settings.json`: `http://127.0.0.1:8090/v1`) was
the endpoint; a guard (a canary base URL children inherit, plus a dialer
that fails on 127.0.0.1:8090) caught exactly one `POST /v1/chat/completions`
for the fixture model `local`. The test ignored the child's exit, so the
suite stayed green while the live swap was touched. The leak is closed.

- **the fixture run is hermetic** (`cmd/rig`): the minted-fleet test now
  dials an httptest fixture and asserts the run exits 0 with exactly one
  model call, so a return of the embedded-default path fails loudly
  instead of touching the operator's swap.
- **the vision wire no longer reads the operator home** (`cmd/rig`): the
  view-registration tests pin `rigHome` to a temp dir, so `wire` never
  resolves the real `~/.rig` — the read was safe, but it was the last
  path from a unit test into the operator home, and `rigHome` carries a
  rename of the old config home.

## [1.3.4]: an empty turn is asked again

The evidence, verified in a session store: messages.seq 13171, model
huihui3.8-flash, the assistant turn had content "", zero tool calls, 268
completion tokens, finish_reason "stop". Its reasoning ended with a fully
formed tool call (`<invoke name="edit"> ... </invoke>`) that the model wrote
inside its thinking block without closing it. llama.cpp filed it as
reasoning, so rig got an assistant turn with nothing to say and nothing to
run, and the turn ended; the operator had to type "you good?" to restart
it. Server side was clean: HTTP 200, truncated = 0, nowhere near maxTokens.

- **the empty-turn guard** (`policy/empty`, `core`): a provider decorator
  at the same seam as the overflow retry. A stream that finishes normally
  (`finish_reason "stop"`) with no content and no tool calls is discarded
  and resampled with the identical request, at most twice, then a fault
  with plain words: `model returned an empty turn 3 times (no content, no
  tool call)`, plus `last reasoning ended with what looks like a tool call
  written inside its thinking` only when the last reasoning carries a
  tool-call marker (`<invoke name=`, `<tool_call>`, `<function=`). A
  `length` cut, a fault, and a content turn pass through untouched; the
  loop is byte-identical.
- **thinking still streams live** (`policy/empty`): the deltas of the
  discarded attempt are shown as they arrive; the `EmptyTurn` notice marks
  the discard, and the recorder drops its partial on it, so the store
  never persists the discarded reasoning. The one residue, named in the
  spec: the loop appends one message per stream, so the in-memory session
  message carries the shown reasoning beside the kept turn's; the store
  and the resample's request stay clean.
- **usage still counts** (`store/state`, `core`): the discarded attempts'
  usage rides `core.EmptyTurn` and is added to the session totals (one
  row per message; two discards in one turn merge), while no empty message
  row is written.
- **cancellation is unchanged** (`policy/empty`): a ctx cancel during an
  attempt or a resample closes the stream cleanly, the loop reads its
  normal interrupt path, no fault, no extra call.

The spec is `specs/SPEC_EMPTY.md`; `loop/loop.go` was not touched.

## [1.3.3]: a queue knows whose it is

The scope law says a queue belongs to its project, and the lazy re-scope
could re-key one once a repo was discovered — but the operator's shape
defeated it: `rig` launched in `~`, working several repos by absolute path
(or none at all: a ledger, an inbox). The measured cost was 593 finished
tasks from six projects in one cwd bucket while the repo's own scope held
nothing, and a live claim that could name a session from another project.
Which queue a session works in is now said out loud, not guessed from the
paths a call happens to name.

- **the binding** (`store/todo`, `tool/todo`, `command`): `session_project`
  records a session's queue. Resolution is one order everywhere: the
  `project` a call names, else the session's binding, else the launch
  directory when it is a repo, else its bucket — where a write refuses with
  the rule and a read answers labelled. Naming a project binds by what the
  call did: a write records it once the action succeeded (`→ bound to
  <label>`), a read is a peek that leaves the session where it was, and
  `bind` — `/todo project <path>` — is the declaration itself. A failed
  write changes nothing, the binding included, so a glance at a neighbour's
  queue or a mistyped id cannot relocate a session's later bare verbs.
  `todo <path> <verb…>` is the same door in one line. Resume re-reads the
  binding, so a queue cannot move because a process started elsewhere.
- **every reply names its queue** (`store/todo`): the summary leads with
  `[rig] 3/7 done · next: t4`, and a bucket minted from a non-repo says
  `[ng (not a repo)]`. Inside a repo the name is the repo's, so a
  subdirectory or a second worktree does not rename the project, and a bare
  repository is a repo of its own rather than the cwd bucket its common dir
  would suggest (SPEC_CORE's naming rule, reaching past the empty reply).
- **`prune`** (`store/todo`, `tool/todo`, `command`): the door for the done
  rows a long-lived summary keeps counting. It is itself an event, so a
  replay drops the same rows and the history stays reconstructable; failed
  rows stay (they still ask for a retry) and an idle prune appends nothing.
- **compaction carries the minting counters** (`store/todo`): the snapshot
  now writes `maxId` and `maxPos`, which is the only place the high-water
  marks survive once the create events they were rebuilt from are deleted.
  Without them the next task took the first free id and position — harmless
  while a hole meant the queue had been cleared, and a real hazard with
  `prune`: a pruned id was handed to a new task and a session holding the
  stale id completed the wrong row.
- **the voice tells the truth about `create`** (`store/todo`): the note
  reports the merge it performs (`queue merged: 2 new, 1 already there`,
  `nothing new`, `queue cleared`) instead of the long-standing `queue
  replaced with 1 tasks`, which taught a model that a create wipes a queue
  it never wipes. Semantics untouched — SPEC_UX 1 kept them for replay
  compatibility and left this wording as the one-liner to land later.

No migration re-keys the old buckets (a hash cannot be walked back to a
path, the rule that killed the earlier re-key); `prune` is the door that
sweeps them. An operator who wants the `~` bucket says so once per session
(`/todo project ~`) and it is a project like any other, marked not a repo.

## [1.3.2]: a job may outlive the clock it never agreed to

A scheduler job's every fire ran under one global 30-minute
`DefaultRunTimeout`. Work that legitimately needs longer — a suite over
a GPU that may be busy at start, a ledger backfill, a long build — got
SIGKILLed at the wall with its work done and only its finalize
outstanding, and the run was recorded as a failure the operator had to
read a log to explain. The bound is now a job field: `timeout`, minutes
per fire, stated where the job is stated.

- **per-job timeout** (`store/scheduler`): `jobs.timeout` (nullable
  minutes, 1..1440, refused outside the range by name) rides the
  create/update events and bounds the spawn context at fire time;
  precedence is the row's own value, then `RunOpts.Timeout`, then the
  unchanged 30-minute default. Orthogonal to the kind — a command job
  carries one too. Schema 4 is the idempotent presence-keyed column add,
  the same shape as schema 3's.
- **`timeout` on the tool and the dashboard** (`tool/scheduler`,
  `frontend/web`): create and update take it, update's `-1` resets to
  the default (an absent field stays "unchanged"), and the in-place
  update form carries it — a cleared field submits the reset. The
  detail-line parser scans the payload's parts instead of fixed offsets,
  so `· timeout 60m` never lands in the model field.

The default stays the default: nothing an existing job did changes
behaviour until it states its own bound.

## [1.3.1]: the docs tell the truth about their reach

A source review of the guardrails (pathguard, the chain, the plugin
zone, `web_fetch`, the loop) found the code sound and two doc claims
wider than the machinery behind them. This release states what the
boundaries actually are, welds one invariant that was only
conventionally true, and pins the chain order where it is written.

- **the egress proxy owns the pin** (`SECURITY.md`): `web_fetch`'s
  resolve-and-pin holds for direct dials; with the proxy in use the
  address check runs per hop but DNS resolves there, so the guarantee
  belongs to the proxy. The boundary now says so instead of implying
  otherwise.
- **the file tools' reach, named** (`SECURITY.md`): `read`, `write`,
  and `edit` are unscoped by design and the path boundary is
  normalization, not containment; the doc said "the path boundary"
  where a reader could hear "file paths are gated". The plugin
  provenance rule is the only file-path rule, and `pathguard`'s scope
  (the `cwd` of `delegate` and `scheduler`) is now written down.
- **`pyLiteral` refuses raw line breaks** (`plugins`): the escaping is
  total only inside a Python single-quoted literal, and the callers
  feed it `json.Marshal` output where breaks are already escaped —
  the gap was an unwritten invariant. A raw `\n` or `\r` now panics
  the cell instead of ending the string literal early.
- **the chain order comments its own definition** (`cmd/rig`):
  `canonicalMiddleware` carries the load-bearing invariants (expand
  before validate; cutoff before approval; deny before ask; cap every
  reply) beside the order they describe, next to the order test.

Two review findings were verified and retired unchanged: the drift
cache is bounded by its 16MB pressure cap regardless of session
churn, and `readCapped`'s probe read already reports exact-cap bodies
as untruncated, correctly.

## [1.3.0]: rig looks at images

A model with eyes had no way to use them: every image the agent met came
back as a byte count. `view` reads a picture, and the picture reaches the
model as a real image part, while the transcript keeps only a line.

- **`view`** (`tool/view`): one path argument, one line of reply. The
  source is decoded (png, jpeg, webp, the first frame of a gif), box-filtered
  to at most 1568 px on its longest side, and re-encoded — JPEG for an
  opaque lossy source, PNG for anything with alpha — then stored
  content-addressed at `<RIG_HOME>/blobs/<sha256>` and never rewritten. The
  reply is the marker line: the address, the mime, both sizes, the sent
  bytes and the source. The bytes never travel through the transcript, so
  a screenshot costs its reference, not its pixels, and compaction can
  never drop them; the same bytes always land at the same address, so
  looking twice sends the wire nothing new. Read-only: it never enters the
  file state, never runs in manual mode's way, and refuses over 20 MiB,
  over 16 megapixels, or anything that is not one of those formats — by
  magic, not by extension.
- **the image reaches the wire** (`provider/openai`): the tool message
  keeps its text (the marker), and rig reads the blob back and appends the
  base64 image part as its own user message after the whole tool batch it
  belongs to, in call order, for the vision model's shape. Only a marker
  the `view` tool actually wrote is honored, so a model quoting the format
  gets text; a blob that is missing, unreadable, or no longer matches its
  address degrades to a text note naming it, never a failed request. A
  non-vision model sends the marker as text and no image at all. Encoding
  stays deterministic: the same transcript assembles to identical bytes.
- **the gate is the model row** (`models`, `config`): a row's `vision`
  flag decides everything — `view` joins the tool table for a vision row
  and not for a text row, and switching models moves it with the row. The
  key is presence-aware (an explicit `false` turns it off on an embedded
  row); the embedded table sets it for nobody, so the wire of every current
  model is byte-for-byte what it was.
- **the row on screen** (`frontend/tui`): a `view` call shows its path,
  `2560x1440 -> 1568x882` when rig resampled, and the sent size — no
  picture is ever painted into the transcript.
- **the marker is one contract** (`imagemarker`): the line's format and the
  blob path live in one stdlib-only leaf, because tool, provider and
  frontend that disagree on those bytes break the prompt cache. The box
  filter is stdlib too; `golang.org/x/image/webp` joins the module (its
  decoder reads lossy webp; lossless refuses, named).

`specs/SPEC_VIEW.md` is the contract; `golang.org/x/image v0.45.0` is the
new dependency (only its `webp` decoder is imported). No frozen path
moved; `imagemarker/` and `tool/view/` join the freeze allowlist as pure
code. 80 new cases: 14 on the marker, 29 on the tool, 17 on the wire, 5 on
the TUI row, 7 on the gate at the root (one of them a run of the real
binary end to end), 8 on the row's `vision` key.

Review pass, same PR: the marker line could be smuggled — a filename with
a newline followed by a marker line made `Find` return the smuggled
marker, so `view` refuses a path carrying a control character (before the
stat), `Parse` refuses a `src` carrying one, and the provider honors only
a result that is exactly the marker line it wrote. The honor rule is also
scoped to the assistant message that owns the current tool batch, so a
call id reused on a later `read` can never be honored as `view`; and the
`plugin` door omits its `name` enum when no plugin is live, because
llama-server rejects `"enum": []` and a plugin-less home could not use it
at all. `-allow` stays the execution gate, not a wire filter — the model's
menu is the native table plus the door, whatever is allowed. 8 more named
cases; the wire goldens moved once, deliberately, for the door's
zero-plugin schema.

## [1.2.16]: the pending paragraph wraps incrementally, and no hidden row is measured

The TUI stuttered while a long unbroken reasoning paragraph streamed:
since 1.2.6 every frame re-wrapped the entire pending paragraph, once
per budget-loop iteration and twice once capped, and then measured
each rendered row with a width pass, so a 100k-character paragraph
burned several milliseconds of the 16 ms frame while the server's
rolling rate stayed flat. Greedy word wrap is prefix stable:
appending text can only change the last row.

- **the pending wrap cache** (`frontend/tui`): `pendWrap` caches the
  wrapped rows plus the cells that begin the last row and folds each
  delta into the last row alone; a rebuild happens only on a width
  change or an edit that is not an append. `liveRegionLocked` starts
  the pending block at the viewport height, the budget loop slices the
  cached rows to the cap instead of re-wrapping per iteration, and the
  pending block's row count is its row length, so `rowsOver` never
  measures rows that will not be painted. The rows shown stay
  byte-identical to a fresh wrap of the full paragraph at every step,
  exact-width rows and the trailing-space trim included; the space a
  soft break skips still charges its width to the row it left.
  `TestPendingWrapCacheStaysByteIdenticalToFullWrap` streams random
  word sequences in random chunk sizes and compares the cache to a
  fresh full wrap after every delta, across a mid-stream width change
  and across a commit. `BenchmarkFramePaint100kPendingParagraph`
  paints one frame over a 100k-character pending paragraph and fails
  above 1ms: the pre-fix frame cost 21.8ms, the fixed one runs near
  0.2ms.
- **the line splitter** (`frontend/tui`): `takeClosedLinesLocked`
  scans only the segs the delta appended for newlines and leaves the
  older segs untouched when none carry one, so a long pending
  paragraph no longer pays a re-split of every seg on every delta
  (2.5ms per 30 deltas over a 1563-seg paragraph before, about 1µs
  after).

## [1.2.15]: a wave's starts land with the wave, and the provider caps its error-body read

A review pass over the runtime found the loop telling the frontend a
concurrent wave ran serially: every call in a wave was dispatched at
once, but ToolStart was emitted one call at a time as the result
cursor advanced, so a TUI could never show what was actually in
flight. The same pass found the provider draining a non-2xx response
whole before truncating it to the 256-byte snippet the fault carries,
so a hostile endpoint could pin memory through a large error body.

- **wave starts** (`loop`): `batch.dispatch` returns the exclusive
  end of the wave it launched, and the loop notifies ToolStart for
  every call in the wave at dispatch time, before any result can be
  posted (results queue behind the advancing cursor on the single
  engine thread). Serial dispatch is unchanged: one start, immediately
  before one call. The TUI keys result blocks by call ID through a
  start-time map, so a wave's results render the call that produced
  them rather than the wave's latest start.
  `TestWaveStartsArriveWhileTheWaveRuns` holds a gate closed until
  three starts have arrived; the pre-fix loop emits one and times
  out.
- **error-body cap** (`provider/openai`): a non-2xx response is read
  through a 256-byte `io.LimitReader`, the exact snippet the fault
  carries, instead of whole. The observable fault is byte-identical;
  `TestErrorBodyIsCappedAtASnippet` is regression-proofing rather
  than red-first.

## [1.2.14]: the chain gets a name and the path vocabulary widens

A review pass over the runtime found the middleware chain written out
twice in the composition root: once in `wire` for the kernel and again,
shorter, in `buildSystem` for the guidelines harvest — the canonical
order living in slice literals whose only guard was a sentence in a
PACKAGE.md. The same pass found the path boundary's vocabulary
understated by its own doc (four fields where the doc named three) and
too narrow to catch the name a tool plausibly invents next. The chain is
now one named constructor the root builds through, and the boundary's
field list covers the path-shaped names a call arrives with.

- **canonical chain** (`cmd/rig`): `canonicalMiddleware` is the one
  place the nine links are listed — `toolset.Resolve`, the approval
  gate, the cutoff, the provenance rule, the allow-list, the bound, the
  round cap, the result cap, and `paths` outermost — and both `wire`
  and `buildSystem` build through it, so the system prompt's harvest
  can never name a chain the tools do not run. Order tests pin the
  load-bearing positions: paths run before the gate (the operator judges
  the expanded path), the allow-list sits inside the round cap (a denied
  call still spends the budget) and outside the gate (an unknown tool is
  refused before the operator is asked), and no link contributes
  guidelines (the harvest stays byte-stable). The links and their order
  are unchanged; this names them.
- **path vocabulary** (`middleware/paths`): `Fields` grows `dir`,
  `directory`, `file`, `target`, `dest`, and `destination` alongside
  `path`, `root`, `cwd`, and `project`. A call whose path-shaped
  argument uses one of these now expands at the boundary with no
  per-tool fix; a name outside the list still rides through
  byte-identical, so a `pattern` or a `command` beginning with `~` is
  never mangled. Tests pin both directions: every vocabulary name
  expands, every plausible non-path name does not.

## [1.2.13]: scheduler jobs without the model

Building an autonomous RFP-digest watcher on rig's scheduler hit the
seam this feature closes: a deterministic daily script had to either
burn a model worker on four commands, or drop to a hand-written crontab
line the store would never know about. Hand-written crontab is escaped
state — the harness cannot pause it, audit its runs, or remember it.
The scheduler now takes a `command` payload: a shell line run by
`sh -c` in the job's cwd instead of a worker session, with the same
tagged crontab line, lock, drift, run records, log capture, and
once-fire `done` semantics as a model job.

- **command jobs** (`store/scheduler`): create/update carry `command`,
  mutually exclusive with prompt/model/busy and refused by name; a
  command job's fire skips the busy probe entirely (a loaded GPU never
  delays a cron command) and runs unjailed — the payload is the
  operator's own, the same trust the crontab line itself carries, and
  the jail exists to contain a model's output, of which a command job
  has none. `update` edits a job's command text or its prompt and never
  converts a job's kind; remove + create expresses the conversion. List
  renders `command <line>` in place of `model <id>`.
- **schema 3** (`store/scheduler`): a nullable `jobs.command` column;
  the 2→3 step is an idempotent column add inside the existing
  migration, keyed on column presence the way the 1→2 fold keys on
  files, verified in test against a v2 store.
- **tool voice** (`tool/scheduler`): the schema and the guidelines
  describe the command payload and steer it toward deterministic
  scripts — pollers, digests, backups — never toward work needing
  judgment, which stays a model job's business.

## [1.2.12]: search asks in whole sentences

A debugging pass on the live SearXNG found one healthy engine serving
navigational junk for brand-heavy queries: a query led by a product
name returns that product's homepage, a query led by a single common
token returns a dictionary entry. The tool's guidelines said nothing
about query shape, so every session relearned the failure mode by
burning queries on it. The guidelines now name the shape to prefer,
route known URLs to web_fetch, and refuse identical retries —
prompt-facing guidance that rides in every session, including the
headless ones that never recall memory.

- **query-shaping guidelines** (`tool/web`): web_search's guidelines
  prefer natural-language multi-word queries, refuse leading
  brand/single-token shapes, send known URLs to web_fetch, and demand a
  reword rather than an identical retry on junk. A rig-over-pane
  divergence, like the announced trafilatura fallback; the schema and
  every runtime voice are untouched, and the golden_020 request pins
  are regenerated for the new description bytes.

## [1.2.11]: approve rides the door

A review pass found the approval gate skipped at the wiring for every
doorless frontend, so `approve: manual` in the rig home's settings.json
leaked onto the whole account — delegates, cron workers, `-p` runs —
where the status bar claimed manual while the tools mutated unasked.
The gate now wires unconditionally, and manual rides the door: a
frontend that cannot ask runs auto, so gating a TUI never binds the
fleet.

- **the gate always wires** (`cmd/rig`): `approve.Gate` is appended
  unconditionally and passes through in auto. The middleware seam is
  9 links (was 8).
- **manual applies where a door exists** (`cmd/rig`): a doorless
  frontend resolves approve to auto at the seam. The live `/approve`
  switch keeps its refusal on doorless frontends — a request that
  cannot be honored errors, while the persistent preference scopes
  itself to the frontends that can honor it.

## [1.2.10]: the jail's env lands in pairs

A review pass over the 1.2.x surface surfaced four fixes: the bwrap
jail's env never parsed, the delegate switched the process's own env
around each spawn, the provenance rule fell open on a symlink crossing,
and `-exec` opened the landlock path from any argv.

- **the jail's env rides explicit pairs** (`store/scheduler`): bwrap's
  `--setenv` takes VAR VALUE (two arguments) and the profile passed one
  `KEY=VALUE` argument, so every jailed spawn died in "bwrap: setenv
  failed" since 0.24.2. The argv now splits on the first `=`, an entry
  without one refuses, and the shape test pins the pairs the spec
  (SPEC_SANDBOX 1) already named.
- **the child env rides the spawn** (`store/scheduler`): the delegate
  and the runner switched the process's own `RIG_HOME`/`RIG_DELEGATE`
  around each spawn and restored after — shared mutable state a fan-out
  sibling could observe, and a race where a concurrent delegate read
  the first one's `RIG_DELEGATE` and refused as a worker. The child env
  is explicit at the spawn site now (`os.Environ()` plus the override);
  the process env never changes, and the jailed runner's `RIG_HOME`
  switch — dead weight, bwrap carries it via `--setenv` — is gone.
- **the provenance rule refuses the crossing** (`middleware/perm`): a
  plugins/ path whose symlink resolves outside the plugins root fell
  through to the tool, and an outside path resolving into the loaded
  tree fell through the same way. The zone is judged on both spellings;
  a pending dir relocated by the operator's own symlink stays the
  landing zone (the crossing is judged against the resolved pending
  dir).
- **the exec door opens only with the profile** (`cmd/rig`): `-exec`
  entered the landlock path from any argv; now only with `RIG_LANDLOCK`
  set, so a one-shot prompt of exactly `-exec` stays a prompt.

## [1.2.9]: the docs name the fan-out

The delegation feature never made it into the README or the landing page:
parallel subagent fan-out, mid-turn execution, the slots gate, and the
GPU busy rule were all undocumented, and the README's tool count and
config table were stale (it said 18 built-in tools unconditionally, and
omitted `workers.json`). The docs now say what the code does.

- **README**: the tool count names the fleet condition; `workers.json`
  joins the config table; a subagents section describes the delegate
  fan-out (parallel workers in one turn, the fleet's slots gate, the
  busy rule, no recursion, resumable transcripts).
- **docs/USAGE.md**: the delegate paragraph names the fan-out and the
  busy:skip refusal; the default allow count is conditioned on the fleet.
- **docs/SETUP.md**: the version examples move; the fleet's slots section
  says a fan-out queues beyond the gate.
- **site/index.html**: the landing page gains the subagent bullet and the
  conditional tool count.

## [1.2.8]: the wire prefix is pinned

The cache win (98-99% prefix hits across sessions and models) is the
product of a byte-stable request prefix: the system prompt, the tool
schemas, and the append-only transcript. Nothing guarded that property,
so a timestamp in the system assembly or per-turn tool trimming would
silently kill the cache. The stability is now pinned by tests.

- **the tools prefix is golden** (`cmd/rig`): the registered fleet's
  wire shape (name, description, schema) is pinned as a sha256; a schema
  or description change fails the test, and the golden moves with a
  deliberate commit — a cache-invalidating change is a reviewed event.
- **the wire is deterministic and append-only** (`provider/openai`):
  the same request marshaled twice is byte-identical, and a later
  turn's message array is the earlier one plus the appended tail; the
  two pins name the property the prefix cache is byte-keyed on.
- **the system assembly is byte-stable** (`cmd/rig`): building the
  system prompt twice yields identical bytes, so `time.Now` or a
  session id in the assembly fails loud.
- **tests**: `TestWireToolsPrefixGolden`,
  `TestWireMarshalingIsDeterministic`, `TestWireMessagesAreAppendOnly`,
  `TestSystemPromptIsByteStableAcrossBuilds`. The cache-ratio query
  already exists (`sessions summary`: `cache_read / prompt`, pinned by
  `TestSessionsSummaryCacheRatioFixture`), so the ratio is measured,
  not inferred.

## [1.2.7]: the refusal lands once

Eight refusals returned the same string as both content and error (the
retry guard's bound and round caps, the allow-list, the plugin
provenance rule's three voices, the python tool's two), and the loop's
fed-back error line appended that string again: the model saw each
refusal twice. The error line is now skipped when the content already
ends with it.

- **the fed-back error line is skipped when it is already there**
  (`loop`): a tool result whose exec failed keeps its content and gains
  the exec error on its own line unless the trimmed content already
  ends with it; a refusal that names itself as both content and error
  lands once, and a failing command that produced output still gains
  its error line. The fourth named reopening of the frozen loop;
  SPEC_CORE and the loop's PACKAGE.md carry the name.
- **the middleware chain's order is documented** (`middleware`): the
  parent PACKAGE.md records the root's `wire()` slice — `Wrap` applies
  in order, so the first-listed link is innermost and the last
  (`paths`) is outermost.
- **tests**: a scripted tool returning `(msg, errors.New(msg))` pins
  the transcript to `msg` exactly once; the malformed-call test's
  transcript now carries `synthetic failure` once, and the
  output-plus-error-line path is unchanged.

## [1.2.6]: the pending tail scrolls by rows, not columns

The capped pending prose line cut its visible tail at an exact column
count, so rows ended mid-word and every delta re-sliced the whole tail
at a new column — at streaming speed the tail jumped several times a
second instead of scrolling. The tail is now computed in rows.

- **the tail is the last wrapped rows** (`frontend/tui`): the pending
  line wraps at words on every frame (the committed path's wrap), the
  cap takes the last `cap-1` rows under the unchanged `· k lines
  hidden ·` marker, and `k` is the wrapped total minus the visible
  tail. A laid row never changes while it stays visible: only the
  last row grows with a delta and the block scrolls by exactly one
  row when a new row starts. `tailSegs` is gone; a row exactly width
  wide gets the same pending-wrap guard (the `toCol(1)` before the
  LF) the committed rows get.
- **the wrap never leaves a trailing space** (`frontend/tui`,
  `wrapSegs`): an emitted row dropped its trailing spaces when the
  text ended on one and kept them once the stream moved past — the
  same row changed while it scrolled. Rows now end at the last
  non-space; the committed path's rows lose only invisible trailing
  blanks.
- **tests**: a scripted stream of a 640+ char paragraph into a narrow
  pane asserts, between consecutive frames, that every visible row
  except the last is unchanged or moved up by exactly one row, that
  no row breaks inside a word, and that the hidden count plus the
  visible rows equals the wrapped total; a no-newline stream of a
  wide word pins the exact-width rows.

## [1.2.5]: the box the jail cannot run on

The operator's box cannot run the bwrap jail: the kernel's
`apparmor_restrict_unprivileged_userns` blocks the user namespace
bwrap needs and the operator has no sudo, so `sandbox: "jailed"` was
always refused there and `"off"` ran the cron workers unjailed. The
fix is a second containment profile the box can actually run: Landlock,
the kernel's unprivileged data-access LSM, no namespaces, no user
namespace, no privileges. One boundary, same guarantees where Landlock
can carry them, each residual named.

- **the third sandbox value** (`config`): `"jailed" | "landlock" |
  "off"`; the refusal voice names the vocabulary.
- **the landlock profile** (`store/scheduler`): `landlock.go` plus the
  two build-tagged syscall files (raw syscalls, no `x/sys/unix`,
  linux/amd64 and linux/arm64); the runner probes the kernel (ABI 4+),
  refuses loud and records the skip when the profile cannot run, and
  spawns `rig -p` with the env scrubbed to the named list and
  `RIG_LANDLOCK=<spec json>` carrying the grants: the cwd rw, the
  scratch home, the kernel dir ro, the rig binary ro+exec, the system
  dirs ro, `/proc` read, the device nodes rw, `sandboxBinds` with the
  same rw/ro semantics. Netless: bind+connect TCP handled and granted
  nowhere; the model call rides the one socket.
- **the domain arrives with the image, the only sound point**
  (`cmd/rig`, `tool/execwrap`, `tool/bash`, `tool/python`):
  `landlock_restrict_self` commits per-thread creds and Go's runtime
  has threads before `main()`, so an in-process restrict leaves the
  worker's own goroutines outside the wall — proven: a worker's
  in-process `read` tool returned the operator's `~/.bashrc` under the
  runner's exact env. The runner spawns `rig -exec rig -p ...`: the
  `-exec` branch restricts on a locked thread and execs, so the new
  image and every thread it creates inherit the domain; the worker
  never applies the profile in-process, and `RIG_EXEC_WRAPPER` lets
  bash/python wrap their subprocess execs through the same helper.
- **fail closed, both ends** (`cmd/rig`): the worker restricts at
  startup, before config and before any tool; a malformed spec, an
  unknown spec version, a missing grant, a probe failure, or an
  old ABI refuses loud with a voice naming the alternative.

## [1.2.4]: the failure's diagnosis is canonical too

The containment rule was already canonical; the failure path's message
decision was not. Under a symlinked session cwd, a file or missing path
that was genuinely inside (in either symlink form) was reported as
"outside the session's cwd" instead of naming the real rule. No access
changed — every path in that branch refuses either way — but the error
lied about why, and a wrong diagnosis is a wrong error.

- **the failure branch knows both forms** (`pathguard`): `Within`'s
  refusal now checks the raw and resolved spellings of both the roots and
  the failed path before choosing the containment message; a path inside
  the roots in any form keeps its specific error (`not a directory`,
  `no such file`), and only a path outside both roots in every form names
  the containment rule.

## [1.2.3]: the review pass's stragglers

A second pass over the same seams landed five follow-ups, each with a
failing test first. The one with teeth was the cwd gate: its lexical
precheck refused a legitimate working directory whenever the session
cwd and the passed path disagreed about symlink form.

- **the cwd gate is canonical only** (`pathguard`): `Within`'s literal
  precheck duplicated the canonical containment and over-refused — a
  resolved path under a symlinked session cwd (and the reverse) was
  refused although the canonical check accepts it. The canonical check
  is the one containment rule; both forms now pass and the symlink
  escape still refuses.
- **the fetch tool introduces itself as rig** (`tool/web`): the
  User-Agent said `pi-web-fetch/1.0`.
- **the bound's concurrent doubles are named** (`middleware/guard`):
  identical calls inside one concurrent run all execute (each passed
  the check before any had failed) and the next identical call refuses;
  the documented consequence now has its test.
- **the busy match reads the driver's typed error** (`store/sqlx`):
  `isBusy` matches the `SQLITE_BUSY` code on the driver's `*sqlite.Error`
  first and the message second, so the retry loop cannot lose the busy
  signal to a message change.
- **the read cap ends on a rune boundary** (`tool/file`): a truncation
  landing inside a multibyte rune no longer ships invalid UTF-8; the
  straddling rune is dropped whole.

## [1.2.2]: the review pass's five findings

A fresh-eyes review pass over the security boundaries, the scheduler's
consistency story, and the memory store landed five findings, each with
a failing test first. One was a real authorization asymmetry — the
store's own `Forget` refused another project's memory while a
`supersedes` could reach the same row from anywhere — and one was a
latent wait: the trafilatura subprocess's 20 s cap never actually
bounded the wait while an orphaned grandchild held the output pipes.

- **supersession is scoped** (`store/rem`): `applySupersedes` refuses a
  target outside the caller's scope or global, naming the owning
  project, before any row is touched — the same rule `Forget` already
  enforced. A refused supersede rolls the whole write back, so the
  learn or reflect lands nowhere.
- **the list names orphan crontab lines** (`store/scheduler`): a tagged
  line with no job row — the crash window between the crontab write and
  the store commit — now lists under `orphans:` with the removal
  instruction instead of being invisible; the runner already refused to
  fire it.
- **the fetch guard's denylist is complete** (`tool/web`): TEST-NET
  (192.0.2/24, 198.51.100/24, 203.0.113/24) and the 6to4 anycast
  192.88.99/24 join the refused ranges, so a host resolving there is
  refused like any private address.
- **the update's version compare pads short segments** (`cmd/rig`):
  `1.0` equals `1.0.0` (the old compare called it older and refused the
  update), and a negative segment refuses as not-a-semver.
- **the extraction rides the fetch's total budget** (`tool/web`):
  `ExtractReadable` takes the fetch's ctx, trafilatura's 20 s cap is the
  floor under the caller's deadline, and the subprocess has a
  one-second `WaitDelay` — so a slow extractor cannot outlive the
  requested timeout, and a killed trafilatura's orphaned children
  cannot hold the pipes past it.
- **the viewport fixture's size writes are synchronized**
  (`frontend/tui`): the mutable size behind the resize tests was read by
  the frame-ticker goroutine while the test wrote it — a data race the
  CI runner's `-p 2` scheduling finally exposed. The fixture is now a
  mutex-guarded pair (`get`/`set`), and every resize test publishes its
  new geometry through `set`.

The version is 1.2.2; the changelog, the SPEC_WEB guard list and
extraction budget, the SPEC_STATE supersede and orphan lines, and the
setup examples move with the code.

## [1.2.1]: the winch follows the terminal

One field report: a resize with nothing streaming never repainted.
The winch guard keyed on the input's nonzero fd — `fdi != 0` — while
stdin is fd 0, so a rig whose input was the terminal installed no
SIGWINCH handler at all: only the pty tests (fd != 0) got one. The
repro: a 44-row pane, one character typed (the caret parks on the
input row), the pane shrunk to 12. tmux deletes the rows below the
parked caret — the status rows — and with no handler nothing
repaints them; they stay missing through the regrow until the next
delta or submit. Verified live: an idle rig writes 0 bytes on
`kill -WINCH` and on a tmux resize, 66 bytes on a keystroke; the
process's SigCgt mask lacks SIGWINCH.

- **the winch handler follows the terminal** (`frontend/tui`): the
  input's terminal-ness is its own flag (`tty`), set where the input
  is found to be a terminal, and the handler installs whenever the
  input is a terminal — fd 0 included. `winchLoop` is unchanged: its
  full draw already repaints from the parked aim.
- **the idle repaint is pinned** (`frontend/tui`): a real-pty test
  where rig is idle — no delta, no keystroke — shrinks the pane and
  asserts the status block repaints; the parked repro types one
  character, shrinks, and asserts the status rows the shrink deleted
  return after the winch and after the regrow, the transcript intact;
  and the stdin shape itself is pinned — a pty on fd 0 owns the
  handler. The existing winch tests stay green.

The version is 1.2.1; the changelog, the SPEC_TUI signal-ownership
amendment, and the TUI docs move with the code.

## [1.2.0]: the delegate fans out

The turn's batch admitted `delegate` as a concurrent native, and the
per-session slot gate stopped refusing when the fleet's slots were
full. Fan-out is N delegate calls in one turn: the calls run
concurrently up to `workers.json`'s `slots` (default 1), and a call
that finds every slot held waits on a short poll for one to free
instead of failing; the refusal is the standing voice only when the
call's context ends, with the wait time named. The worker is jailed
and cwd-guarded, cannot recurse (`RIG_DELEGATE`), and its output
returns as a tool result like a read, so running it in parallel is
non-mutating from the brain's side; the approval gate still counts it
as mutating (it spawns a worker and writes stores).

- **`delegate` is concurrent** (`cmd/rig`): the loop's concurrent
  native set grows by `delegate`, so several delegate calls in one
  turn run as a fan-out instead of one-after-another barriers.
- **the slots gate waits instead of refusing** (`store/scheduler`):
  the acquisition retries on a short interval while every slot is
  held, until a slot frees or the call's context ends; on the context
  ending the refusal keeps the standing voices — the one-slot
  `delegate: a delegation is already in flight (this session)`
  unchanged, the full-set `delegate: the session's delegate slots are
  full (slots N)` now naming the wait time.
- **the bound and the wait are documented** (SPEC_DELEGATE 6, the
  package docs): the fan-out replaces the one-in-flight/refusing
  voice; the tests pin the overlap (slots 3), the sequence (slots 1),
  and the slots-full wait (slots 2).

The version is 1.2.0; the changelog, SPEC_DELEGATE's bounds, and the
PACKAGE.md docs move with the code.

## [1.1.4]: the fed-back error line

One field report: a tool failure could reach the model as a bare
`(cwd /root)` line. When bash's child never ran — a bad or unreadable
cwd, Go's `fork/exec /usr/bin/bash: permission denied` — the output was
empty and the reply was just `(cwd /root)`: the loop substituted the
exec error only when the content was empty, and the model saw no error
at all. Alongside it, the system prompt said "the working directory"
without naming it, which is why the model guessed /root.

- **the fed-back error is always a line** (`loop`): a tool result whose
  exec failed keeps its content and appends the exec error on its own
  line, so the model sees the output and the reason; an empty result
  carries the error alone. The third named reopening of the frozen
  loop; the gate's clause and SPEC_CORE carry the name, and the
  re-freeze follows the merge. An unknown tool now returns the error
  only (the loop appends it), so the refusal is never shown twice.
- **bash refuses a dead cwd by name** (`tool/bash`): the cwd is stat'ed
  and checked before the child starts; a missing, non-directory, or
  unsearchable cwd fails with `bash: cwd X: <reason>` and no content,
  the loop feeds the error line into the result once, and the model
  never sees a bare `(cwd X)` or a fork/exec line naming /usr/bin/bash.
  The refusal is a real failure: the TUI shows the fail glyph and the
  retry guard counts it.
- **`~` is the session home, and the boundary keeps the shell's forms**
  (`middleware/paths`): a leading `~`, `~/…`, or `~user/…` in a
  path-shaped argument (`path`, `root`, `cwd`, `project`) expands at
  the path boundary, before any validation; a `~` anywhere else, an
  unknown user, or an unset home stand as given. The boundary already
  did this; the report that tilde expansion was missing was wrong.
- **the session names itself** (`cmd/rig`): the system prompt carries
  the session's working directory and home at session start, so the
  model never guesses where it is, and a leading `~` in a tool path
  expands to the home it was told about.

## [1.1.3]: the parked aim

One field report: the typed-key fast path repaints the input row in
place and parks the caret on it, `parked` rows above the region's
bottom. The repaint's first move re-anchored with a cursor-down back
to the bottom before aiming up at the region's top. tmux handles a
height shrink by deleting rows below the cursor first and only then
scrolling the top into history — the cursor stays put — so a shrink
that lands while parked deletes the rows under the caret, the
cursor-down is a no-op at the bottom, and the following cursor-up
overshoots by `parked`, writing the region over committed rows above
it. The repro: a 24-row pane, one character typed during a streaming
answer, the pane resized through 20/16/12/16/20/24 a few times at
~70ms per step — the first two committed rows of the answer were
overwritten every run. A second report: a 640-char paragraph with no
newline streams (region ~16 rows), one key typed (park 4), the pane
shrunk 24→12 — five wrapped rows of the paragraph stayed above the
committed copy and the first fifty words showed twice, because the
aim was capped at the shrunken viewport before the park was
subtracted.

- **the aim starts from the park** (`frontend/tui`): the repaint no
  longer re-anchors through a cursor-down. The cursor-up before a
  repaint is the region's uncapped row count minus one minus
  `parked`, and only the result is capped at the viewport — a pane
  that shrank under a region painted for a taller one must aim all
  the way to the region's top, not to the shrunken viewport's (an
  aim capped at the viewport first undershoots by `parked` and
  leaves the region's head above the repaint). The park clears after
  the paint. Every path that aimed from the bottom applies it — the
  region repaint, the submit, the winch re-layout, and the in-place
  input edit, whose cursor-up is measured from the parked row. The
  caret still rests on the input row after an in-place edit, and the
  protocol folds the old `ESC[nB ESC[mA` pair into one `ESC[(m-n)A`
  (the golden streams shrink by the pair). The viewport cases are
  named: paint a region, type a character (park), shrink the pane by
  the parked rows, and deliver a text delta — every committed row
  above the region survives and the region paints exactly once; the
  same through the stepped shrink-then-grow sequence; and a shrink
  that lands under a region taller than the target pane paints the
  pending paragraph exactly once across history and the screen.

## [1.1.2]: the page the frame shows

One field report: the pager stepped by logical lines while the frame
fills the view by visual rows, so a record holding lines wider than
the pane — tool results and code blocks commit unwrapped — showed
fewer lines than the step and the lines in the gap were never
displayed. The repro: 40 lines, lines 20-27 padded to 70 chars, a
52x24 pane with a five-row footer, paging up showed L26..L40, then
L10..L23, then L01..L06 — L24, L25, L07, L08, L09 never shown.
Alongside it, the markdown pass read `_` as an emphasis marker inside
plain words, so `cron_audit, gpu_stats` rendered as `cronaudit,
gpustats`.

- **the pager steps by the frame** (`frontend/tui`): PgUp advances the
  offset by the lines the current frame actually showed, minus one;
  PgDn walks forward from the frame's bottom, accumulating `rows()`
  against the same row budget, and steps by that many minus one. The
  offset stays in lines, the pages overlap by a row as they did, and
  every line is reachable: the repro's pages now cover all 40 lines,
  adjacent pages sharing exactly one.
- **underscores need a word boundary** (`frontend/tui`): an `_` opens
  or closes emphasis only when the neighboring character is not a
  letter or digit, the way CommonMark treats intraword underscores;
  snake_case identifiers render literal in prose and in list items.

## [1.1.1]: the painted aim

One field report: the tear was back on 1.1.0. Mid-stream, content
painted over the model info and the spacing between the content and
the input collapsed, until a keystroke relaid the region. The viewport
bound capped the region's height, but the repaint still aimed with the
geometry about to be painted instead of the geometry on screen — three
doors into that one root:

- **the aim is painted geometry** (`frontend/tui`): the live region
  remembers the row count and the width it was last painted at. Every
  cursor-up aims with the painted count, capped at the viewport (a
  paint that overflowed the pane scrolled its own head into history,
  so the painted span is what the next aim must clear), and the
  stability check refuses the in-place input-row edit while the
  painted width is stale — the keystroke takes the full re-layout,
  which aims correctly by construction. A resize re-lays the region
  once, from the true top, at the new width; the transcript survives
  whether or not the terminal ever delivers SIGWINCH, because the size
  is read at the repaint.
- **the viewport budget counts the rows it paints**: the status block
  contributed its logical rows (split on newline) while the paint
  renders their wrapped rows, so a narrow pane let the region run a
  row taller than the screen and every streaming frame scrolled and
  clamped. The budget now counts the block's wrapped rows beside it.
- **a submit repaints the whole live block**: the echo aimed from the
  input row's top, which let the verb menu's rows survive the submit
  as committed-looking text. The submit now aims below the separator
  blank (which survives) and repaints the menu's rows away; a submit
  on a live turn is a steer, and the activity row the turn still owns
  carries into the new region instead of lingering stale above the
  echo.
- **committed bytes expand tabs on the paint seam** (`frontend/tui`):
  a tab advances to the next eight-column stop while the width math
  counts it as nothing, so a tool result carrying tabs — any Go or
  YAML source — rendered wider than `visualRows` saw, and every row
  after the first tab drifted down the frame, baking fragments of the
  status block and of neighbouring rows into the committed block. The
  flow path already expanded tabs; the seam now covers everything the
  region paints, SGR sequences copying through at zero width.
- **the aim caps at the viewport**: the phone's virtual keyboard is a
  height-only resize, and the first repaint after the shrink aimed
  with the pre-shrink painted span, overshooting the shorter screen
  and leaning on the terminal's clamp. The size is read at the
  repaint; the aim now holds the painted span inside whatever the
  pane currently is.

## [1.1.0]: the viewport bound

Three field reports, one root: the live region had no notion of the
viewport's height. A streamed paragraph taller than the pane repainted
with the cursor-up clamped at the screen's top, writing the region over
committed history and leaving rows the bookkeeping could never clear —
the phone's keyboard-open overlap, the lost spacing between the content
and the input, the model info block gone or too far below the input,
and the tearing under long thinking blocks (every 16 ms frame scrolling
duplicate prose into history). Alongside it, the typing fast path was
dead in production: the region-stability check counted the status
block as one row when it is four.

- **the live region is bounded by the viewport** (`frontend/tui`):
  the height is read at every repaint beside the width; the pending
  prose line renders its last rows under the dim `· k lines hidden ·`
  marker (the line still commits whole to scrollback when it closes),
  the menu's candidate window shrinks under the same budget before its
  `… N more` tail and hint row, and the input's five-row window
  shrinks last. The shrink order keeps the operator's controls alive
  as the room runs out; a pane shorter than its own controls renders
  whole and degrades, named.
- **the typing fast path is restored** (`frontend/tui`): the
  region-stability check counts the status block's real rows, so a
  keystroke on a stable region rewrites the input row alone — the
  golden streams shrink by forty rows of re-laid region per keystroke.
- **the escape-capture harness models the viewport** (`frontend/tui`
  tests): height, scroll, and the cursor clamp a terminal applies at
  the margins; the viewport cases are named in SPEC_TUI's testing
  section (the streamed-tail case, the shrunken windows, the
  no-clear-below keystroke, the no-cursor-down frame after a commit).

## [1.0.0]: the tag

The gate the roadmap set before the tag is met: lived use — a worker
soak and the TUI field-tested as the daily driver — with the receipts
in the session store (63 sessions, 724M prompt tokens, 99.08% served
from the KV cache). The freeze has held since 0.3.0, core/ and loop/
open to extension and closed to modification; this tag says the
interface is stable.

- **the version is 1.0.0** (`cmd/rig`): the const, its freeze test
  (the version must now be semver `x.y.z`, not `0.x.y until 1.0`), and
  the setup docs' `--version` examples agree.
- **the example configuration** (`docs/SETUP.md`): the configure
  section gains a `settings.json`, a `models.json`, and a `workers.json`,
  each annotated — the fastest start from a blank rig home, with the
  allow-list's fleet rule and the `/effort` vocabulary named.
- **the roadmap's gate is marked met** (ROADMAP.md): the v1.0.0 line no
  longer waits.

## [0.25.9]: the pre-1.0 polish

The review's stragglers before the 1.0 tag: a constructor that could
panic, a server that never closed idle connections, a Go requirement the
docs understated. The project's front doors land too: how to report a
vulnerability, how to contribute, and the issue forms.

- **the theme is the constructor's third argument** (`frontend/tui`,
  `cmd/rig`): `tui.New` takes the resolved theme and no longer resolves a
  fallback that could panic; the composition root stays the one place
  that reads the theme file. The constructor's contract is now total.
- **the dashboard closes idle connections** (`frontend/web`): the serve
  server sets `WriteTimeout` and `IdleTimeout` beside `ReadHeaderTimeout`,
  so a slow or abandoned client cannot pin a loopback connection forever.
- **the Go requirement is exact** (README, docs/SETUP): `go.mod` requires
  Go 1.26.6; the docs say ≥ 1.26.6, not ≥ 1.26.
- **the front doors** (SECURITY.md, CONTRIBUTING.md, the issue forms): the
  trust model and the reporting path, the spec-first process and the
  house rules, and the two issue templates that ask for the spec. The
  freeze gate's allowlist gains the two root docs.

## [0.25.8]: the tear harness stops replaying the last unit

The flake that CI kept tripping on `TestTearNoSyncPromptNeverBlanks`:
the terminal model kept the last complete unit of every feed call in
its buffer and replayed it on the next call, at a cursor position that
had already advanced. A chunk boundary landing right after a rune or a
CSI shifted and duplicated the following content until the prompt row
was overwritten ("the prompt vanished at byte 9666"). The model now
keeps a tail only when a call ends mid-unit, so the screen no longer
depends on how the stream is split.

- **the model keeps only an incomplete tail** (`frontend/tui`, tear
  tests): `vtStream.feed` tracks whether the call stopped inside a rune
  or CSI; only then does it retain `buf[rest:]`. A call that ends on a
  complete unit leaves the buffer empty, and the same stream replayed
  in 9-byte chunks and in whole frames produces the same screen. The
  prompt stays on screen at every read boundary.

## [0.25.7]: the live frame never erases before it writes

The tear the operator kept seeing through tmux: the sync pair was a
no-op there. tmux 3.4 consumes `?2026h`/`?2026l` without forwarding
them (the pane-side support landed in 3.7), so every repaint was an
unsynchronized erase-then-rewrite, and a write split at the pty buffer
(a large commit, fast text) showed the region erased and blank — the
input line flickered and lost its content. The frame now writes over
the old region.

- **the frame writes first, erases after** (`frontend/tui`, PACKAGE.md):
  a repaint no longer clears the region before writing it. Each row's
  replacement lands, then the tail is erased (`CSI K`); the shrink below
  the last row is erased with `0J` after the content. A reader that
  splits the frame at the pty buffer sees the old frame, then the new
  one in place — never a blank region. The input row's prompt stays on
  screen at every read boundary. `TestTearNoSyncPromptNeverBlanks` feeds
  the real stream through a terminal model that swallows the sync pair
  and splits writes at 9 bytes, asserting the prompt is never gone;
  `TestTearNoSyncPairIsOneWrite` pins the pair and the frame as one
  write.
- **the sync pair closes the frame's write** (`frontend/tui/live.go`):
  `?2026h`+frame+`?2026l` are one `write`, so the end marker cannot be
  separated from the frame it closes. Terminals that support the mode
  (kitty, ghostty, wezterm, foot, iTerm2, Windows Terminal, tmux 3.7+)
  still paint the pair once; the rest ignore it.
- **the support table says tmux 3.7** (PACKAGE.md): the claim that tmux
  3.4 buffers the pair was wrong — 3.4 consumes it. Under 3.4 the
  write-first protocol is the whole protection; the pair is inert.

## [0.25.6]: the stream's idle bound, the model's no default, the seam's dead weight

The pre-v1 review's three findings: the one unbounded wait left in the
loop (a server that holds the stream open without events hangs the turn
forever), the persistence seam's unused postgres adapter, and the
embedded `local` model default (a fresh install now refuses to start
without a model instead of guessing one).

- **the stream's idle bound** (`provider/openai`, SPEC_HARDENING 11):
  the provider already bounds the wait for response headers (5 minutes);
  the body read had none. A stream with no line for 10 minutes — the
  constant beside the header wait, reset by every line (a delta, a
  comment, a keep-alive) — is closed and Faults, naming the bound and
  the silence; a slow stream that keeps sending data never faults. This
  is not the wall-clock cap decision 9 rejected: only total silence
  faults, not total time. No loop change: the Fault takes the ordinary
  mid-stream path. `TestAStallingStreamFaultsAfterTheIdleBound` drives
  the stall (one chunk, then the connection held open: exactly one
  Fault naming the idle bound), and `TestIdleBoundResetsOnEveryEvent`
  pins the reset (chunks every 30 ms against a 150 ms bound: the stream
  completes with `Done`).
- **the busy refusal names the wait** (`store/sqlx`): the seam's
  busy-wait contract now has a named case —
  `TestTxBusyRefusalNamesTheWait` holds the write lock past a caller
  deadline: the refusal names the busy wait and the deadline, not a
  bare driver error — and the wait-out test trades its `time.Sleep` for
  the holder/contender channel handshake (no sleeps, no vacuous pass).
- **the unused adapter is cut** (`store/sqlx`): `ArrayScanner`, the
  postgres array-literal and JSON parsing that existed for it, is
  called by nothing in the tree, and the seam's PACKAGE.md told a
  postgres story (pgx/v5/stdlib) with no postgres driver in go.mod.
  Both are gone; the seam is one constructor, one isolation, one read
  of the context.
- **the model default is gone** (`config`, `cmd/rig`): the embedded
  settings.json no longer carries `model: local`, and a run that
  resolves no model refuses at start — `--model`, `RIG_MODEL`, or the
  `model` key in settings.json — before any store opens or request is
  made (`TestNoModelRefusesBeforeAnyRequest` counts zero requests). The
  embedded models.json keeps the `local` row: a model table is not a
  default, and `model: local` still works when the operator names it.
  SPEC_CONFIG 5, the SETUP knob table, and the README's first-run
  paragraph say so.

## [0.25.5]: the truncated call comes back in-band

The one fault class the soak has recorded: a stream cut off by the
output token limit mid-tool-call used to Fault, killing the turn and
discarding the streamed reasoning and the partial call. The model got no
feedback, so the operator had to steer with the fault text pasted into
the next prompt (2026-09-09, the 36-minute stream that ended in a cut
`bash` call). The cut call now comes back in-band.

- **the cut call is marked, not faulted** (`core`, `provider/openai`):
  `ToolCall` gains `Cut`, the finish reason that cut a call's arguments
  mid-JSON (empty when the call is complete). The adapter emits every
  accumulated call, and a call whose args are invalid at stream end
  carries the marker and is followed by `Done`, never a `Fault`; a
  complete sibling in the same stream still executes.
  `TestLengthFinishedTruncatedToolCallArgsMarked` replaces
  `TestLengthFinishedTruncatedToolCallArgsFault` (which required the
  fault, and was wrong), and `TestStopFinishedMalformedToolCallArgsMarked`
  and `TestLengthCutCallKeepsCompleteSiblings` pin the two edges. The old
  fault text's advice (`raise MaxTokens or the reserve`) was misleading:
  `Reserve` is the context-compaction threshold, not an output budget.
  The allocation, not the size, was the fault.
- **the cutoff link refuses before the tool** (`middleware/cutoff`, new):
  the root's chain executes the link after the allowlist and before the
  approve gate, and a marked call never reaches the tool: the partial
  args are data, not intent, and a truncated command is exactly the
  thing never to run. The refusal teaches: a length cut says to re-issue
  more tersely or split the call; any other finish reason says the
  arguments were not valid JSON. `TestMarkedCallRefusesWithoutExecuting`,
  `TestUnmarkedCallExecutes`, `TestMalformedMarkedCallTeaches`.
- **the model recovers on the next turn** (`loop`, unchanged): the
  refusal feeds back through the ordinary tool-result path, so the
  transcript keeps the reasoning, the partial call, and the refusal, and
  the model re-issues with full context; no `Fault` row is minted, so
  the soak's fault count does not grow for this class. The refusal is a
  failure, so the guard's bound still counts it and `Rounds` bounds the
  turn. `TestTruncatedCallRecoversInBand` drives the whole path.

## [0.25.4]: the release workflow stops trusting apt

The v0.25.3 release died in the minisign step: `apt-get update` broke on
the Chrome repo's stale index (`Hash Sum mismatch` on `dl.google.com`),
so the binary never arrived. The workflow no longer touches apt at all.

- **minisign comes pinned, not from apt** (`.github/workflows/release.yml`):
  the sign step ran `apt-get update`, which refreshes every runner source
  including the Chrome repo whose flaky index fails intermittently, and
  `apt-get install minisign` after it. The workflow now downloads the
  project's static `minisign-0.12-linux.tar.gz` release, verifies its
  pinned sha256 (`9a599b48ba6eb7b1e80f12f36b94ceca7c00b7a5173c95c3efc88d9822957e73`,
  itself checked against the project's official signature), and puts the
  binary on `PATH`. No apt, no sudo, no `dl.google.com`.

## [0.25.3]: the one cwd rule and the fire-time bind check

The deep review pass found the jail fix's one remaining window and the
cwd rule's one duplication. Each carries a test that failed before and
passes after.

- **the job's cwd is revalidated at fire time** (`store/scheduler`): the
  0.25.2 check validated the cwd at create and update, but the jail
  rw-binds the stored path at fire time, and the model can rewrite the
  session's project between the two: create a real directory, schedule a
  once job against it, then rename the directory and leave a symlink to
  `/etc` (or the operator's home) in its place. The bind follows the
  symlink, so the jailed worker gets rw access to the host again. The
  runner now canonicalizes the stored cwd at fire time and refuses when
  the path no longer resolves to itself: a replaced, moved, or deleted
  cwd skips the fire with a recorded reason and a teaching message, and
  the crontab line stays for the list to flag.
  `TestRunJobRefusesAReplacedOrMissingCwd` replaces and deletes the job's
  cwd and requires the skip, not a spawn.
- **the cwd rule is one function, not two** (`pathguard`): the scheduler
  tool carried its own copy of the delegate tool's `canonicalCwd`, and
  the copies had already drifted: the delegate's accepted a file as a
  cwd (it failed at spawn), the scheduler's refused one at the boundary.
  The rule now lives once in `pathguard` (`Canonical` and `Within`), both
  tools call it, and the delegate inherits the directory check.
  `TestDelegateCwdFileRefuses` points a file at the delegate and requires
  the refusal, not a spawn; `pathguard`'s own tests pin canonicalization
  and containment by name.
- **the store waits out a busy write lock** (`store/sqlx`): the
  transaction seam retried nothing on `SQLITE_BUSY`, so a concurrent
  burst that outran the driver's five-second busy timeout (eighty queued
  writers on a slow CI disk) failed creates with a raw "database is
  locked" instead of serializing. `beginTx` now retries the begin while
  the caller's context lives, bounded at thirty seconds with a doubling
  backoff, and the final refusal names the wait.
  `TestTxWaitsOutTheWriteLock` holds the write lock, waits out the
  release, and requires the transaction to succeed.

## [0.25.2]: the jail escape and the missing brakes

The deep review pass found one sandbox escape and four missing brakes. Each
carries a test that failed before and passes after.

- **the scheduler jail could be scoped to the host** (`tool/scheduler`): a
  job's `cwd` was taken verbatim, and the jail rw-binds it
  (`--bind p.Cwd p.Cwd` in `store/scheduler/jail.go`), so a model could
  create a job with `cwd: "/home/<operator>"` (or `/etc`, or `/`) and the
  jailed worker would have rw access to it. The tool now validates the
  cwd the way the delegate tool already does: lexical and canonical
  (symlinks resolved), and only under the session's cwd or the rig home.
  `TestCreateRefusesACwdOutsideTheSessionRoot` creates a job with
  `cwd: "/etc"` and requires the refusal, not a job row.
- **bash output was capped after the fact** (`tool/bash`): the whole output
  was buffered (`bytes.Buffer`) and truncated only after the process
  exited, so `cat` of a huge file pinned it in RAM. The bounded writer now
  keeps only the head at the cap, drops the rest, and always consumes the
  child's writes (the child never blocks); the kept bytes are
  byte-identical to the old truncation.
  `TestBoundedWriterKeepsTheHeadAndNeverBlocksTheChild` writes three times
  the cap and requires the kept output to be the head plus the marker, and
  `TestHugeOutputIsBoundedWithTheMarker` pins the exec's reply
  byte-for-byte.
- **model-supplied timeouts were unbounded** (`tool/web`, `tool/python`):
  `timeoutMs` was taken as-is; the schemas declared `minimum: 1000` and
  nothing enforced it, so a value of 1 ran anyway, a huge value could hang
  a turn for days, and an overflow wrapped negative. Both tools now refuse
  outside `1000..300000` (web_fetch) and `1000..600000` (python) naming
  the range, and the schemas carry the `maximum`. The web case
  (`TestOutOfRangeTimeoutMsRefuses`) and the python case
  (`TestOutOfRangeTimeoutMsRefuses`) pass a table of hostile values and
  require the refusal, not a run. Named change: the E2E timeout case now
  uses the new minimum (`timeoutMs: 1000`) so it stays a timeout instead
  of hitting the boundary refusal, and the wire goldens carry the new
  `maximum` fields.
- **rem content was unbounded** (`store/rem`): `learn` and `reflect` stored
  any size content, so a model could bloat the store and the fuzzy arm's
  trigram table. Both now refuse above 64 KiB naming the cap.
  `TestLearnRefusesOversizedContent` learns just over the cap and requires
  the refusal, not a memory row.

## [0.25.1]: the panic that could kill the harness

The v1.0.0 review pass found two reachable panics and one missing brake.
Each carries a test that failed before and passes after.

- **web_search and web_fetch refuse out-of-range args instead of
  panicking** (`tool/web`): a model-supplied `maxResults` of 0, a
  negative, or a huge integer made `make([]result, 0, n)` panic with
  `makeslice: cap out of range`; a `maxChars` below the schema minimum
  made `CapChars` slice past the string head. The schemas declared the
  bounds but nothing enforced them — the model reads the schema as a
  contract, and the harness must fail loud, not crash. Both tools now
  refuse out-of-range at the boundary, naming the value and the allowed
  range. The tests (`TestOutOfRangeMaxResultsRefusesInsteadOfPanicking`,
  `TestOutOfRangeMaxCharsRefusesInsteadOfPanicking`) pass a table of
  hostile args and require a refusal, not a run.
- **a panicking tool is a tool error, not a process crash** (`loop`,
  named in SPEC_CORE): the batch's `run` executes a tool in a goroutine
  with no recover, so any panic — a tool bug, a hostile arg slipping
  past a schema — took the whole harness down. `run` now recovers and
  surfaces the panic as that call's tool error: the model sees the
  panic text, the transcript survives, the process survives.
  `TestBatchSurfacesAPanickingToolAsAToolError` runs a tool that
  panics and requires the error to land as a `ToolResult`. This is the
  second named reopening of the frozen loop (the first was SPEC_EVT 2a);
  the gate's clause and SPEC_CORE carry the name, and the re-freeze PR
  after the merge deletes the clause.
- **ROADMAP's pointer lands**: "The queue's next lives in the CHANGELOG's
  `[Unreleased]`" pointed at a section that did not exist. The line now
  points at the CHANGELOG's top.

## [0.25.0]: the clean release

v0.24.6 was tagged from the release branch before it was merged, so the
tag and the GitHub Release pointed at the unmerged head. The tree was
identical to main, but the release was not the merge commit. 0.25.0
ships that same tree from the merged main with a clean tag: the last
pre-1.0 release starts clean.

## [0.24.6]: the pre-v1 sweep

Three findings from the v1.0.0 review pass. Each carries a test that
failed before and passes after.

- **read never materialises the file** (`tool/file`): a read of a file
  larger than the 1 MiB cap read the whole file into memory before
  truncating — a model naming a multi-gigabyte file could pin the box
  (the field measurement: 806 MB allocated to read a 64 MB file). The
  read now streams the file once, hashing every byte for provenance and
  capturing only the requested line window, capped at one byte past the
  output cap so the exact cut point is known; the returned bytes are
  byte-identical to the old split-join contract (the trailing-newline
  line count, the truncation marker, the window), and the drift-diff
  cache remembers the window, not the file.
- **the fetch's DNS rides the request context** (`tool/web`): the host
  resolution used `context.Background()`, so a stalled resolver could
  outlive the fetch's own timeout and return a success past the
  deadline. `LookupFn` now carries the ctx (the seam's shape changed,
  named in SPEC_WEB), the default resolver uses it, and a cancelled
  lookup surfaces the context error instead of a generic resolution
  failure.
- **the winch signal stops at Close** (`frontend/tui`): `signalWinch`
  registered SIGWINCH and never stopped it — a goroutine and a signal
  handler leaked past `Close`. The handler now owns a stop that is
  idempotent and synchronous (the goroutine exits before it returns),
  and `Close` calls it.

## [0.24.5]: the dead session's claim is released

The queue's one unpaid debt: a task claimed by a session that died
stayed `in_progress` forever, and every live session reading the queue
had to fail-retry-start it by hand (the operator's cleanup of the
stale queue was the field report). Each finding below carries a test
that failed before and passes after.

- **the dead session's claim is released** (`store/todo`,
  `tool/todo`, `command/tools.go`, `cmd/rig`, `specs/SPEC_STATE.md`):
  the todo store gains a `release` event op and two doors. `Release`
  (the tool and `/todo release <id>`) returns a claimed task to
  `pending`, clearing the owner, and refuses the caller's own claim,
  an unclaimed task, a finished task, and a foreign claim younger than
  `StaleClaimAfter` (24h) — a live session's work is never stolen by a
  tool call. `Reap` (wired at session open in `cmd/rig`) frees every
  foreign claim whose owner's session row has ended (the exact arm:
  `state.ListSessions`, `Exit != "open"`) and every claim whose
  owner's last event on the task is older than the staleness window
  (the SIGKILL arm: a hard-killed session leaves its row `open`, and
  only age proves it). The reaper never touches the caller's own
  claims; the note names each task and the owner it was freed from,
  printed to stderr at open, silent when idle. Tasks survive the
  release — text, deps, and position stay; only the claim dies. The
  compact snapshot now carries each task's `updatedTs` (the claim
  clock), so a compaction folds the log without resetting a stale
  claim's age.
- **the todo schema rides the wire** (`tool/todo`): the `release`
  action joins the schema enum, and the wire goldens were regenerated
  (`cmd/rig/testdata/golden_020`), which is why this entry's diff
  carries the three `.json` fixture changes.



Each finding below carries a test that failed before and passes after.
Field-tested by the harness's own occupant: the session that found the
label bug was reading its own session row.

- **the served model rides the message row** (`core/provider.go`,
  `provider/openai/openai.go`, `store/state`, `specs/SPEC_CORE.md`,
  `specs/SPEC_STATE.md`): the session store recorded the requested model
  id once, at open, and never learned what actually served each turn — a
  `/models` switch or a backend swap behind the endpoint left the row
  lying about who was home. `core.Done` now carries `Model`, the
  response's own echo, and the recorder stamps it on assistant message
  rows (schema v3: `messages.model`, nullable); user, compaction, and
  re-landed rows stay null. `sessions.model` stays the requested id at
  open — the divergence between the two is the diagnostic. The freeze
  gate learned the distinction it was missing: the frozen surface is open
  to pure addition (every old line survives, in order) and closed to
  modification, so extension no longer reads as a violation.
- **the session names itself and counts its tokens** (`store/state`,
  `command/sessions.go`, `tool/sessions`, `frontend/web`): the listing
  was a wall of anonymous ids. `sessions.label` (schema v3) carries the
  first user prompt's first line, trimmed, at 60 runes — written once by
  the recorder, first writer wins, never rewritten; `ListSessions`
  returns it beside the summed prompt+completion tokens. The command,
  the tool, and the dashboard render both; absent labels render nothing.
- **the busy refusal teaches the escape hatch** (`store/scheduler/delegate.go`):
  on a single-endpoint box a delegate from inside a turn cannot win —
  the worker's model would have to evict the delegator's own residency.
  The refusal already named the holder; it now names the path that does
  work (schedule a once-job, it fires between turns).
- **the regeneration is documented** (`store/state/PACKAGE.md`): the
  exact lift invocation for regenerating `domain`/`ddl` after a metadata
  edit, which lived in nobody's head and nowhere on disk.

## [0.24.3]: the release's signature proves itself

Each finding below carries a test that failed before and passes after.

- **the release's signature proves itself** (`cmd/rig/update.go`,
  `specs/SPEC_BUILD.md` 5): the verifier from 0.24.2 was written against
  an invented wire format — a 40-byte public key and a 72-byte signature
  over the raw file. Real minisign 0.11 emits a 2-byte algorithm tag +
  8-byte key id + 32-byte ed25519 key (42 bytes) and, in its default
  mode, a 2-byte "ED" tag + 8-byte key id + 64-byte signature over the
  BLAKE2b-512 digest of the file; the first signed release would have
  been refused by its own verifier even after `MINISIGN_SECRET_KEY` was
  configured. `verifyMinisign` now parses the real format, accepts both
  "Ed" (legacy, raw) and "ED" (hashed) signatures, and refuses the
  invented short format by name; the suite pins golden fixtures produced
  by the actual minisign binary.
- **the release key is pinned in the build** (`config/settings.json`,
  `config/PACKAGE.md`, `docs/SETUP.md`): the operator's minisign public
  key is now the embedded `updateKey` default, so every built rig knows
  which key signs the releases and `-update` verifies out of the box
  (`RIG_UPDATE_KEY` > settings.json `updateKey` > the embedded pinned
  key); env and file still override, and a build without a pinned key
  still refuses loud. The release workflow's `MINISIGN_SECRET_KEY`
  secret holds the key's base64; a missing secret still refuses the
  release before any asset ships.

## [0.24.2]: the update proves itself and the jail starts empty

Each finding below carries a test that failed before and passes after.

- **the update proves itself** (`cmd/rig`, `specs/SPEC_BUILD.md` 5): the
  `-update` path verified the downloaded asset against `checksums.txt`
  from the same release, corruption protection with no authenticity.
  The asset and its `.minisig` are now fetched and the ed25519
  signature is verified against the pinned minisign key
  (`RIG_UPDATE_KEY` > settings.json `updateKey`); a missing key, an
  unsigned release, and a bad signature each refuse loud before the old
  binary is touched, and the checksum lookup matches the exact asset
  field (a name that shares the asset's prefix no longer steals the
  line). The release workflow signs every asset with the
  `MINISIGN_SECRET_KEY` secret.
- **the jail starts empty** (`store/scheduler`, `specs/SPEC_SANDBOX.md`
  1): the bwrap profile passed the operator's whole environment to a
  jailed worker, exported secrets included. The profile now clears the
  environment and names the whole list (`PATH`, `HOME`, `RIG_HOME`;
  `RIG_DELEGATE=1` for a delegate worker) in explicit `--setenv` pairs.
- **the chain's order is written down** (`docs/DESIGN.md`): the
  middleware paragraph described the wrapper stack from the tool
  outward, and "the listing order reads as the execution order" was the
  reverse of the code. The paragraph now prints the execution order
  explicitly: paths -> cap -> rounds -> bound -> allowlist -> plugins
  -> approve -> resolve -> the tool.

## [0.24.1]: the turn's end always leaves one blank row before the prompt

Each finding below carries a test that failed before and passes after.

- **the turn's end always leaves one blank row before the prompt**
  (`frontend/tui`, PACKAGE.md): a reply that ended without a trailing
  newline drew the input line directly beneath the last row. The live
  region supplies its blank margin from `lastBlank`, which was still
  stale-true when the final commit's lines were computed, so the margin
  never appeared. `TurnEnd` now commits one blank row after the pending
  text drains when the last committed row is not blank, and `live.draw`'s
  blank merge keeps a double out when the reply already ended with a
  newline. `TestSpacingRule` pins all three endings (trailing newline,
  trailing blank line, no trailing newline) to exactly one blank row
  before the prompt.

## [0.24.0]: the live region paints once per frame

Each finding below carries a test that failed before and passes after.

- **the live frame is wrapped in the synchronized-output mode**
  (`frontend/tui`, PACKAGE.md): every flushed repaint is written between
  `?2026h` and `?2026l`, so a supporting terminal buffers the pair and
  paints once — the tearing is gone outright. tmux 3.4, kitty, ghostty,
  wezterm, foot, iTerm2, and Windows Terminal support the mode; the rest
  ignore it. `TestLiveSyncFrames` pins the pairing.
- **the live region repaints on a 16 ms frame cadence**
  (`frontend/tui`, PACKAGE.md): `flow` no longer paints per delta — it
  marks the region dirty and the frame tick paints once per frame, so
  tokens that arrive together repaint together (at 75 tok/s the region
  repainted 75 times a second before). The commit points stay immediate
  and drain the pending chunks, and the activity spinner keeps its
  120 ms pace on top. `TestFlowCoalescesDeltas` pins one paint per frame
  window.
- **the frame ticker lives only while a turn or a compaction can paint**
  (`frontend/tui`, PACKAGE.md): the 16 ms ticker used to run for the
  life of the TUI, so an idle session woke about sixty times a second to
  find nothing dirty. It starts with the turn and the compaction, and
  stops once the final commit has drained the pending chunks.
  `TestFrameTickerLifecycle` pins the four transitions.

## [0.23.1]

Each finding below carries a test that failed before and passes after.

- **the retry guard's streak identity is canonical JSON**
  (`middleware/guard`, PACKAGE.md): the bound keyed the streak on the raw
  args bytes, so a model re-serializing the same call with a different key
  order or whitespace got a fresh streak each time and could dodge the
  bound forever. `canonical` re-encodes args with object keys sorted
  (numbers and strings preserved; invalid JSON falls back to the raw
  bytes), so semantically identical retries share the streak while a
  changed value still resets it. `TestCanonicallyIdenticalArgsShareTheStreak`
  and `TestCanonicalIdentityRespectsValuesNotKeys` pin both directions.
- **an interrupted python call no longer leaves a busy kernel behind**
  (`tool/python`, PACKAGE.md): on a context cancel the call only dropped
  its reply, and the still-running cell kept the kernel slot occupied, so
  the next call queued behind it (or timed out) instead of running.
  The interrupt now tears the kernel down like the timeout path does, and
  a reply that arrived before the cancel is still returned rather than
  discarded. `TestInterruptTearsDownTheBusyKernel` pins the poison.

## [0.23.0]: the quality sweep

Each finding below carries a test that failed before and passes after.

- **tool calls are scoped to the session and the message**
  (`store/state`, SPEC_STATE): model-minted call ids are not globally
  unique, and the old `(id)`-only key let one session's result overwrite
  another's row in the shared workspace file. `tool_calls` is now keyed
  `(session_id, message_seq, id)`, the recorder scopes every call and
  result to the session and the landing message, a duplicate id within
  one message is minted to `id-2`, `id-3` (results arrive in call
  order), and the v1 store is migrated on open: `session_id` is
  backfilled from `messages` and the table rebuilt. Compaction replay
  keeps wire ids and attributes a reused id to its own re-landed
  message; the relanded result copies the `err` column from the
  original row.
- **the env and file knobs validate their bounds at the boundary**
  (`cmd/rig`, `config`): a negative `RIG_RETRIES`/`RIG_ROUNDS`/
  `RIG_RESULT_CAP` silently disabled its guard (the guard clamps
  negatives to 1 or 0), and `RIG_RETRIES` ignored a parse error while
  its siblings refused. All three env knobs now refuse loud, and
  `settings.json` `retries` gets the same `v < 0` check as `rounds`
  and `resultCap`. `jsonInt` rejects values at or beyond the platform
  range: `int()` of a float64 past `MaxInt64` is implementation
  dependent and turned `1e300` into a negative.
- **the worker spawn bounds its output capture** (`store/scheduler`):
  stdout and stderr were captured unbounded in memory; each stream now
  keeps the first and last 128 KiB of a 256 KiB budget with a
  truncation marker, so a verbose worker cannot OOM the runner.
- **the memory dedup digest is sha256** (`store/rem`): the natural-key
  digest of model-generated content was md5; the v2->v4 migration
  rehashes legacy rows and renames the column (`content_md5` ->
  `content_sha256`), so the field, the column, and the unique index all
  agree.
- **the dashboard opens the scheduler store with its migration**
  (`frontend/web`): `rig serve` alone on a v1 scheduler store 500'd
  with a schema mismatch; the state store's migration is wired there
  too.
- **a once job's `at` must be in the future** (`store/scheduler`): a
  past `at` made the crontab line fire at the next local occurrence and
  keep firing daily until a run finally succeeded. `NextFire` computes
  in the caller's location, the list shows the stored `at` for once
  jobs, and the crontab calls are bounded at five seconds.
- **an empty completion's usage no longer lands on the previous
  message** (`store/state`); the sessions list counts user turns after
  the last summary marker, so compaction replay no longer inflates the
  turn count.
- **the compact policy locks the transcript mutation**
  (`policy/compact`); the job socket is chmod'd 0600 after listen
  (`store/scheduler`); the pending plugin write opens with
  `O_NOFOLLOW` and a zone move re-checks the destination after the
  rename (`plugins`); the dashboard page limit is capped
  (`frontend/web`).

## [0.22.0]: the third review

Three PRs from the outside review, then a clean pass over every package;
each finding below carries a test that failed before and passes after.

- **edit requires a recorded observation** (`tool/file`, SPEC_CORE
  amended): a threaded session refuses an edit whose path has no
  recorded `FileState`; `read` or `write` mints the license, a
  standalone exec carries no session and so no license to check. The
  drift diff keys on the session: the remembered bytes are keyed by
  session id and path, capped at 16 MiB and FIFO-evicted, so two
  sessions sharing one process never diff each other's observations and
  the cache never pins a session or a file's bytes.
- **the provider bounds its wait for response headers**
  (`provider/openai`): `ResponseHeaderTimeout` on both transports, the
  5-minute default through `NewWithHeaderTimeout`; the plain path is
  `http.DefaultTransport`'s clone, so the dial and TLS-handshake bounds
  survive. A server that accepts and never speaks faults instead of
  wedging the headless worker; the stream after the headers stays
  unbounded.
- **evt refuses adds after stop; the loop keeps empty completions out of
  the transcript** (evt, loop): an `Add` after `Stop` queues nothing and
  returns no id, so a producer of a dead engine cannot grow it; a
  completion with no text, no reasoning, and no calls appends no
  assistant row.
- **the recorder persists file states under the file tool's lock**
  (`store/state`): the files upsert rides the wired `Snapshot` seam, so
  a user typing during tool execution no longer races the tools'
  concurrent records.

## [0.21.0]: the second review

An outside review of the whole tree; every finding below carries a test
that failed before and passes after.

- **the plugin door admits plugins only** (`specs/SPEC_GROWTH.md` 9):
  the door's `Live` seam is `Plugin(name)`, absent for a native. A
  native named through the door was reaching `Exec` past the allowlist
  and the approval gate, which key on the outer call's name.
- **the plugin zone expands `~` itself** (`specs/SPEC_SANDBOX.md` 2):
  `resolvedPath` applies the `paths` rule before the symlink walk, and
  the root lists `paths` last (outermost). A `~/.rig/plugins/x.py`
  write was passing the zone unexpanded and landing live.
- **the SSRF guard parses** (`specs/SPEC_WEB.md`): `net/netip`, 4-in-6
  unmapped, loopback/private/link-local/multicast/unspecified refused
  plus the reserved prefixes the string table named. The vetted
  addresses pin the dial; the transport never re-resolves, and every
  redirect hop re-vets and re-pins. `::ffff:7f00:1`, `0:0:0:0:0:0:0:1`,
  `0::1`, and `::0001` reached loopback before.
- **the scope resolves symlinks** (`store/scope`): the git common dir is
  realpath'd before hashing. git prints it relative from the main
  worktree and absolute from a linked one, so a symlinked cwd split one
  repo into two keys. A repo under a symlinked path gets a new key.
- **a once-job's `done` is an event** (`specs/SPEC_STATE.md`): appended
  in the run's transaction and applied by the fold; the direct
  projection write is gone. The next refold was reverting the job to
  `active`. A done job's consumed crontab line is not drift.
- **the TUI restores the terminal on every exit**: the two `os.Exit`
  paths after raw mode (a bad `-resume` id, a loop error) close the
  frontend first.
- **the freeze gate bites on `main`**: the diff base is the merge-base
  with `origin/main`, then `main`. On `main` itself the old base was
  `HEAD`, an empty diff.
- **the tui tests compile on darwin**: `pty_test.go` is `linux`-tagged;
  the goldens, the freeze gate, and the session tests run here again.

## [0.20.1]: the clock never repeats

- **`evt.Monotonic()` refuses the id collision** (`specs/SPEC_EVT.md`
  decision 2): a same-nanosecond step, or a wall clock stepping
  backwards, takes `last+1` under a CAS, so the id stays unique and
  arrival-ordered. The C accepted the collision and misordered the tie;
  in Go a repeated id was a dropped push, since the queue treats the id
  as identity. The default `Counter()` clock is unchanged.
- **`Event` loses `UpdatePriority`**: mutating a queued event's priority
  from outside broke the heap invariant; `Engine.Update` is the one
  door and already fixes the heap.
- **`Execute(ctx, event)`** takes the context first, as Go does.
- The landing page and README use plainer copy; `ci` and `pages` accept
  `workflow_dispatch`.

## [0.20.0]: documentation release

- Rewrites the documentation for direct, concise technical reading.
- Removes em dashes from Markdown, package documentation, specs, and website
  copy.
- Changes no runtime behavior.

## [0.19.1]: the phone row goes horizontal

- **the scheduler page's phone controls go horizontal**
  (`specs/SPEC_SERVE.md` 16): below 720px the job row's controls
  (pause/resume, remove, runs) no longer stack into full-width
  blocks; they keep the base flex row, wrapping, with the 44px tap
  targets (the `.rowact` class) intact. The remove-confirm and the
  runs row share the rule; the update form stays one column.

## [0.19.0]: the workers file is the fleet

- **the startup block says the fleet, and how to begin**
  (`specs/SPEC_TUI.md` 3, amended): under the session id, `workers:
  <model>` or `workers: none`, then `chat with your model, or type /
  for commands`. The live status row loses its `workers:` segment; a
  static fact reads once at start; the row is for what changes under
  you. The scheduler news line leaves the block too (SPEC_TUI 6, cut):
  identity and invitation only; a job's failure has `/scheduler` and
  the dashboard.

- **the workers file is the fleet** (`specs/SPEC_CONFIG.md`):
  `~/.rig/workers.json` is `{"model": "<merged-table id>", "slots": 1}`:
  `model` is required and must resolve against the merged models table,
  `slots` defaults to `1` and gates concurrent `delegate` calls per
  session. The embedded `models.json` sheds its `qwen3.8-workers` row
  (`local` alone), `settings.json`'s `defaultJobModel` is cut; the
  first start mints `workers.json` from it once and says so, later
  starts nag until the key is deleted, a disagreement refuses:
  and the default allow sheds `scheduler` and `delegate`, growing them
  back only when a fleet stands and the operator named no allow of
  their own. Absence of the file is absence of workers: no
  `scheduler`/`delegate` on the wire, a named `/scheduler` refusal
  (no fleet configured, the file's path in the voice), a dashboard
  refusal in place of the create form, and `workers: none` on the
  status row (a configured fleet names its model there instead).
  `store/scheduler` drops its `defaultModel` constant: `Create` takes
  the model from the caller and an empty one refuses by name, the
  scheduler tool's default and the `run-job`'s model both ride the
  fleet or the job's own row. The delegate's single per-session flock
  becomes one lock per slot: the standing "already in flight" voice at
  one slot, "the session's delegate slots are full (slots N)" at the
  full set. The dashboard's `GET /api/scheduler` carries the fleet's
  model (`worker`, empty when absent) so the view renders its half,
  and `POST /api/scheduler` refuses 400 without a fleet, supplying the
  fleet's model when a body names none.

## [0.18.0]: the job's whole life

- **the scheduler's doors come to the dashboard** (`specs/SPEC_SERVE.md`):
  `POST /api/scheduler/pause|resume|remove|update` and `GET
  /api/scheduler/runs?id=jN&n=` stand beside the create; each calls
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
  is partial; any of `prompt`, `cron`/`at` (mutually exclusive in one
  call, refused by name; create's refusals apply verbatim: a bad ISO, an
  invalid cron, a `once` without its `at`), `model`, `cwd`, `busy`,
  `name`. No fields → `update needs a change`; an unknown id, or a
  removed one, refuses by name; `name` keeps its store-wide uniqueness.
  One `update` op is appended and the fold overlays only the fields it
  carries: the id and the runs stay; remove + create is the rejected
  alternative (the id re-mints, the runs orphan, two crontab moves
  express one change; the trail surviving an edit is the point of having
  one). A cadence change rewrites the job's one crontab line under the
  same key; a paused job stays paused (its line rewritten commented) and
  the new line lands on resume; `pause`/`resume` stay their own ops and
  `update` never changes the state. The `/scheduler` line takes the verb
  free (the command is tool-backed): `update <id> [name <n>] [model <m>]
  [cwd <dir>] [busy <skip|force>] [cron <5 fields|once>] [at <ISO>]
  [prompt <the rest of the line>]`; prompt is last so a prompt that
  says "model" keeps its tail. The tool menu's cost is the one enum word.

## [0.17.1]: a read names its staleness

- **a read that finds a stale observation names it** (`specs/SPEC_CORE.md`):
  when the session's recorded FileState for a path no longer matches
  on-disk, the read's content opens with `[changed since your
  observation] <path>; re-read before acting on it`, and the fresh read
  re-records so it says it once; the drift refusal moved from the edit
  to the moment the model can still act on it.

## [0.17.0]: the soak's vitals

- **sessions, the soak's vitals** (`specs/SPEC_STATE.md` and
  `specs/SPEC_COMMANDS.md`, amended): a read-only native tool (the
  eighteenth) over the session store; `list` is the recent sessions
  (id, started, model, version, turns, faults; newest first), `summary`
  the vitals over the same slice (session and turn counts, the models
  with their versions, the fault count with the latest fault's first
  line, the aggregate cache ratio). The store gains the typed fault read
  (`SessionFaults`); `ListSessions` takes a named limit (`ListCap` the
  default and the maximum) and `SessionRow` carries the model, the
  version, and the fault count. `project` names another workspace (the
  state file is cwd-keyed, one per workspace; a subdirectory or a
  second worktree reads its own file); `n` caps the slice at 1..50.
  The operator gets `sessions summary` (this workspace, the tool's
  reply verbatim). Read-only: absent from the root's `mutatingNatives`
  (it never pauses at the gate) and from the concurrent read set (it
  opens a store, like todo/rem/scheduler, so it is not a pure
  observation).


## [0.16.1]: the kernel knows where it is

- **the python kernel is born in the session's working directory**: it
  inherited the rig process's cwd, so a session started one directory
  up ran kernel code whose relative paths silently pointed at the wrong
  place; a model named it from inside. `SetCwd` on the tool, wired at
  the root; the description says so.

- **grep's glob names its rule**: the schema line reads "a path glob
  (** spans directories; * does not cross /)"; `*.go` matching nothing
  cost a model a call; find already advertised the rule, grep now does.


## [0.16.0]: every row a project

- **rem, the deliberate project** (`specs/SPEC_STATE.md` and
  `specs/SPEC_COMMANDS.md`, amended): learn/reflect/recall/prune gain an
  optional `project`; a path, resolved through `store/scope`, replacing
  the session cwd for that call (worktree-safe, `~` expands at the
  `middleware/paths` boundary), so a session in `~/Projects` learning
  about `~/Projects/rig` files facts into a scope the repo recalls.
  `project` + `scope: global` refuses by name; the description carries
  one short Guidelines clause ("name project when the fact belongs to a
  repo you did not start in"). The operator gets `rem project <path>`:
  that project's live memories in `rem list`'s shape (empty names the
  project); show/forget stay id-addressed and file-wide; the 0.13.0
  forget wall stands.
- **todo is one store, scoped by project** (`specs/SPEC_STATE.md`,
  amended): the per-cwd partition is gone; one `todo/todo.sqlite`,
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
  <path>`; a one-off read of that project's queue, writes staying the
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
  dropped; bold keeps its weight, headings their accent, lists and
  quotes their furniture. The operator's call: the coloring read
  inconsistent, plain reads better. And the status row paints `auto`
  in the warn color beside `manual`; the permissive mode is the one
  worth a glance.


## [0.15.1]: the binary updates itself

- **`rig -update`** (`specs/SPEC_BUILD.md` 5): the binary's own
  installer beside `-version`; resolves the latest release by the
  `releases/latest` redirect (no API call), maps the platform into
  `rig_<os>_<arch>`, verifies the sha256 against `checksums.txt`
  before anything moves, and renames a 0755 temp over the resolved
  executable; atomic on one filesystem, a running rig keeps its old
  inode and the scheduler's next fire gets the new one. Already-latest
  is a no-op; an unwritable directory names itself and the sudo line;
  a platform with no asset and a build with no release tag each say
  so rather than downgrading.

## [0.15.0]: one store

- **the scheduler is one store** (`specs/SPEC_STATE.md`, amended): the
  cwd partition is gone; `scheduler/global.sqlite` holds every job, the
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
  Same-origin is now the browser's definition; the bound address, or
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

## [0.14.1]: the path boundary

- **`~` is the home at the tool boundary** (`specs/SPEC_FS.md`, amended):
  one chain link, `middleware/paths`, expands a leading `~`, `~/…`, or
  `~user/…` in the path-shaped arguments (`path`, `root`, `cwd`) before
  any tool runs; read/write/edit, ls/find/grep, and bash's, delegate's
  and the scheduler's `cwd` inherit it; the tools stay pure. A model
  named the footgun from inside rig (`ls ~/Projects/x` failed where the
  absolute path worked) and the fix's shape: at the boundary, not per
  call. Bytes pass untouched when nothing expands.

- **the scheduler's cwd section names its directory**: `cwd
  /home/x/proj: no jobs` instead of `cwd: no jobs`, so an empty section
  reads as "none here", not "none anywhere"; a model read the old line
  as a lie. The description says another directory's jobs are listed
  from there.

## [0.14.0]: plugin and plugins

- **no round cap by default** (`specs/SPEC_HARDENING.md` 9, amended):
  `rounds` is `0` in the embedded settings and `guard.Rounds(0)` counts
  but never refuses; a one-shot turn with a large model on a real task
  legitimately exceeds 200 calls now that reads overlap, and a cap that
  ends a correct run mid-work is worse than the runaway it guards
  against. The retry bound and compaction still stand. `rounds: N` (or
  `RIG_ROUNDS=N`) caps the turn for an operator who wants the wall.

- **the plugin surface is two natives** (`plugin` run|schema, `plugins`
  list|create|delete|reload; `plugin_schema` and `plugins_reload` fold
  in): delete is disable; the loaded file moves into `plugins/disabled/`
  (reversible with `/plugins enable`), one `Move` shared with the
  `/plugins` command; create and the web forge share one
  `plugins.WritePending` (the filename-stem rule, the native collision,
  the DESCRIPTION/SCHEMA/def run contract; a bad name, a missing
  contract, and a collision refuse identically through both doors).

- **a settings-file allow list needs `plugins`**: a home whose
  `settings.json` writes an `allow` key replaces the embedded default
  (which is the native set), so it must carry `plugins` or every
  `plugins` call is `permission denied`; the same rule that landed
  `plugin`/`plugin_schema` in 0.12.1.

## [0.13.0]: rem is deliberate

- **the dashboard's quick hits**: the plugin listings read every
  DESCRIPTION form; the parenthesized implicit concatenation seventeen
  of the operator's plugins use listed as "(no DESCRIPTION)" (one static
  extractor in `plugins`, shared by the dashboard and `/plugins`); the
  models view shows the context window and the output length only; a
  failed task carries `retry` beside the hands (`/api/todo/retry`,
  `todo.Retry`); lines hang their wrapped text under the text, not the
  glyph, on phones.

- **rem is deliberate** (`specs/SPEC_STATE.md`): every rem operation is
  something chose; the model learns/recalls/reflects/prunes through its
  tool, the operator prunes through the `/rem` verb; nothing is written
  by a compaction and nothing is read into the prompt by a session
  start. Two cuts, one new verb surface, one scope change.

- **the injection is cut**: the root's `remembered` segment (the
  `remstore.Recent` read, `rememberedK`, `renderRemembered`) is gone:
  no memory is read into the prompt at a session start. The system
  prompt's "Remembered notes are suggestions…" becomes "Memory is a
  tool: recall before re-deriving a project fact, learn deliberately
  what the next session should not re-derive, supersede by id when the
  code disagrees" (settings.json, the goldens, the config tests).

- **the auto-reflection is cut** (`specs/SPEC_COMPACT.md` 6): the
  `WithAutoReflect` seam, `store/rem.AutoReflect`, and the
  `autoReflectionImportance` constant are gone; compaction writes
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
  launch cwd; rows learned from another directory of the same repo move
  when rig is next started there, since a hash cannot be walked back to
  its cwd. The git probe resolves a relative common dir against the cwd
  and treats an echoed option (git < 2.31 passes `--path-format` through,
  exit 0) as no path.

- **`store.Open` refuses what it cannot migrate**: an older file opened
  by a build that passes no migration is a named mismatch, not a silent
  version stamp; the newer-file refusal runs before any schema statement
  touches the file; the migrations and the version bump commit together.

- **`/rem forget` is scoped**: ids are file-wide, so a typo must not
  reach another repo's row; only this project's or a global memory is
  removed; another project's id is refused by name with its label.

- **the `/rem` command** (`specs/SPEC_COMMANDS.md` 11): `rem [list|show|
  forget]` over the same store; list the live memories (project then
  global, one line each), show by id, forget by id. The model's
  multi-line learn/recall/reflect/prune stays the tool; the operator's
  read and prune get a typed line. Rejected, named: `rem pin`; importance
  is only the per-access reinforcement multiplier, so the verb changed
  nothing the operator could see, and an operator write that is not a
  prune is off the surface.

## [0.12.2]: the two bounds

- **the description's shape** (`specs/SPEC_CORE.md`, the Tool section):
  every tool's description is the same four parts; what, a
  `Guidelines:` sentence on when and when not (it was missing from 13 of
  18), the reply's shape, at most one gotcha; storage mechanics leave
  the wire for the `PACKAGE.md`s; the scheduler's "pi session" / "pi
  model id" become rig's words. A case in `cmd/rig` pins the whole
  menu under 14,000 chars on the wire and refuses another harness's
  voice, so growth is a decision; the schemas of rem, scheduler, todo,
  and diff are the next lever, named.

- **no comments in Go, anywhere**: the sweep strips every comment from
  implementation and test code (147 files; generated code and the
  `metadata` packages exempt; lift reads their doc comments), the
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
  what to do; stop and report, or ask the operator. It caps the
  alternation the retry bound's per-args streak does not (SPEC_HARDENING
  7's named consequence) and a runaway batch: a concurrent run of 50
  reads counts 50, the cap on calls, not turns. The counter sits under a
  mutex (SPEC_EVT 6's concurrent chain) and `TurnStart` clears it like
  the bound. `core/` and `loop/` stay frozen at 0.12.0.
- **the result bound** (`specs/SPEC_HARDENING.md` 9): `guard.Cap(bytes)`
  (settings `resultCap`, default 64 KiB) bounds every tool result before
  the transcript, in one place; an oversized result truncates to the
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

## [0.12.1]: the door is allowed

- **the plugin door was never allow-listed** (`specs/SPEC_GROWTH.md` 9,
  amended): `plugin` and `plugin_schema` joined the native set in the
  door round but not the embedded `allow` default, so every plugin call
  through the door since 0.9.0 was `permission denied: plugin is not in
  the allow-list`; on a home without an `allow` key (the embedded
  default), which is the daily driver's. The two names are in the
  default now, and a named case pins the rule the bug broke: the
  embedded allow is the native set, exactly.
- **the plugin switch is a directory** (`specs/SPEC_GROWTH.md` 9,
  amended): `settings.json`'s `plugins.enabled` inverted the default:
  on a home with no list, `disable` changed nothing and `enable X` hid
  every other plugin, and its filter ran only at wiring, so a reload
  undid a toggle until the next start. Retired. `/plugins disable
  <name>` moves the file into `plugins/disabled/` and reloads; `enable`
  moves it back; `plugins disabled` lists the zone; the dashboard shows
  all three zones; `max` now applies on every reload. A non-empty
  `plugins.enabled` refuses at load naming the move.

## [0.12.0]: the event loop

- **the loop is the engine's consumer** (`specs/SPEC_EVT.md` 7, the
  named reopening after 2a): every step of a turn is a closure on the
  loop goroutine; the Frontend's `Input`, the provider's stream, and
  every tool run are producers that post (input 90, stream 50, tool
  completion 50). The Frontend contract, the event bracket, the
  transcript, and every named loop case are unchanged; thirty-six
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

## [0.12.0]: the event loop

- **the event loop, phase 1** (`specs/SPEC_EVT.md`): libevt's shape
  (`~/Projects/libtrdr`) made Go-centric as the leaf package `evt`:
  `Context`, `Event`, `Queue` (the 4-ary max-heap, priority desc then
  arrival, with the one addition `Update`), `Engine` (one consumer,
  many producers, a mutex and a cond where the C spun), `Scheduler`
  (the harness, the codes as errors), `Clock`. libevt's tests by name.
  Consumed by nothing yet: phase 2, named in the spec, makes the turn
  loop its consumer (parallel tool calls as goroutines posting ordered
  completions) and reopens SPEC_CORE.

## [0.11.0]: the delegate

- **the one-shot worker tool** (`specs/SPEC_DELEGATE.md`): `delegate`
  spawns a headless worker on a task now, in a cwd under the session's
  or the rig home, waits, and feeds back the worker's last message.
  One tool over the existing runner; the jail per the sandbox setting
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

## [0.10.2]: the todo's hands

- **start and done from the dashboard** (`specs/SPEC_SERVE.md` 15): a
  pending task carries `start` and `done`, an active one `done`; each is
  the store's own verb (`todo.Start`, `todo.Complete`) attributed to
  `dashboard`, the reply verbatim, the queue re-read; a task the model
  claimed refuses in the store's voice (fail it first to take over), as
  the tool would.
- **the models view in three rows**: name and role, the context line
  (window · max · reserve · keep · trigger), the effort ladder in the
  ramp's colors.
- **the mobile header stacks**: the workspace on its own row, add and
  browse on the row after.

## [0.10.1]: the dashboard in the TUI's grammar

- **the dashboard speaks the TUI's grammar** (`specs/SPEC_SERVE.md`
  11–14): every view is a tool block (`● name · detail` … `name ✓`),
  input is a `❯` prompt row, no panels; the models view in the `/models`
  table's line with the effort ramp; the memory tab and route removed;
  the plugins page split approved | pending with the forge; an embedded
  python editor (gutter, highlighting, no dependency) over three doors:
  source by zone, save into the pending zone (the contract checked, a
  native name refused), approve (the command's verb; an installed name a
  409 until `replace`); a folder browser for the workspace picker, rooted
  at home, directories only, symlinks resolved, capped; the mobile nav
  toggle hidden on desktop (the bug: it was hidden by a class the button
  did not carry).
## [0.10.0]: the dashboard's polish round

- **the polish round** (`specs/SPEC_SERVE.md`, phase 2): the local
  dashboard grows its two named phase-2 writes; a scheduler create
  (the same `scheduler.Create` verb a live session calls, attributed to
  `dashboard`, the runner command the root wires) and a plugin create
  (one contract file into `plugins/pending/`, the provenance rule's
  landing zone, refused by name against both zones). The plugin listing
  goes live (read per request, never cached), the cwd picker accepts
  new workspaces (client state over the server's list), and the sidebar
  collapses into a drawer below 720px.
- **the TUI homage** (phase 2, decision 10): the page adopts the TUI's
  visual grammar; the todo and scheduler text are parsed with the
  `frontend/tui` `tools_render.go` rules and rendered in the oled slots
  (the progress bar, the status glyphs, the dim metadata, the warn
  `drift:`), the sessions and transcript take the tool-block shape
  (the `✓ name · detail` opening, the `❯` prompt, the `→` result, the
  aggregated usage row), and unparseable text keeps the verbatim
  `<pre>` (the TUI's own fallback).

## [0.9.2]: the streamlined contract and the self-healing door

- **the contract split** (`specs/SPEC_STREAMLINE.md` 1): the standing
  context carries shape, the responses carry semantics. The todo,
  scheduler, and rem descriptions trim to the verbs and the arguments;
  the state machine, the claim rules, the auto-start, and the
  compaction sentence ride the voices that already teach them on
  contact. The pinned goldens regenerate in place (the SPEC_PLUGINS 8
  precedent); the wire carries the trimmed bytes.
- **the compaction fact** (2): the operation that crosses the
  1000-event threshold names the fold in its own reply:
  `· log compacted (N events folded into the snapshot)`, so the stale
  footer's quieting after the fold reads as explained, not as state
  loss. Below the threshold the replies are byte-identical to
  0.9.1's.
- **the minted-id voice** (3): the unknown-id refusal at every verb
  now reads `no task 'tN' (ids are minted by the tool; copy from a
  reply)`, the teaching on contact instead of standing prose.
- **the door's self-heal** (4, 5): the `plugin` and `plugin_schema`
  doors take a redo seam; an unknown name runs the root's reload once
  and re-resolves, a nil redo keeps today's refusal, and a failing redo
  is named in the refusal. The authoring dance loses its reload step:
  write to pending, the operator approves, the model calls the door:
  and the `/plugins create` template says so. `plugins_reload` stays
  the operator's explicit verb.

## [0.9.1]: the estimate tells the truth

- **calibration trusts a measurement, not a turn** (`specs/SPEC_COMPACT.md`
  4, amended): a delta under 2% of the anchor no longer moves the factor
 ; a tool-loop turn's `reported − anchor` is the template's overhead and
  the reasoning it keeps or strips, none of it in the delta's bytes; the
  clamp ceiling is 2.0. The field shape: per-turn ratios of 44, 0.31,
  31.8, 0.02 pinned the factor at 4, the brain compacted 20 times at ~50k
  real tokens, and a 125k summary input read as 427k and faulted.
- **history reasoning leaves the estimate**: `Reasoning` counts on the
  last assistant message only; every chat template strips it from prior
  turns, so the server never counted the 8.3 MB one session carried; a
  `-resume` of it would have compacted everything on the first assemble.
- **the oldest slice that fits** (3, amended): an older prefix whose
  summary input does not fit the window is cut to the largest prefix that
  leaves the summary floor, one call, the remainder folding on a later
  pass, where it was a fault that stuck the session until `/new`. A
  single message that alone does not fit is still the loud failure.

## [0.9.0]: the plugin door and the enablement

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
  <name>` / `disable <name>` toggle the file and reload; the
  models-switch semantics, next-turn.

## [0.8.2]: the plugins land

- **the allow-list's presence reversal** (`specs/SPEC_PLUGINS.md` 7,
  amended): an installed plugin's presence in `plugins/` root is itself
  the allow-list entry. The provenance rule forces a model's
  `write`/`edit` into `plugins/pending/`, so a plugin in the root can
  only have arrived by the operator's `/plugins` approve; that presence
  IS the admission, and the operator need not add a name to `allow`.
  The mechanism is a second door (`perm.AllowlistWithDoor`) wired to the
  live plugin table's `IsPlugin`: a name the table carries as a plugin
  passes though absent from the static list. Plugins only, never natives
  (the collision rule keeps the sets disjoint); nil door is today. Pinned
  with approved-passes / pending-refused / deleted-after-reload-refused /
  native-still-refused. `loop/` and `core/` stay byte-frozen.
- **the plugins are a live surface** (the local install): `listen`,
  `net_conn`, `syshealth`, `url_check`, and `plugin_scaffold` land as
  real tools; read-only probes, the SSRF-guarded fetch, and the
  scaffold that writes the next plugin into `pending/`. `plugin_scaffold`
  closes the loop: author, approve, reload, land.

## [0.8.1]: the walls speak

- **the system prompt names the walls** (`config/settings.json`): the
  default grows from two sentences to five; the harness enforces an
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
  and 457 calls came back `(no output)` ok; a silent success that ran
  nothing. `code` joins the schema enum.
- **the guard's spec says one thing** (`specs/SPEC_HARDENING.md` 7):
  the streak is per tool with a last-failed-args marker; identical
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

## [0.8.0]: the modes

- **`/effort`** (`specs/SPEC_MODES.md` 1): the session's reasoning
  budget in the row's own vocabulary (`models.json` `efforts`); a
  provider decorator stamps the dial onto requests that carry none;
  the compaction summary's own effort survives untouched; unset is
  today's bytes. A model switch resets a level the new row does not
  name; loudly, in the `/models` reply; never stamping a level into
  a template that cannot speak it.
- **`/role`** (2): default, architect, or reviewer: the stance's
  prose sits between the system prompt and AGENTS.md (position is
  precedence: the contract reads after it and wins), rebuilt on the
  switch, next-turn.
- **`/approve`** (4): auto (today) or manual: every mutating tool
  call pauses for the operator's y/n at a TUI ask row (y runs, n
  declines, Esc declines and interrupts; a denial is a model-visible
  teaching refusal, the turn continues). The gate is a middleware
  wired with closures; the ask door is an optional frontend
  interface; `core/` and `loop/` byte-frozen, the Frontend seam
  untouched. The read set and the store tools pass silently; every
  plugin pauses. The default is settings.json `approve`; workers and
  the one-shot never ask.
- **the status is three rows** (3, amended): identity (`model ·
  used/window`), the stance (`effort · role · auto|manual`; the
  effort in pane's footer ramp colors, new `effort*` theme slots;
  the role abbreviated; manual in the warn), and the usage totals.
- **the distribution surface** (`specs/SPEC_BUILD.md` 5): the first real
  tag ships through a release workflow (the tag asserted against the
  `Version` const before any asset is built, `CGO_ENABLED=0` cross-builds
  linux/darwin x amd64/arm64, checksums, provenance attestation, the body
  from the matching CHANGELOG section), a POSIX installer fetches and
  verifies the binary, and a static install site serves it.

## [0.7.0]: the reload and the forge

- **plugins register without a restart** (`specs/SPEC_PLUGINS.md`
  decision 8): `plugins_reload`, the fifteenth native; re-runs the
  discovery over the rig home's `plugins/` directory (the same loud
  skips, the same collision refusal, removal free: the list rebuilds
  from disk) and swaps the kernel's tool list at the root. The seam
  is named and built (`middleware/toolset`, pure core): the root owns
  one live tool table; a provider wrapper stamps the table's specs
  into every request before delegating, and a middleware, innermost
  first, resolves a call against the table before the chain's
  participants bound its result, falling through to the loop's own
  exec. A swap is one atomic write to that table: the next turn's
  request carries the new list and the new tool executes, by
  construction; the models-switch's semantics, **zero `loop/` and
  `core/` lines** (both byte-frozen against the branch's base), the
  loop's snapshot is the bootstrap and the table is the truth.
  `/plugins reload` is the operator's verb (the same re-discovery,
  from the command door); `/plugins create <text>` queues the
  authoring prompt (the steer precedent: the command queues a line,
  never dispatches a turn); the forge's contract, the model
  authors, the plugin lands in the pending zone (SPEC_SANDBOX's
  provenance, the gate this decision waits on), the operator
  approves. **The approve's tail is the reload's**:
  `/plugins approve <name>` moves the file and re-registers, and the
  next `/plugins` listing follows the swap (the listing reads the
  root's state at call time). The reload imports into the running
  kernel, so a new plugin's functions are callable from the python
  tool immediately; the shared namespace, one process, and callable
  as a tool on the next turn. The no-plugins wire's golden pin moves
  with the set: the fixtures regenerate in place (the 0.2.0 wire
  baseline, the 0.7.0 native set; 15, `plugins_reload` among them),
  as the earlier releases' did. `core/` and `loop/` zero diff.
  Version 0.7.0; still pre-1.0.

## [0.6.0]: the worker jail

- **the scheduled worker runs jailed** (`specs/SPEC_SANDBOX.md`
  decisions 1, 3, 5): the `run-job` runner spawns the worker under
  bubblewrap's `--unshare-all` profile; the spec's block, verbatim,
  composed by a pure function (`store/scheduler/jail.go` pins it
  line-for-line): the ro system, the fresh `/proc`/`/dev`/`/tmp`, the
  job's cwd rw, the operator home's kernel directory and the rig
  binary ro, the socket's one bind, and the worker payload. The
  worker is netless except **one unix socket**; the runner's socket
  proxy (stdlib `net` + `httputil`, no new Go dependency) listens on
  it and forwards to the swap endpoint, the OpenAI `/v1` prefix
  applied exactly once, whatever the operator's spelling; the socket
  is removed after the run, nothing answers. The worker's rig home is
  the **scratch home** `<job cwd>/.rig-job`: the worker's stores land
  inside the jail, and a worker that cannot write the operator's
  stores cannot poison the next session's transcript; the
  operator's `~/.rig` is byte- and mtime-untouched after the run.
  **Fail closed is the default**: `sandbox: "jailed"` refuses the run
  loud (recorded as a skip, the outcome row carries it) when bwrap is
  absent, on a non-linux platform, or where the operator home's
  kernel directory is absent; the refusal names bwrap and the
  profile and teaches both settings keys; `sandbox: "off"` is the
  operator's explicit act, runs the worker as before with exactly one
  loud line per worker run. `sandboxBinds` rides the profile as extra
  binds (an absolute path, ro by default, `:rw` opts one in; the
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

## [0.5.0]: the plugin provenance rule

- **creation separated from installation** (the forge's gate,
  `specs/SPEC_SANDBOX.md` decision 2): `~/.rig/plugins/`, top level,
  stays the TRUSTED set the discovery loads; `~/.rig/plugins/pending/`
  is the FORGE's landing zone; invisible to discovery by the existing
  top-level rule (no loader change at all), created at startup, silent
  and idempotent (the write tool makes no directories, so the model's
  first pending write must not depend on the operator's mkdir). The
  perm middleware gains one path rule beside the allow-list: the
  model's `write` and `edit` refuse a target inside `plugins/` that is
  not inside `plugins/pending/`, and the refusal teaches:
  `permission denied: <path> is in plugins/ outside plugins/pending/
  (plugins install by the operator's /plugins approve; write to
  plugins/pending/)`. The rule is the guard for the honest path, not
  the boundary: bash can still move a file there (the operator's shell
  is the operator's); the worker jail is the boundary, the provenance
  rule is the workflow. `/plugins pending` lists the zone with each
  file's DESCRIPTION (the file's top-level string literal, read without
  running the file; a pending file is untrusted); `/plugins approve
  <name>` moves one to the top level with the atomic rename; a name
  that collides with a native tool refuses with the existing rule's
  voice (the existing rule at the new door), and a file of the name
  already installed refuses too (a clobber is not an operator's verb by
  accident). Approval is the operator's verb: it never runs from a tool
  call (the command door is Frontend-side by construction). The reload
  is SPEC_PLUGINS decision 8's, and the forge (SPEC_PLUGINS 8) is
  unblocked by this. `core/` and `loop/` zero diff. Version 0.5.0;
  still pre-1.0; the freeze's discipline and the tag's criterion
  unchanged.

## [0.4.0]: the rig home and the python plugins

- **the rig home** (the `~/.config/rig` move, `specs/SPEC_CONFIG.md` 11):
  the config home is `~/.rig`; the `.pi`/`.omp` convention, not the XDG
  one. The resolution is stated once: **`$RIG_HOME` > `~/.rig`**; the
  env var (non-empty) is the home, the operator's spelling used as-is;
  unset, `~/.rig`. `RIG_HOME` is the one new env var (the config spec's
  non-goal's named amendment); `XDG_CONFIG_HOME` is no longer consulted.
  The migration is once and deterministic: at startup, if the resolved
  home is **absent** and the old `~/.config/rig` **exists**, the old
  directory is **renamed** to the resolved home (atomic; both live
  under `$HOME`) and exactly one line says so; a present home wins,
  whatever the old directory holds, a failed rename refuses the
  start loud, and it is a **default-path event**: under an explicit
  `RIG_HOME` the migration never runs (the override is isolation, not
  a move order; an absent override stays absent, the old home stays
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
  through the **shared python kernel**; the same persistent kernel as
  the `python` tool, one process, the namespace shared on purpose (the
  model's python can call plugin functions directly; plugin state
  persists across calls); imports each file, reads the three names,
  and registers the tool on the existing `core.Tool` seam,
  indistinguishable from a native tool on the wire (the wire's head is
  the 14 natives in order; the plugins ride the tail, in file order). A
  file missing a piece or failing import is a **loud skip** (one line
  naming the file and the field; the kernel's own voice; startup
  continues); a name colliding with a native tool is a **loud refusal**
  at startup (native-wins would be silent shadowing; refuse instead);
  a kernel-level discovery failure refuses loud (fail closed). A call
  invokes the module's `run` with the model's args dict in the same
  kernel: the return `str()`s into the tool result, an exception is a
  tool error carrying the traceback tail, and the kernel stays alive.
  No plugins directory (or an empty one) is a no-op that never starts
  the kernel; the `golden_020` fixtures are untouched. `/plugins` is
  the standard set's eighth entry: the loaded (name, description, file)
  and the skipped (file, reason), in file order. The plugins are
  subject to the allow-list like any tool and are **not** in the
  built-in default (the operator allow-lists a plugin's name). The
  sandbox is named and deferred: pre-sandbox, trust the plugins as you
  trust your own python. `core/` and `loop/` zero diff; the `plugins/`
  leaf and the `tool/python` `Run` door (the raw reply the discovery
  and calls ride) are the only new surface. Version 0.4.0; still
  pre-1.0; the freeze's discipline and the tag's criterion unchanged.

## [0.3.0]: config as a first-class runtime component

- **config loading** (the pre-10 runtime component, `specs/SPEC_CONFIG.md`):
  flags, env, and constants become a four-layer resolution per key:
  **flags > env > file > embedded defaults**, with the defaults moved out
  of code into the embedded `config/settings.json` and
  `config/models.json`. `config/` is a new stdlib leaf that parses; the
  root (`cmd/rig`) consumes; one `config.Load(dir, cwd)` per process,
  after flag parse and before any store is opened, on every entry mode
  (REPL, `-p`, `run-job`; the worker inherits its own job-cwd's
  `AGENTS.md`, not the creating session's). The user files under
  `~/.config/rig/`: `settings.json` (the existing knobs, flat, by their
  env names; `allow` a JSON array there, CSV in the env), `models.json`
  (the table out of code: `models.Defaults` removed, the user file merges
  over the embedded row by row; set fields replace, unset fields keep,
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
  0.2.0 bytes; pinned against golden request-body fixtures captured from
  the 0.2.0 build (the named exception: the `/models` role column).
  Version 0.3.0; `core/` and `loop/` zero diff.

## [0.2.0]: the feature-complete runtime

- **user commands** (roadmap deliverable 9, `specs/SPEC_COMMANDS.md`): the
  command seam and the first seven commands; the human's own verbs, over
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
  first non-passthrough `ContextPolicy`; `policy/compact` wraps the
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
  answer that logs success; a kept batch larger than the model can hold.
  The summary call carries a lower reasoning effort (`medium`) where the
  provider supports it (`Request.ReasoningEffort` on the wire in both
  shapes: top-level `reasoning_effort` for OpenAI-shaped servers and
  `chat_template_kwargs.reasoning_effort` for llama.cpp; measured on the
  swap, only the kwargs entry changes the think length): it is the one
  call whose thinking nobody reads. The summary prompt also says prose
  only, no tool calls, so a max-effort model can't answer the summary
  with a call. **The summary request is two messages**: a short system
  role, and one user message carrying the older prefix as a quoted
  `<transcript>` block (role-prefixed lines, tool calls and results
  included) followed by the prompt's instruction; the prefix is data,
  not a live conversation, so the model summarizes it instead of
  continuing it (a last "reply with only X" stays inside the block).

- **runtime hardening** (roadmap deliverable 7, `specs/SPEC_HARDENING.md`):
  the seams and events everything after this needed, in one named loop
  change (L1–L7). **Tool events**: `ToolStart{Call}` and
  `ToolResult{ID, Content, Err, Duration}` bracket each execution in the
  Event vocabulary (the bracket wraps the whole middleware chain, so the
  result carries the guarded verdict); the CLI renders `● name` and
  `name ✓|✕ duration` and the old `[call]` line is gone; the recorder's
  middleware tap is retired; it sources its rows from the loop's events,
  and the root's chain is `[perm, guard]`. **Reasoning round-trip**:
  `ReasoningDelta` streams (thinking precedes speech), `Message.Reasoning`
  accumulates in both assistant branches and rides the wire back as
  `reasoning_content` (the `messages.reasoning` column lands); the CLI
  renders it verbatim, one-shot ignores it. **Usage cache fields**:
  `CacheRead`/`CacheWrite` on `Usage`, mapped from
  `prompt_tokens_details` (absent → zero); the adapter now requests the
  stream's usage chunk (`stream_options.include_usage`, wire-shape asserted)
 ; without it OpenAI and llama.cpp emit no usage at all; the CLI sums per
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
  web_fetch: the guarded fetch; http(s) only, DNS refuses private and
  link-local space before any connection, every redirect hop re-guarded,
  hop cap, textual content types, declared-and-streamed 5 MiB byte cap
  with the loud marker, the 20 000-char cap with pane's elision marker,
  the 30s whole-fetch timeout, egress through the compose's tinyproxy
  (:8889; LOOPER_WEB_FETCH_PROXY, set empty = direct), and the
  unreachable-proxy fix-it voice. Extraction: trafilatura as a
  documented external (shared venv, then PATH; LOOPER_TRAFILATURA
  explicit, empty = off), degrading to pane's stdlib text pass, and
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
  policy; `LOOPER_PYTHON` is the operator's explicit interpreter and the
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

- **tool/todo** (roadmap deliverable 2): the task-queue store: pane's
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

## [0.1.0]: initial release

Shipped through the stacked-review process: each slice landed on its parent,
gated on `go test ./...` / `go vet ./...` / `gofmt -l`, merged down-stack.

- **core + loop**: the typed seams (`Provider`, `Tool`, `Frontend`,
  `ContextPolicy`, `ToolMiddleware`), wire types, the streaming-event
  vocabulary, versioned session JSON with provenance and exec records, the
  composition kernel, and the concrete turn runtime: faults (provider,
  transport, *and* a failing context assembly) surface through `Notify`,
  abort the turn, and leave the session intact; cancelled steps surface
  cleanly; a stream closed without Done or Fault fails loudly.
- **provider/openai**: the SSE streaming adapter over `net/http`: finish-
  marker gated delivery, tool-call accumulation by choice index, tool schemas
  transmitted as JSON objects, non-2xx and truncation surfaced as faults.
- **tool/bash**: `bash(1)` execution with induced-work bounds: 256 KiB
  output cap (truncation named), `WaitDelay` so background children cannot
  hold the turn, and process-group teardown on cancellation.
- **tool/file**: read/write/edit with `FileState` provenance, path-
  normalized drift checking (external modification named and refused;
  ambiguity refused, never guessed at), and a 1 MiB read cap.
- **middleware**: `policy` passthrough (day-one `ContextPolicy`); `perm`
  allow-list, deny-by-default at the boundary with attributed denials;
  `guard.Bound`; every call executes exactly once; the model's re-issuance
  of an identical failing call is counted across turns (name plus args
  digest), refused without executing at the bound, and cleared on success.
- **frontend/cli**: the stdin/stdout REPL over the `Frontend` seam: plain,
  greppable rendering; blank lines no-op; Ctrl-C cancels the turn at its next
  boundary and the session survives.
- **cmd/looper**: the composition root: `looper.New(...)` wires every seam
  in one call; configuration via flags or `LOOPER_*` env, no config files;
  `--version` reports the release.
