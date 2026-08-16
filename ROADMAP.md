# looper: roadmap

Ten deliverables, in execution order (reordered 2026-08-16: the leaf ports
run before the loop change, so the runtime hardening gets the slowest
spec). Each is one feature, one PR stack, and one spec first: `specs/SPEC_{FEATURE}.md`, in the format of `specs/SPEC_CORE.md`
(goals, non-goals, layout, interfaces, decisions, testing, scope), written
and agreed before code. `SPEC_CORE.md` itself changes only when a
deliverable changes core/ or the loop, and that change is named in the PR.
CLI-only through 9: the frontend is last because a TUI on an unfinished
runtime is a TUI you rewrite. Harden the backend, then draw it.

The runtime is 0.x until the end of 9 and v1.0.0 after it: seams, events,
commands, and stores complete, and everything that could ask for a loop
change has already asked. From then on core/ and loop/ are open to
extension and closed to modification (models are sensitive to the loop; a
frozen loop is a controlled variable). 10 is the first consumer of the
frozen runtime, in its own module on its own version line, and is the
freeze's first test: if the TUI needs loop.go, 1.0 was premature.

The reference for taste is `~/Projects/pane` (README.md, AGENTS.template.md,
the per-tool specs under docs/ and at the root). The reference for Go style
stays `~/Projects/lift/engine`. Same design, different runtime: a port keeps
pane's tool surface, schema, error voice, and promptGuidelines; it changes
only what the language and the seams force.

Standing rules across all ten:

- The design test holds: a feature is leaf packages plus registration lines
  in the root. A loop change is named as such in the PR and made once.
- Named-case tests before implementation. Boundary cases by name.
- Errors teach: every refusal names what was wrong and what would be right.
- Loud truncation, fail closed, no silent retries. Unchanged from `specs/SPEC_CORE.md`.
- Deps: stdlib only in core/ and loop/. A leaf dep is justified in its
  feature spec first. Storage is decided once, off-roadmap, in
  `specs/SPEC_STATE.md`; deliverables 2 through 4 were built on it.

## 1. tool/fs: grep, find, ls
> done

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

## 2. tool/todo: the concurrent job queue
> done

Deliverable: pane's todo, same design, ported. Reference: `pane/TODO_SPEC.md`,
`pane/docs/TASK_TREE_SPEC.md`, `pane/extensions/todo.ts`, `todo.types.ts`.

- Storage is `specs/SPEC_STATE.md`: lift-generated stores over
  `database/sql` + `modernc.org/sqlite`, the `sqlx` transaction seam on the
  ctx, one transaction per tool call. This deliverable is the first tool
  adapter over that substrate; it does not re-decide storage.
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

## 3. tool/rem: memory
> done

Deliverable: pane's rem, ported. Reference: `pane/docs/REM_SPEC.md`,
`pane/extensions/rem.ts`, `rem.types.ts`, `sqlite.ts`.

- learn (idempotent, scoped global or per-project), recall (fuzzy/semantic:
  FTS5 plus trigram, two arms, reciprocal rank fusion, project-first
  global-fill), reflect (distilled logs, auto-parks compaction summaries),
  prune (strength decay, remove/reduce).
- Hybrid single-file store topology, corruption quarantined aside and never
  deleted, effective strength at recall with a checkpointed pass at prune.
- Uses `store/` from SPEC_STATE; FTS5 and trigram bookkeeping pinned as
  in the spec.

Done when: recall quality on pane's fixture set matches, and quarantine is a
named test.

## 4. tool/scheduler: background jobs
> done

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
- The job runtime is looper itself in `-p` mode (landed). Reports
  back through rem (landed).

Done when: the scheduler runner tests have Go equivalents by name, and a
job fires from a cold shell with only cron-env.

## 5. tool/python: the persistent kernel
> done

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

## 6. tool/web: search and fetch
> done

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

## 7. runtime hardening: the plumbing pane needs

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
- Session: `core.Session.ID` (minted, stable, attributed to by todo's claim
  semantics and rem's source) landed with SPEC_STATE, and the state store's
  recorder is the persistence; JSON Save/Load stays a test seam only. What
  remains here is the resume path: rebuild `[]core.Message` from the state
  rows (the projection SPEC_STATE names).
- Extension points for the agent side: a way for a leaf to contribute
  system-prompt guidelines (pane's promptGuidelines) and to observe turn
  boundaries (`turn_start`, `tool_result`) the way pane's tool-retry-guard
  does. Concretely: a small `core.Hooks` or the middleware seam widened;
  decide in the spec, keep it one mechanism.
- Guard alignment: bound keyed by tool name, cleared per turn, matching
  pane's retry guard semantics (a model failing `edit` with drifting args is
  the real failure mode).
- Root: `-p <prompt>` one-shot mode landed with the scheduler
  (`frontend/oneshot`, fault propagates to exit); nothing left here but to
  keep it byte-identical while the events above are added.

Done when: `specs/SPEC_CORE.md` documents each new event and seam, every named fault and
cancellation case still passes, and a Frontend can render pane's status
glyphs from events alone with no access to loop internals.

## 8. policy/compact: compaction as a ContextPolicy

Deliverable: the first real `ContextPolicy` beyond passthrough. Today there
is none: passthrough replays the whole transcript, and a worker on a 64k
window simply hits the wall. This is the load-bearing seam for local models
and the first hard problem the runtime meets; it lands before any tool that
would hide it.

- One package, `policy/compact`, implementing `core.ContextPolicy`. Wraps
  passthrough: below the trigger it is passthrough; at the trigger it
  summarizes the older transcript through the same `core.Provider` and
  returns system + summary + kept tail. The loop is untouched; the policy
  owns when and how.
- The trigger is window-relative and per-model: compact when
  `estimated(context) > window - reserve`, where `window` and `reserve` are
  the model's, not a global. This is the pi bug seen on 2026-08-15 (global
  reserve larger than the workers' window fires compaction every turn,
  including headless `-p`); looper does not inherit it. Requires a models
  table at the root (id, window, maxTokens, reserve, keepRecent), which the
  `models` command in 9 reads.
