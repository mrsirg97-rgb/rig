# rig design

rig is a minimal agent harness, not a framework. It assembles context,
streams the provider, executes tool calls, returns results, and repeats.
Every dependency sits behind a typed seam.

The [core spec](../specs/SPEC_CORE.md) governs this document where they
disagree.

## architecture

```
 user ◄──────────────────────────►  frontend (tui default, cli piped)  seam: Frontend
                         │
                         ▼
                  loop.Run(ctx, kernel)   the concrete turn runtime
                  │  ├─ seam: ContextPolicy ─► policy        (per-turn Assemble)
                  │  └─ seam: Provider      ─► provider/openai (stream out, events in)
                  └─ seam: Tool ─► ToolMiddleware chain
                                   └─ toolset.Resolve → approve.Gate → cutoff
                                       → perm.Plugins → perm.Allowlist
                                       → operator (delegated workers only)
                                       → guard.Bound → guard.Rounds → guard.Cap
                                       → paths (the ~ expansion)
                                       (first-listed = innermost)
                                        │
                                        ▼
             tool/bash · tool/file · tool/view · tool/todo
             · tool/rem · tool/scheduler · tool/delegate · tool/python
             · tool/web · tool/diff · tool/sessions · plugins

 cmd/rig (composition root): wires every seam once at startup; flags and env only.
 store/state: the recorder wraps the Frontend; it sources its rows from the
              loop's events, and -resume rebuilds a session from the state store.
```

### the seams

Five interfaces hold the whole shape of the system:

| seam             | shape (abridged)                                        | role                                        | swapped where          |
|------------------|---------------------------------------------------------|---------------------------------------------|------------------------|
| `core.Provider`  | `Stream(ctx, req Request) (<-chan Event, error)`        | model access; streaming events in           | `kernel.WithProvider`  |
| `core.Tool`      | `Name/Description/Schema/Exec(ctx, args)`               | capability; stdlib-agnostic                 | `kernel.WithTools`     |
| `core.Frontend`  | `Input(ctx) (string, error)`; `Notify(Event)`           | human I/O and the event sink                | `kernel.WithFrontend`  |
| `core.ContextPolicy` | `Assemble(ctx, session) (messages, error)`          | context construction per turn               | `kernel.WithPolicy`    |
| `core.ToolMiddleware`| `Wrap(core.ToolExec) core.ToolExec` (+ optional `TurnStart`, `Guidelines`) | cross-cutting tool-side policy, observed, taught | `kernel.WithMiddleware`|

`ToolMiddleware` is an interface; `ToolMiddlewareFunc` adapts a plain
`func(core.ToolExec) core.ToolExec` to it. Two capabilities ride it by
assertion, not obligation: `TurnStart` lets a middleware observe the turn
boundary (the guard clears its counts there), and `Guidelines` lets it
contribute system-prompt prose (the root collects it before building the
policy). One mechanism, not a second.

The loop names none of the concrete types. `loop.Run(ctx, k *rig.Kernel)`
takes the kernel and resolves seams at runtime, so swapping any
dependency is a change at the composition root and nowhere else.

### the turn, and the queue it runs on

The turn runs on the `evt` engine: one consumer, many producers, work
arriving as closures ordered by priority first and arrival second. The
consumer is the only thing that touches the session — which is what makes
concurrency cheap. There is nothing to lock because nothing races:
parallel tool calls are goroutines that *post their completions*, and
the loop applies those completions in call order. Time leaves the
program the same way locks do: nothing polls, nothing sleeps waiting for
a result; a step that waits on the world spawns and its completion posts
back at the same priority, so the event is the wakeup.

The kernel carries the named priorities (`PriorityInput` 90,
`PriorityStream` 50, `PriorityTool` 50, `PriorityFleet` 30,
`PriorityReview` 10); the loop reads them, the root assigns them. The
fleet's room posts at 30 and the reviewer at 10 — that is the whole
mechanism behind "a worker's message runs in the gaps of a turn and
never ahead of it."

```
 user message ─► Assemble (ContextPolicy) ─► Stream (Provider)
      ▲                                        │
      │                                        ├─ ReasoningDelta / TextDelta ─► Notify
      │                                        ├─ ToolCall ─► ToolStart ─► exec chain ─► ToolResult ─► back into the stream
      │                                        ├─ EmptyTurn ─► resample the identical request (SPEC_EMPTY)
      │                                        ├─ Fault ─► Notify + the turn aborts, session intact
      └────────────────────────────────────────┴─ Done ─► TurnEnd{over|fault|interrupt} ─► next user message
```

