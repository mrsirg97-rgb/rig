# looper: roadmap

Eight deliverables, in order. Each is one feature, one PR stack, and one
spec first: `specs/SPEC_{FEATURE}.md`, in the format of `specs/SPEC_CORE.md`
(goals, non-goals, layout, interfaces, decisions, testing, scope), written
and agreed before code. `SPEC_CORE.md` itself changes only when a
deliverable changes core/ or the loop, and that change is named in the PR.
CLI-only through 7: the frontend is last because a TUI on an unfinished
runtime is a TUI you rewrite. Harden the backend, then draw it.

The reference for taste is `~/Projects/pane` (README.md, AGENTS.template.md,
the per-tool specs under docs/ and at the root). The reference for Go style
stays `~/Projects/lift/engine`. Same design, different runtime: a port keeps
pane's tool surface, schema, error voice, and promptGuidelines; it changes
only what the language and the seams force.

Standing rules across all eight:

- The design test holds: a feature is leaf packages plus registration lines
  in the root. A loop change is named as such in the PR and made once.
- Named-case tests before implementation. Boundary cases by name.
- Errors teach: every refusal names what was wrong and what would be right.
- Loud truncation, fail closed, no silent retries. Unchanged from `specs/SPEC_CORE.md`.
- Deps: stdlib only in core/ and loop/. A leaf dep is justified in its
  feature spec first. Deliverable 3 makes the one dep decision (the sqlite
  driver) that 3 through 7 inherit; it is not re-decided per feature.

## 1. tool/fs: grep, find, ls

Deliverable: three named tools in the default set, next to bash, read, write,
edit. Named tools with small schemas are what a local model reaches for;
`bash grep` is where it fumbles quoting.

- One package, `tool/fs`, three tools: `ls` (entries with kind and size,
  one level, optional hidden), `find` (glob by name under a root, `**` ok),
  `grep` (regexp in content under a root, `path:line: text`, optional glob
  filter). Stdlib only: `filepath.WalkDir`, `regexp`, `path/filepath.Match`.
- Skip `.git` and binary content. Hard result caps like `readCap`, loud
  `[truncated: N of M]` markers, results sorted for stable output.
- Add `Description() string` to `core.Tool` and set it on all seven tools,
  terse, in pane's promptGuidelines voice. This is the one seam change in
  this deliverable; it is what makes named tools worth having.
- Root: three registration lines and the allow-list default grows.
- Tests: real filesystem in `t.TempDir()`; caps and truncation by name; the
  bash tool is not used to implement any of it.

Done when: the loop is byte-identical, `Description` reaches the wire as
`function.description`, and the four original tools plus three new ones are
the default set.

## 2. runtime hardening: the plumbing pane needs

Deliverable: the seams and events required for everything after this to be
leaf work. No user-visible feature; this is the loop change made once,
deliberately, while the runtime is still the focus.

- Tool-exec events from the loop: `ToolStart{Call}` and `ToolResult{ID,
  Content, Err, Duration}` in the Event vocabulary, forwarded to
  `Frontend.Notify` like the model's stream. Without these the Frontend is
  blind to tool execution and pane's `● tool · detail` rows cannot exist.
- Reasoning: `ReasoningDelta` event and `Message.Reasoning` round-tripped by
  the provider (llama.cpp `reasoning_content`). Interleaved-thinking tool
  turns lose their reasoning today; the user sees silence during long
  thinks.
- Usage: cache-read and cache-write tokens, and per-turn totals surfaced so a
  footer can show throughput and hit rate.
- Steering: an input path during a turn. Interrupt is `cancel()` plus
  re-entering awaiting_input; a queued message is delivered on the next
  Input. No mailbox until this needs one; the CLI gets a minimal version so
  the shape is proven before the TUI inherits it.
- Session: Save/Load wired at the root (path under `os.UserConfigDir()`),
  session-attributed events for tools that need ownership (todo's claim
  semantics), and a stable session id.
- Extension points for the agent side: a way for a leaf to contribute
  system-prompt guidelines (pane's promptGuidelines) and to observe turn
  boundaries (`turn_start`, `tool_result`) the way pane's tool-retry-guard
  does. Concretely: a small `core.Hooks` or the middleware seam widened;
  decide in the spec, keep it one mechanism.
- Guard alignment: bound keyed by tool name, cleared per turn, matching
  pane's retry guard semantics (a model failing `edit` with drifting args is
  the real failure mode).
- Root: `-p <prompt>` one-shot mode (a Frontend that reads one prompt and
  exits). Scheduler needs it, and it proves the seam.

Done when: `specs/SPEC_CORE.md` documents each new event and seam, every named fault and
cancellation case still passes, and a Frontend can render pane's status
glyphs from events alone with no access to loop internals.

## 3. tool/todo: the concurrent job queue

Deliverable: pane's todo, same design, ported. Reference: `pane/TODO_SPEC.md`,
`pane/docs/TASK_TREE_SPEC.md`, `pane/extensions/todo.ts`, `todo.types.ts`.

