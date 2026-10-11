# rig: async tool calls (a bound cooks in the background)

The exec family reads four time stories off one board: bash is
synchronous and unbounded; python and web carry a `timeoutMs` that caps
a synchronous call; delegate has no clock at all (2.12.7); scheduler
caps its fires in minutes. The model guesses anyway. In the recorded
session stores the guess landed eighteen times on bash as `timeout` or
`timeoutMs` — every one refused as an unknown field, every one
recovered by a naked retry, two by re-sending the other spelling. The
values are round minutes converted to milliseconds under both
spellings, so the confusion was never units: the board never said what
a bound *means*.

This spec gives the board one rule.

> **A bound is a classification.** A call that carries `timeoutMs`
> says *I cannot promise when this finishes* — so it does not block
> the turn: it is bounded, it cooks in the background, and its result
> arrives on a later turn. A call that omits it says *this is fast* —
> it runs in-turn under the tool's own ceiling. Specifying the bound
> is the caller's promise: *I expect this around N ms; if I am wrong,
> kill it at N ms; either way, do not make me wait for it.*

The mechanism is the delegate pattern generalized: the delegate stops
being "the tool that spawns model workers" and becomes the primitive
every bounded call rides — a worker, model or deterministic, started
now; a report fed back later. The promise is about the bound, not the
delivery latency: even a fetch that finishes in 200 ms delivers on a
later turn, because a live turn is never interrupted (SPEC_DELEGATE 8).

## what it is not (named)

- **Not a distributed work queue.** SPEC_DELEGATE's non-goal stands:
  no pull, no standing pool, no getter — the return is pushed, the
  hand-off shape is the only shape.
- **Not concurrency for the kernel.** An async python call does not
  run beside the session's kernel; it queues on it. Coherence over
  parallelism: the kernel is the session's shared state, and two
  writers racing it is a correctness hazard, not a speedup.
- **Not a clock on model workers.** 2.12.7 stands: a clock on a queued
  model worker measures the queue. The bound belongs to deterministic
  work, whose clock measures the work, because no model-server queue
  is in its path.
- **Not new process topology.** Every worker — model, command, kernel,
  fetch — rides the seams that exist: the runner's spawn, the state
  store, the one scheduler store's record, the frontends' inbox fold.

## goals

- One rule, taught by the wire: `timeoutMs` on any tool that carries
  it means bound + background + notify. One name, one unit,
  milliseconds, everywhere.
- One primitive: the delegate's `DelegateStart`/`Wait` seam
  (SPEC_DELEGATE 8) behind named interfaces, with worker kinds — the
  model worker that exists, and the deterministic worker this spec
  adds — plus in-process kinds for python and web.
- bash joins the time-carrying set as pure sugar: `timeoutMs` on bash
  is a delegation of the command line, no new exec path in bash.
- scheduler's field joins the same vocabulary: `timeoutMs`, ms in,
  rounded up to whole minutes at the input boundary, the store keeps
  minutes, nothing migrates.
- Reports ride the existing `core.WorkerDone` fold — the inbox, the
  boundary, the batch block — so the async lane costs no new turn
  machinery.

## non-goals

- No parallel kernel execution; no second kernel.
- No clock on model workers; `DelegateInput.Timeout` stays the
  scheduler's own callers' spend bound.
- No cron semantics on the primitive: the primitive is fired by a
  call, never by a schedule; the scheduler stays its own client.
- No polling, no getter, no `status` tool: the report is pushed; the
  record and the log are the operator's audit.
- No new output caps: the 256 KiB cap and its `[TRUNCATED]` marker are
  the one cap, everywhere.

## decisions

### 1. The rule and the field

`timeoutMs` is one field with one meaning on every tool that carries
it. The time-carrying set is the exec family: bash, python, web,
delegate, scheduler. The file family (read, write, edit, todo, rem)
carries no time and refuses the field as an unknown field, loudly, as
today.

- **Present**: the call answers at once with one hand-back line naming
  the work, and the result arrives on a later turn, killed at the
  bound. The hand-back names enough to associate the return with the
  call: the worker, its run id, its log.
- **Absent**: sync in-turn under the tool's own ceiling, exactly as
  today. The ceiling is the tool's standing default (web 30s, python
  120s); it is a ceiling, not a promise.