- Keep-recent is a token budget, cut at a message boundary, never inside a
  tool-call/tool-result pair. The summary request's `max_tokens` is clamped
  to what the window actually has left.
- Estimation is stdlib: bytes/4 with the provider's last reported prompt
  usage as the correction when present. Named as approximate in the spec.
- The compacted prefix invalidates the server-side prompt cache; the policy
  logs the reprocess cost (tokens dropped, tokens kept) through `Notify` as
  a named event so the operator can see it in a footer later. On DeltaNet
  hybrids the rollback is bounded by the server's checkpoints, not by
  looper; note it, do not design around it.
- Overflow recovery: a provider fault that names context length triggers
  one compact-and-retry, once, then surfaces. Never a silent loop.
- The compaction summary is handed to rem's `AutoReflect` (already in
  `store/rem`, unwired): a low-importance reflection scoped to the cwd,
  deduped by content, fire-and-forget. pane's session_compact hook, here.
- Headless: `-p` workers get the same policy with the same per-model
  numbers; a job that would compact every turn is a config error surfaced
  at start, not a slow death.
- Tests: scripted provider, fixture transcripts at known sizes; trigger
  math by name for a 64k model and a 262k model with one shared config;
  pair-boundary cut; overflow-recovery once; the summary call's clamped
  max_tokens.

Done when: a scripted 64k-window run past the trigger compacts exactly
when the math says, keeps the tail intact, and passthrough behaviour below
the trigger is byte-identical.

## 9. user commands and tools

Deliverable: a user-facing command seam and the first commands, so the
human has verbs of their own, not only prompts. In the CLI they are typed
lines with a prefix; in the TUI (10) they become `/` commands over the same
seam. Same glass, both sides: where a command has an agent-side tool, it is
the same tool.

- One mechanism: `core.Command{Name, Description, Run(ctx, args, k) (string,
  error)}` registered at the root like tools (`WithCommands(...)`). The
  Frontend recognizes the prefix and dispatches before `Input` returns to the
  loop; the loop never sees a command. This is Frontend-side plumbing, not a
  loop change; if it needs the loop, 2 was incomplete.
- Runtime commands: `compact` (force the policy from 8 now, report dropped
  and kept), `new` (close the session, mint a fresh id, keep the process),
  `sessions` (list, show, resume from the state store, which exists), `models`
  (list the models table from 8, switch the active model for the next turn,
  show window/reserve/effort), `steer` (queue a message for delivery at the
  next boundary of the running turn, the deliverable 7 path made a verb).
- Tool-backed commands: `todo` and `scheduler` are the same tools the agent
  gets, exposed to the human on the same seam. The user reads and edits the
  queue the model works from; the user schedules the job the model would
  have. No parallel implementation, no goal or loop verb: the queue is the
  plan. Both tools have landed (SPEC_STATE), so they attach in this
  deliverable alongside the runtime commands.
- Rendering stays plain in the CLI (greppable lines); the render kit is 10.
- Tests: dispatch by prefix with the loop never invoked; each runtime
  command by name against a scripted kernel; `steer` delivered at the
  boundary and not before.

Done when: `compact`, `new`, `sessions`, `models`, `steer` work in the CLI
over `WithCommands`, and adding a command is one file plus one registration
line.

## 10. frontend/tui

Deliverable: pane's glass, on the Frontend seam. Reference: `pane/README.md`,
`pane/extensions/builtin-restyle.ts`, `footer.ts`, `input.ts`,
`_render-kit.mjs`, `themes/`.

- One visual language, both directions: `○ ◐ ● ✕` on tool rows, the todo
  queue, and the prompt. `● tool · detail` headers, head/tail previews with
  expand hints, durations, write previews content, edit previews its diff.
- Footer: throughput and cache above the input, model · thinking · context
  (colored past 70/90%) below. Built for phone width.
- Prompt glyph carries state: `❯` your turn, `◐` agent streaming, typing
  queues or steers via the deliverable 7 path and the `steer` command of 9.
- Terminal handling in stdlib or a single justified leaf dep for raw mode;
  the render kit ports as plain Go.

Done when: the TUI is a Frontend registration and nothing else, and the loop
is byte-identical to the end of deliverable 7. If the TUI needs a loop
change, deliverable 7 or 9 was incomplete and is reopened first.