- The sqlite decision, made once here, the way lift already made it:
  `database/sql` is the seam (stdlib), `modernc.org/sqlite` is the driver
  (pure Go, no cgo, static binary kept, FTS5 built in for rem; the same
  driver lift vendors). `store/` mirrors `~/Projects/lift/sqlx`: a
  `DB{*sql.DB}` wrapper, `Tx`/`TxReadOnly` opening the serializable
  transaction and landing it on the ctx under a typed key, `TxFrom` failing
  closed on an unbound request. Tool packages read `*sql.Tx` off the ctx and
  never import the driver; only `store/` does, registered once at the root.
  Justified in `specs/SPEC_TODO.md`.
- Enforced FSM (pending -> in_progress -> done | failed; failed -> retry),
  minted ids, several tasks in flight, batched transitions serialized against
  fresh state, illegal transitions returning errors that teach the protocol.
- dependsOn task trees: completion gated by dependencies, cycles refused,
  blocked tasks skipped by next. move via minted-position events.
- Session-attributed events with claim semantics: start claims, foreign
  complete refuses, fail frees. Event log with idempotent create, replayable
  history, compaction past 1000 events (snapshot, epoch reset), corruption
  fails closed.
- Tool surface and promptGuidelines identical to pane's.

Done when: the pane test suite's cases have Go equivalents by name and pass.

## 4. tool/python: the persistent kernel

Deliverable: pane's python, ported. Reference: `pane/extensions/python-kernel.ts`,
`python-kernel.types.ts`.

- One persistent IPython kernel per session; state survives across calls.
- Timeout kills the whole kernel and says so; an unexpected death is
  announced on the next call with exit description and stderr tail.
- Process-group discipline from tool/bash (Setpgid, WaitDelay, group kill).
- Stdlib only: `os/exec` and the kernel wire protocol over stdio, no
  third-party client.

Done when: state persistence, timeout semantics, and death announcement are
named tests against a real kernel.

## 5. tool/rem: memory

Deliverable: pane's rem, ported. Reference: `pane/docs/REM_SPEC.md`,
`pane/extensions/rem.ts`, `rem.types.ts`, `sqlite.ts`.

- learn (idempotent, scoped global or per-project), recall (fuzzy/semantic:
  FTS5 plus trigram, two arms, reciprocal rank fusion, project-first
  global-fill), reflect (distilled logs, auto-parks compaction summaries),
  prune (strength decay, remove/reduce).
- Hybrid single-file store topology, corruption quarantined aside and never
  deleted, effective strength at recall with a checkpointed pass at prune.
- Uses `store/` from deliverable 3; FTS5 and trigram bookkeeping pinned as
  in the spec.

Done when: recall quality on pane's fixture set matches, and quarantine is a
named test.

## 6. tool/scheduler: background jobs

Deliverable: pane's scheduler, ported. Reference: `pane/docs/SCHEDULER_SPEC.md`,
`pane/extensions/scheduler/` (core, store, crontab, cron, runner), and the
post-merge corrections (job cwd independent of the store key, names unique
among live jobs only, never kill ambiguous processes).

- Crontab is the scheduling truth: tagged lines, surgical rewrites, foreign
  lines byte-identical. Two stores (global, per-cwd) with todo's event-log
  discipline and tombstones.
- Runner: flock per key, busy policy against llama-swap `/running` with
  alias normalization, own-slot-loaded short-circuit, fail closed when
  unreachable, run records and log rotation.
- The job runtime is looper itself in `-p` mode from deliverable 2. Reports
  back through rem from deliverable 5.

Done when: the scheduler runner tests have Go equivalents by name, and a
job fires from a cold shell with only cron-env.

## 7. tool/web: search and fetch

Deliverable: pane's web_search and web_fetch, ported. Reference:
`pane/extensions/web-search.ts`, `web-fetch.ts`, `web-fetch.types.ts`.

- SearXNG search over `net/http`. Guarded fetch: DNS refusal of private
  address space with readable errors, redirects re-checked hop by hop, byte
  and char caps with loud truncation markers, optional egress proxy.
- Extraction: pane uses trafilatura; the port either shells to it (a
  documented external, like bash and the python kernel) or ships a stdlib
  readability pass. Decide in the spec; do not add a Go HTML dep for it.

Done when: private-space refusal, redirect re-check, and cap markers are
named tests against `httptest` servers.

## 8. frontend/tui

Deliverable: pane's glass, on the Frontend seam. Reference: `pane/README.md`,
`pane/extensions/builtin-restyle.ts`, `footer.ts`, `input.ts`,
`_render-kit.mjs`, `themes/`.

- One visual language, both directions: `○ ◐ ● ✕` on tool rows, the todo
  queue, and the prompt. `● tool · detail` headers, head/tail previews with
  expand hints, durations, write previews content, edit previews its diff.
- Footer: throughput and cache above the input, model · thinking · context
  (colored past 70/90%) below. Built for phone width.
- Prompt glyph carries state: `❯` your turn, `◐` agent streaming, typing
  queues or steers via the deliverable 2 path.
- Terminal handling in stdlib or a single justified leaf dep for raw mode;
  the render kit ports as plain Go.

Done when: the TUI is a Frontend registration and nothing else, and the loop
is byte-identical to the end of deliverable 2. If the TUI needs a loop
change, deliverable 2 was incomplete and is reopened first.