- Bounds are positive, minimum 1,000 ms. `0` refuses by name
  (`timeoutMs: 0 means nothing; omit the field or promise a real
  bound`). No `-1` sentinel anywhere in the exec family: a call-scoped
  field is omitted, not reset; the one stored knob keeps its reset and
  it is documented in its own decision.

The description sentence, one shape, per tool:

    when present, the call answers at once and the result arrives on a
    later turn, killed at the bound; absent, runs in-turn under the
    default ceiling.

scheduler's variant swaps the async sentence for the rounding one
(decision 6).

### 2. The primitive: one seam, worker kinds behind it

The delegate seam split in 2.14.0 — `DelegateStart` does everything
that can refuse, `Wait` collects the outcome — is the primitive. The
spec promotes it to a named interface set in the broadcast style
(`Member`, `Message`, `Room`, `Transport` are the precedent), so tools
depend on the seam, never on the store:

```go
type Worker interface {
    Kind() Kind // model | command | kernel | fetch
    Run(ctx context.Context, bound time.Duration, report func(Report))
}

type Report struct {
    Kind      Kind
    RunID     string
    OK        bool
    Exit      int         // command workers
    Output    string      // capped, the loud marker, all kinds
    Fault     string      // why it did not finish
    Duration  time.Duration
    SessionID string      // model workers; resumable transcripts
    Log       string      // the run's log path
}
```

`core.WorkerDone` carries a `Report` to the frontends' inbox; the fold
(`WorkerBlock`) is unchanged. The runner keeps its spawn, its jail
(SPEC_SANDBOX 1, verbatim), its record (the ad-hoc `jN` run, decision
4 of SPEC_DELEGATE, verbatim), and its pipe. The interface set lands in
a leaf package; the root wires it once; the loop never names a
concrete kind.

### 3. The command worker: deterministic bash

`delegate` gains `command`, exclusive with `task`:

```json
{
  "type": "object",
  "properties": {
    "task":      {"type": "string"},
    "command":   {"type": "string"},
    "workspace": {"type": "string"},
    "model":     {"type": "string"},
    "timeoutMs": {"type": "integer", "minimum": 1000,
                  "maximum": 86400000}
  },
  "required": []
}
```

- `task` xor `command`: both set, or neither, refuses by name.
- `command` runs one bash line through the same spawn seam — `execwrap`
  argv, the jail as configured, pathguard, the workspace resolved as
  bash resolves it. No model, no tools, no `-allow`, no
  model-server queue: the worker is `bash -c` on the runner's spawn.
- `model` with `command` refuses (`a command worker has no model`).
- `timeoutMs` with `command` is the kill bound, ms-native, capped at
  the seam's 24h (86,400,000 ms — the same bound the scheduler's
  1440 minutes makes, one ceiling, two vocabularies, converged). At the
  bound the process tree is killed the spawn's way (Setpgid, the
  process-group SIGKILL), and the report carries the partial output
  and a fault that names the bound:
  `command exceeded timeoutMs=300000 (partial output attached)`.
- `timeoutMs` with `task` refuses, by name, pointing at 2.12.7:
  `delegate: a model worker has no clock (2.12.7) — command workers
  take timeoutMs`. The recorded evidence stands: a clock on a queued
  model worker measures the queue. A command worker's clock measures
  the work, because nothing queues on its path.
- The worker inherits the spawning session's environment; the
  delegation marker (`RIG_DELEGATE`) is set, one rule everywhere.
- Cost: no model fires. The deterministic lane is free.

### 4. bash joins as sugar

bash's schema gains `timeoutMs`, bounds per decision 1. Presence is
sugar, no new exec path: the tool submits the command line as a
command-worker delegation bound by the field, returns the hand-back
line, and the report arrives on a later turn. The tool's own exec path
— argv, workspace, capture, the `(workspace ...)` decoration — is
untouched; the sugar routes through the primitive before `Run` is
reached. The approval gate classifies a delegating bash call as
mutating, because it is one: it spawns a worker and writes a record.

### 5. python and web: the in-process kinds

`timeoutMs` present promotes the call to the background; absent is
today's behavior verbatim.

- **python (kernel kind)**: the call enqueues on the session's kernel
  and answers at once. The kernel executes the queued cell when its
  turn comes — submission order, coherence over parallelism — and the
  result reports when done. The kernel is the worker: no new process,
  no second interpreter, shared state untouched. A dead session takes
  the kernel and its pending results with it; nothing injects, the
  fault is the answer. The standing sync ceiling (120 s) governs the
  absent case, unchanged.