Every turn exit inside the run emits `TurnEnd{Reason}` (`over`, `fault`,
`interrupt`) after the turn's last other event; a run-context end emits
none. The loop fans out `TurnStart` to registered observers once per
turn, before the first Assemble.

**Batching.** Tool calls execute in batches: a call the kernel's
`Concurrent` predicate admits runs beside its admitted neighbours,
bounded by the kernel's `Parallel` (default 8); any other call is a
barrier in call order. Results are emitted and appended in the order the
model asked — three parallel reads land as if nothing ran in parallel.
The root admits the observations and the waits (`read`, `view`, `web`,
`decide`) and the hand-off (`delegate`); everything with effects — the
stores (`todo`, `rem`, `sessions`, `scheduler`), the shared kernel
(`python`, plugins), or the disk — stays sequential.

The turn boundary is the runtime's contract, enforced and tested:

- **Fault or transport error** (provider fault, stream error, *and* a
  failing Assemble on a live turn): surfaced through `Frontend.Notify`,
  the turn aborts, the session survives to its last complete message,
  the loop returns to awaiting input. A failing policy cannot take down
  the REPL.
- **Steering**: the turn's cancel is threaded onto the Input ctx as the
  interrupt handle (`core.WithInterrupt`/`InterruptFrom`). A steer reads
  as an interrupt at every seam: at the prompt the loop re-enters
  awaiting input (no Fault); mid-stream it breaks the turn; at the
  pre-stream seams it is `TurnEnd{interrupt}` with no Fault, because the
  model never started. The run re-prompts; the delivered line is the
  next user message.
- **Stream closed without Done or Fault, both contexts alive**: `Run`
  returns a loud error (a provider bug). Silent termination is not an
  option.
- **A panicking tool**: the batch recovers and surfaces the panic as
  that call's tool error. The model reads the panic text; the transcript
  and the process survive. A tool panic is a tool failure, not a loop
  failure.
- **Cancellation** (run-ctx teardown, e.g. Ctrl-C): the session ends
  once the in-flight step unwinds, cleanly (`nil`). The loop only
  observes the context; it never names which step.

### middleware composition

The root's chain — `toolset.Resolve`, `approve.Gate`, `cutoff`,
`perm.Plugins`, `perm.Allowlist`, the `operator` link on a delegated
worker's wire, `guard.Bound`, `guard.Rounds`, `guard.Cap`, `paths`, and
the conditional graph tap and decision links — composes **first-listed
innermost**: a call enters at the outermost link and unwinds inward.

This deliberately inverts the usual `http.Handler` convention, and each
inversion buys something: the bounds sit outside the denials so a denied
call still counts toward the streak; the path expansion happens before
any rule sees a path-shaped argument; the cutoff link refuses a
provider-marked cut call before the operator is asked to approve it; and
the resolve sits innermost of the policy links so a live table's plugin
tool executes under every bound. The `operator` link rides only a
delegated worker's wire: by the time it speaks the tool is already
allowed, and what it refuses is the *verb* — the session's verbs (`todo`
prune/accept/reject/move, `scheduler` remove, `plugin` delete, one list
in the registry) come back as named refusals, never faults.

### guard semantics (`middleware/guard`)

The guard never retries; every tool call executes exactly once. What
`Bound` bounds is the *model's* re-issuance of a failing *tool* — keyed
by tool name, streaked per args, cleared at the turn's start and on
success; the full rule lives in `docs/USAGE.md`, its one home. Beside
the bound, the same package carries the two caps: `Rounds`, the per-turn
cap on tool calls (`rounds`, default 0 = no cap), and `Cap`, the wall
(`resultCap`, default 64 KiB) that bounds every tool result before the
transcript, with the loud `[TRUNCATED]` marker naming the full size.
Denials carry the refusal string and the error, so they are countable —
that attribution is what makes "the refusal is fed back to the model"
and "repetition is bounded" true at the same time.

### session and persistence

