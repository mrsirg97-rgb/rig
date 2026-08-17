# Changelog

## [Unreleased]

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
  prompt. The runtime is the 1.0 freeze (Version 1.0.0); the loop is
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