- **web (fetch kind)**: the fetch runs out-of-band behind the `Do`
  seam that exists for tests; the call answers at once; the report
  carries the fetched body, capped. The bound kills a slow fetch at
  expiry, naming itself. The absent case is the standing 30 s ceiling,
  unchanged.
- Both kinds report through the same `Report` envelope and the same
  inbox fold as the spawned kinds. Reports arrive in completion order;
  the hand-back line carries the run id the return will name.

### 6. scheduler joins the vocabulary

`timeout` becomes `timeoutMs` on the tool's schema. Input is
milliseconds, normalized at the tool's input boundary: the
milliseconds divide by 60,000 and round up — `59_999` and `60_000`
both land on 1, `61_000` on 2 — and the normalized minutes are
validated against the standing bounds (`-1` reset, ceiling 1,440;
`86_400_001` refuses). The store keeps minutes; rows, crontab, and
compaction migrate nothing; the operator's `/scheduler` line keeps
minutes. The description carries the rounding sentence, not the async
one: fires are already out-of-band, and the field is their per-fire
cap, not a mode switch.

### 7. The fold, unchanged

`core.WorkerDone` gains the `Report`; `WorkerBlock` renders one block
per kind with the same trailer grammar (`exit N · duration · session ·
log`), command workers naming the run id instead of a session. The
inbox keeps 2.14.0's shape: returns append in arrival order, a live
turn is never interrupted, a batch that fills while no turn is live is
itself an input. Nothing new learns to wait.

### 8. Orphans, named

A session that ends before a report lands leaves the result where the
record lives: the run row and the log under `runs/<id>/`. `scheduler
runs <id>` answers for every kind; nothing injects into a session
that no longer exists. Command and fetch workers die with the
session's context as workers do today (SPEC_DELEGATE 8); the kernel's
pending results die with the session for the same reason the
transcript does.

## testing

Named cases, failing first:

- **tool/delegate**: `task` and `command` both set refuses; neither
  refuses; `model` with `command` refuses; `timeoutMs` with `task`
  refuses naming 2.12.7; the command worker records an ad-hoc run and
  returns the hand-back line; the happy path reports capped output and
  the trailer; the bound kills the tree and the report names the bound
  with partial output; a command worker's spawn context carries the
  deadline, a task worker's carries none.
- **tool/bash**: `timeoutMs` absent runs in-turn verbatim (byte-equal
  result to today); present submits the delegation and answers with
  the hand-back line; the workspace rides the worker.
- **tool/python**: absent is today verbatim under the standing
  ceiling; present answers at once, enqueues, and reports in
  submission order; a session death faults the pending report.
- **tool/web**: absent is today verbatim; present answers at once and
  reports the capped body; a slow fetch dies at the bound, naming it.
- **store/scheduler**: the ms normalization edges (`59_999→1`,
  `60_000→1`, `61_000→2`, `86_400_000→1440`, `86_400_001` refuses,
  `-1` resets, `0` clears); store rows keep minutes (fold golden);
  the crontab line is untouched.
- **core**: the `Report` envelope round-trips `WorkerDone`;
  `WorkerBlock` renders one block per kind with the one trailer
  grammar; two returns in one turn arrive as one batch, in order.
- **cmd/rig**: the goldens regenerate once; the menu budget holds;
  the native set is unchanged; the wiring is registration lines only.

## scope

- `specs/SPEC_ASYNC.md` (this file); `specs/SPEC_DELEGATE.md` gains the
  worker-kind amendment; `specs/SPEC_CORE.md` is named in the PR for
  the `core/` envelope change.
- `store/scheduler`: the interface set, the command-worker kind on the
  existing `DelegateStart`/`Wait` seam, the ms normalization on the
  renamed field.
- `tool/delegate`: `command`, `timeoutMs` (command only), the
  refusals; `tool/bash`: the field and the sugar; `tool/python`,
  `tool/web`: the promotion and the in-process kinds.
- `core/provider.go`: the `Report` on `WorkerDone`.
- `tool/registry.json`: the schema diffs and the description
  sentences; the wire sha and the goldens move once, deliberately.
- `cmd/rig`: the wiring, registration lines only.
- `CHANGELOG.md`, the touched `PACKAGE.md`s: the rule, named.