The session is an in-memory transcript with a minted, stable id,
persisted by the state store (`specs/SPEC_STATE.md`): SQLite under the
rig home, WAL, with a corruption quarantine. The todo and rem stores are
single SQLite files whose rows carry a project scope — the repo's
identity, shared by worktrees (`store/scope`). The scheduler store is one
global file beside the crontab, and it is the scheduling truth.

The **recorder** is a Frontend wrapper wired at the root, not the loop:
it sources its rows from the loop's events — transcript, tool calls and
their guarded results, usage with the cache fields, reasoning — and
appends per event in short transactions, so a kill leaves every completed
row readable. A `TurnEnd` discards the unlanded partial of a torn turn,
and each turn boundary upserts the file provenance. `rig --resume <id>`
projects the session back from the store in one read-only transaction:
transcript in seq order, assistant reasoning and calls in row order,
landed results, dangling calls kept, files rebuilt — and the recorder
adopts the existing row, so one identity serves todo's claims and rem's
sources. The JSON Save/Load pair is the test seam now, not a persistence
path.

Writes are validated before acting: the file tool checks disk state
against the last-read state and names the drift; ambiguity ("occurs N
times") is named and the write is refused. Provenance is canonicalized
through `filepath.Abs`, so `a.go` and `./a.go` are one key.

### induced-work bounds

One table, because every input from the model is untrusted work:

| where            | bound                                                          |
|------------------|----------------------------------------------------------------|
| bash output      | 256 KiB, naming the truncation                                 |
| bash lifecycle   | `WaitDelay` (background children can't hold the turn), process-group teardown on cancellation |
| file read        | 1 MiB, naming the truncation; streamed — a huge file is never materialised |
| every tool result | `resultCap` (default 64 KiB): head and tail with the loud `[TRUNCATED]` marker naming the full size, before the transcript |
| a turn's calls   | `rounds` (default 0 = no cap): the n+1th call is refused without executing |

## the decision pipeline

`decision/` is a stdlib-only leaf beside `pathguard` (SPEC_DECISION). The
rule that names the shape: **"an answer is a proposal an LLM reviews,
never an action."**

A `Question` is typed — `choice`, `score`, `binary`; an `Answer` carries
the value, the confidence, and who gave it. The gates hold a `Recorder`
and record their finals — site, state, question, answer, decider, scope,
session — into one SQLite store under the rig home, scoped like todo.
Recording never changes a decision, and a store error never fails a call
(the wired recorder swallows store errors, and says so in the room when
the root gives it a voice).

With `decisionUrl` set, the model runs as a sidecar over one URL, and
three things start happening. Every bash call gains a pending risk row.
The `decide` tool joins the live table, so the model hands over its
sorting — one typed question over many items instead of reading them
through context. And the reviewer settles pending rows in batches
(`reviewBatch`), posting its bite at `PriorityReview`: it starts only
when nothing else is queued, so labeling gets the idle time and nothing
waits on the labels.

The pipeline curates its own training set. `train/` is a discovery zone
beside `plugins/`: one trainer, one Python file with a checked contract
(`train(rows_path, out_dir)`, `evaluate(checkpoint, rows_path)`), run
headless through `trainPython` — never inside a turn. The run exports
the settled rows (an approved row takes the proposer's answer, a denied
row the reviewer's correction), splits them 80/20 stratified, and scores
candidate, incumbent, and the constant baseline on the same held-out
rows. Promotion is a measured win: the candidate must beat the constant
**and** the incumbent on every question — a tie is not a beat — and it
lands as a printed command: the one `Environment=` line in the unit
`decisionUnit` names. Rig restarts no service it does not own.

## the fleet

`broadcast/` is the fleet's message seam (SPEC_SWARM): a `Room` of
`Member`s over a `Transport`, exchanging `Message`s that carry origin,
health, and a `core.Event`. The transport is the loop itself
(`NewLoopTransport(id, engine, priority)`): a send posts one closure at
the room's priority and acks on the post — the queue is the durability —
and a second heartbeat before the first ran is not posted at all. `Say`
is the one voice every background subsystem notices with; the kernel
names the members (`MemberFrontend`, `MemberDelegate`, `MemberGraph`,
`MemberDecision`), and the swarm's supervisor registers as one.

Because the room posts at `PriorityFleet` 30, below the turn's own
events, a worker's message runs in the gaps of a turn and never ahead of
it. Starvation is the documented property, not a bug to chase: a busy
session delays the fleet's chatter and loses nothing, because the stores
hold the facts. That same ordering — priority, not source — is why the
swarm needs no machinery of its own: workers, reviewers, and the
operator's input are just producers on one queue.

Presence is display, not state: the swarm band under the TUI's status
row carries the live counts per role (`workers 2 · +3 ✓5 ✕1 · w2 t388
12s`, `reviewer 1 · ⧗1 ✓1 ✕0`), and zero rows when nothing runs; a
delegated batch takes two rows of its own — the count and elapsed over
the most recent call of any of its workers — both publishers riding
throttled `core.SwarmStatus` snapshots.

The return is the turn's front door. `core.WorkerDone` carries the
worker's message, exit, duration, session, and log; the frontend's inbox
drains it at the prompt as the head of the next user message — returns
first, then what the operator typed — and a drain with no live turn
starts one. A worker belongs to the session, not the turn that spawned
it; the interrupt gesture with no turn live (esc on an empty prompt, the
dashboard's stop button) stops every running worker.

## extending

The design test: adding a tool, a provider, a policy, a frontend, or a
middleware is **one file plus one registration line** at the composition
root, and the loop never names a concrete type.

1. **A tool**: implement `core.Tool` in `tool/<name>/<name>.go` (stdlib-
   agnostic exec; loud at the boundary), register in `cmd/rig`'s
   `WithTools(...)`, and in the default allow-list if it should run.
2. **A provider**: implement `core.Provider` (every stream ends in Done
   or Fault, ctx teardown excepted); `WithProvider(...)`.
3. **A context policy**: implement `core.ContextPolicy`; `WithPolicy(...)`.
   The compaction policy (`policy/compact`, per-model trigger) wraps the
   passthrough, and `policy/effort` decorates the provider with the
   reasoning dial. A policy may rewrite the session transcript — the one
   named mutation the seam carries.
4. **A frontend**: implement `core.Frontend`'s two methods;
   `WithFrontend(...)`. A TUI can sit beside the CLI.
5. **A tool middleware**: implement `core.ToolMiddleware` (or adapt a
   plain function with `ToolMiddlewareFunc`), registered at its intended
   position — first-listed innermost. Optionally carry the assertion-
   checked `TurnStart` and `Guidelines` capabilities.
6. **A command**: implement `core.Command` (a user verb, dispatched by
   the Frontend before the loop sees the line); `WithCommands(...)`. One
   file in `command/` plus one registration line.
7. **A plugin**: no Go at all. One Python file under the rig home's
   `plugins/`, one tool, discovered at startup, reached through the one
   `plugin` door; the operator installs it by approve. How:
   `docs/PLUGINS.md`.

## constraints

- **Stdlib-only core.** `core/`, `loop/`, and `evt/` carry no
  dependencies; a leaf dependency is justified in its spec first. The
  store's one dependency is `modernc.org/sqlite` (pure-Go,
  `specs/SPEC_STATE.md`); five more sit at the edges: `golang.org/x/image`
  at `tool/view`, `golang.org/x/sys` at `tool/bash` (and a `frontend/tui`
  pty test), `golang.org/x/term` and `mattn/go-runewidth` at
  `frontend/tui`, and `golang.org/x/crypto` at `cmd/rig`'s updater. Every
  other provider, tool, middleware, and frontend is stdlib.
- **Closed, typed seams.** The Go graph is compile-time explicit: no
  reflection, nothing loaded or discovered into the loop at runtime. The
  two discovery zones — `plugins/` and `train/` — are the operator's
  files under the rig home, checked against a named contract before any
  of their code runs, and the wire reaches them through the one `plugin`
  door. Unknown at a seam is a loud error, never a guess.
- **One process.** A modular monolith; cross-process only when demanded.
- **Default-deny at the boundary.** Tools execute only through the
  registered middleware chain; the root's default allow-list exists
  because a default-deny CLI would ship a dead agent, and narrowing is
  the operator's act.
- **The loop never retries.** Repetition is the model's act; the loop's
  duty is faithful surfacing plus the bound.

## non-goals

Explicitly out, per the spec: multi-provider negotiation and
conversational features the model does not ask for. Sandboxing and
plugin systems were non-goals at the day-one spec and now ship
(`docs/SETUP.md`): the worker jail (`specs/SPEC_SANDBOX.md`) and the
Python plugins (`specs/SPEC_PLUGINS.md`).
