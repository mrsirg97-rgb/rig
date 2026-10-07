# rig: the delegate (a native tool that spawns and hands off)

The interactive session sometimes needs a bounded sub-task done by a
headless worker whose result is a message; a long compute, a sweep,
a review against a foreign spec; without threading the whole turn
through it and without scheduling anything. This spec adds one native
tool, `delegate`, that spawns a worker on a task now and feeds the
worker's last message back into the turn — on the turn after the one that
asked, since 2.14.0.

It is a one-shot over the existing runner (SPEC_SANDBOX's jail, the
socket proxy, the busy rule), a recorded run in the one scheduler
store, and a resumable transcript in the state store. It
adds no new process topology: the worker is a `rig -p` subprocess the
delegate spawns, exactly as `run-job` spawns one.

**Amended by SPEC_WORKERS (2.4.0)**: the fleet's `slots` gate and the
per-session flocks are retired — the worker model resolves at claim
time (the resident model, else the session's default) and the gate is
the live free-slot read; decision 6's slot bounds below are historical.

**Amended in 2.14.0 (the delegate lets go)**: the tool no longer waits.
`Run` starts the worker and answers in the same turn with one line — the
worker's number, its session, its log — and the worker's message comes
back later as `core.WorkerDone`, folded by every frontend into the head
of the next turn's input. The turn is never interrupted by a return: the
block waits for the boundary, and an inbox that filled while nothing was
live is itself an input (decision 8). What does not move: every refusal
still lands this turn, the record, the log, the jail, the residency gate
and the no-nesting guard. What moves: "an interrupted turn ends the turn
and kills the worker's process tree" — the worker is no longer a child of
the turn's context but of the session's, so the turn ending is not what
kills it (decision 8); the session ending is, and the interrupt gesture
made with no live turn stops every running worker. A piped session
(`rig -p`) has no next turn to carry a return, so there the delegate
keeps its synchronous shape and waits, exactly as it did (decision 9).

**Amended in 2.12.7 (a delegate has no clock)**: `timeoutMs` leaves the
tool — the schema, the defaults and the timed-out voice with it. The
spawn context is the turn's context, so a worker lives until it exits
or the turn is interrupted, and the worker's silence is shown in the
indicator's swarm row rather than killed. The scheduler's own callers
(`run-job`'s per-job timeout, the review fire) keep theirs; decision 1
carries the rule and the evidence.

## what it is not (named)

- **Not a distributed work queue.** The "notify the workers and the
  first to grab it acquires the job" shape is the cron runner's own:
  a fire is grabbed under flock, first-wins, and the report lands in
  the log; the right shape for unattended scheduled work where
  nobody waits. The delegate is the interactive opposite: the turn
  needs an answer back, so it spawns its own private worker and hands it
  off. A pull-based queue would need standing worker processes and
  a poll-and-await seam for a result the turn can get by spawning; it
  is the async non-goal of this phase, named.
- **Not a scheduler.** No crontab line is written: nothing fires on a
  schedule. The run is recorded in the one scheduler store so
  `scheduler runs` and the dashboard show it beside cron runs, but
  nothing ever fires it.
- **Async by hand-off; no nesting.** Fan-out is N delegate calls in one
  turn: each starts its worker and answers at once, so the turn that
  fans out ten costs one turn. A worker cannot delegate (an env
  marker). Each is named with its reason in BOUNDS.

## goals

- One native tool, `delegate`, over the existing runner: the same
  `RunOpts`/`Spawn` path `run-job` uses, verbatim; the jail per the
  sandbox setting (fail closed exactly as workers do), the socket
  proxy, the worker command, the GPU busy rule with `busy:skip`
  semantics.
- Every delegation is a recorded run in the one scheduler store: the
  event log is the spine; under a minted ad-hoc key, so
  `scheduler runs <id>` and the dashboard show it beside cron runs
  with its log path.
- The worker's transcript is its own session in the state store: the
  tool result names that session id so the operator can
  `sessions resume <id>` it.
- The result is the worker's last assistant message, capped the way
  bash output is capped, plus one trailer line — delivered on the turn
  after it lands, not the turn that asked (2.14.0). The hand-back line
  names the worker, its session and its log, so the operator can follow
  it while it runs.

## non-goals

- No standing worker pool, no queue, no assignment protocol: the
  grab-the-fire model is the cron runner's, and the interactive turn
  needs a synchronous result (see what it is not).
- No polling, no timeout, no getter for a worker's result: the return
  is pushed. There is no call that asks "is it done" — the done event
  arrives on the room, or nothing arrives until it does.
- No interrupting a live turn with a return: a worker that finishes
  mid-turn waits for the turn boundary (decision 8).
- No fan-out inside a call: one worker per call, no parallel
  sub-delegates (fan-out is N calls in one turn, bounded by the
  fleet's slots).
- No nesting: a worker cannot delegate.
- No new cron scheduling semantics, no crontab lines.

## decisions

### 1. The schema and defaults

`delegate` takes three fields, one required:

```json
{
  "type": "object",
  "properties": {
    "task":       {"type": "string"},
    "workspace":  {"type": "string"},
    "model":      {"type": "string"}
  },
  "required": ["task"]
}
```

- `task` (required): the prompt the worker runs.
- `workspace` (default the session's workspace): canonicalized, must be
  under the session's workspace or the rig home; anything else refuses by name. The
  requested path and both allowed roots resolve symlinks before the
  containment check, so a lexical child cannot escape through a link.
- `model` (default the workers file's `model`: SPEC_CONFIG 12's
  fleet): the worker row, exactly as `scheduler create` defaults. The
  fleet's model is a row of the operator's models table; there is no
  fallback baked into the binary.
- The tool has no clock (2.12.7). `timeoutMs` is off the schema, the
  seam takes no default, and the spawn takes a caller-supplied context —
  the turn's until 2.14.0, the session's since (decision 8): a worker
  lives until it exits, the session ends, or the idle interrupt stops it,
  and the cancel's process-group kill takes the whole tree down either way
  (decision 2). A timeout on a fan-out seam guesses how long the work
  should take, and under a shared slot the guess is always wrong for
  someone: a ten-way fan-out of test-pass reads on a one-slot model on
  2026-10-05 lost its tail worker at 1500.3s to `the worker timed out
  after 25m0.003s (process tree killed)`, the clock started at spawn,
  so it measured the nine ahead of it in the server's queue rather
  than the work. Waiting was right — *extras wait for a slot rather
  than failing* — and a clock turned the wait into a death sentence on
  a schedule. What bounds an induced worker is the turn that asked for
  it and the operator's hand on the interrupt; neither needs a guess
  at a duration.
- Silence is shown, never acted on: the indicator's swarm row carries
  each worker's heartbeat age (SPEC_SWARM 7), so a worker that has
  written nothing for ten minutes reads as one in the band and the
  operator ends the turn. The stall kill (1.3.8) stays exactly where
  it was and is not a clock on the work: it is the runner's per-job
  `stall` window (minutes), an opt-in operator setting that speaks
  about a process that has written nothing for the window, never about
  how long a worker may run. The interactive delegate carries no
  window — `stallMs` left the schema with the 2.6.0 send-and-wait gate,
  which queues at the server and would shoot a worker for waiting —
  and gains none here.
- `DelegateInput.Timeout` stays for the scheduler's own callers: a
  positive value is the fire's spend bound, capped at the seam's 24h
  (`maxDelegateTimeout`), exactly as a `run-job` fire carries its
  per-job `timeout` in minutes; the runner and its default are
  untouched. A negative value runs the worker on the caller's context
  alone, which is what the delegate tool passes, beside the review
  fire and the swarm.
- The tool registers only when the fleet is configured (SPEC_CONFIG
  12's presence rule): no `workers.json`, no `delegate` on the wire:
  there is no worker to spawn, and a tool that can only refuse is
  menu weight.

The description teaches when to delegate: a bounded sub-task whose
result is a message, not a conversation; a long compute, a sweep, a
review; done on a separate worker row without threading the whole
turn through it. It also names the busy rule and the resumable
transcript.

### 2. One tool over the existing runner, verbatim

The delegate spawns the worker through the existing `RunOpts`/`Spawn`
seam, reusing the exact pieces `run-job` uses:

- **The jail** (SPEC_SANDBOX 1): `jailSpawn` when the sandbox profile
  is `jailed`; the bwrap argv, the socket proxy, the scratch home,
  the kernel bind, the `sandboxBinds`. Fail closed exactly as workers
  do: no `bwrap` on a linux box, a loud refusal; `sandbox: "off"`
  runs unjailed with the one loud line. The jailed path is verbatim;
  one named addition, the state-store bind (decision 3).
- **The socket proxy** (SPEC_SANDBOX 3): the worker's model calls ride
  the bound unix socket to the swap; the jail stays netless.
- **The worker command**: the root's `self` (the same `WorkerCmd`
  `run-job` wires), so the worker is the operator's binary.
- **The GPU busy rule** (the runner's `busyState`): `busy:skip`
  semantics. A held GPU is a loud refusal naming the holder, never an
  eviction from inside a turn. A busy-check failure (uncertain GPU
  state) fails closed the same way, naming the failed check.
- **The spawn**: `RealSpawn` (`CommandContext`, Setpgid, the SIGKILL
  process-group cancel). The context handed it is the caller's: for an
  interactive delegate that is the session's, so the worker's tree dies
  with the session or the idle interrupt and not with the turn (2.14.0);
  for the scheduler's own callers it is the fire's, and a caller that gives
  the fire its own spend ceiling kills it the same way when the ceiling
  expires. Nothing else kills it.

The worker prompt is `task + ReportBack` (`ReportBack`, the runner's
standing directive), exactly the prompt `run-job` builds. The worker
runs `rig -p - -base-url <swap>/v1 -model <model>` with the prompt on
its stdin (2.9.3: a prompt never rides argv; the spawn context carries
it, `WithPrompt`/`PromptFrom`, and `RealSpawn` pipes it), with the jail
argv carrying `-base-url unix:<sock>`.

### 3. The transcript is resumable (the one named deviation)

SPEC_SANDBOX 1 sends the worker's stores to a scratch home
(`<cwd>/.rig-job`) inside the jail so the worker cannot poison the
operator's stores; the cron report is the deliverable. The delegate
deliberately deviates for the transcript: the worker's session must
land in the operator's state store for `sessions resume <id>`.

The jailed delegate adds one read-write bind to `jailSpawn`: the
operator's state-store directory (`<rig home>/sessions`) is bound at
the scratch home's sessions path (`<scratch>/sessions`), so the
worker's recorder writes `sessions/<cwd-hash>.sqlite` at the
operator's real path. The jail keeps the rest of SPEC_SANDBOX 1's
containment (rem, todo, scheduler writes stay in the scratch home;
the message is the deliverable, the transcript is the resumable
exception). `sandbox: "off"` needs no bind: the worker runs with the
operator's home and writes the state store directly.

The tool mints the worker's session id before the spawn and passes it to
the fresh `-p` worker through the worker-only `-session-id` flag. The
recorder therefore lands under an identity already owned by the delegate,
and the tool result names it directly. Concurrent slots in one cwd cannot
misattribute one another by querying for the newest session.

### 4. The record: a minted ad-hoc run

Every delegation is a recorded run in the one scheduler store, the
event log as the spine, under a minted ad-hoc key; not a crontab
line, nothing scheduled.

- The delegate mints a job id via the fold's `mintID()` (`jN`, forward
  over tombstones, the id space shared with cron jobs so they never
  collide; the one store's single sequence), key `jN`, and appends a
  `create` event; **without** writing a crontab line (the store gains
  a create-without-crontab path; nothing is scheduled).
- The job row's `Name` is `delegate:<task first line>` (the first
  line, truncated; the approval prompt's shape, decision 7), `Cron`
  `once` with `At` = the spawn start (so the list renders "at
  passed" and reads as a one-shot record), `Cwd` = the delegate cwd,
  `Model` = the delegate model, `Busy` = `skip`. The list shows it
  with `drift: no crontab line`; the clear non-cron marker.
- The run is `RecordRun` with the ad-hoc id, status from the worker's
  exit, duration, and the log path (`runs/<id>/<timestamp>.log`,
  written like `run-job`'s, pruned the same way). `scheduler runs
  <id>` resolves it (the ad-hoc job row exists) and lists it beside
  cron runs; the dashboard's `sched.List` shows the job row.

The ad-hoc job row is a disposable projection like any job row: it
never needs a crontab line, remove survives compaction, and the run
history survives compaction (the runs container, SPEC_STATE's
deviation).

### 5. The result, fed back

The worker's last assistant message is its stdout (`-p` prints the
final assistant text and faults; SPEC_HARDENING's "the worker's
stdout is the assistant text and faults, byte-identical"). The result
is that text, capped the way bash output is capped: the loud
`[TRUNCATED]` marker with the full size. One trailer line:

    delegate: exit N · 123ms · session <id> · log <rel path>

A failed worker's return names its exit in the trailer; in the
synchronous shape (decision 9) it is also a tool error:
`delegate: the worker failed (exit N)`. There is no timeout voice: the
tool has no clock (decision 1). The trailer always rides the return, so
the operator has the session id and log path either way. A worker that
never ran — a spawn that faulted after acceptance — has no stdout to
cap, and its return's content is the fault itself: the reason is the
answer, and it is not dropped on the floor.

The hand-back line — what the turn that delegated actually gets — is
one line and never the answer:

    delegate: worker #2 started · session <id> · log <rel path>

### 6. Bounds, named

- **The fleet's slots gate the in-flight count (SPEC_CONFIG 12)**:
  `slots` (the fleet's, default 1) is how many delegates may run at
  once per session. The delegate takes a non-blocking flock on the
  per-session slot lock files in the scheduler home (one file per
  slot, `delegate:<session>:<i>`), trying the slots in order and
  taking the first free, releasing it after. A call that finds every
  slot held waits: it retries the acquisition on a short interval
  until a slot frees or the call's context ends, so a fan-out can
  issue more calls than slots and the extras queue rather than
  fail. On the context ending the refusal is the standing voice:
  with `slots` 1 `delegate: a delegation is already in flight (this
  session)`, with `slots` > 1 `delegate: the session's delegate
  slots are full (slots N)` with the wait time named. The flocks
  also guard stale workers from interrupted turns (they release on
  death). It is the run-job `acquireLock` shape, keyed per session
  per slot. The gate already counts, so raising `slots` is a file
  edit, not a code change.
- **Amended in 2.6.0**: the send-and-wait gate replaces the slot
  read and `WaitBusy` (SPEC_WORKERS 2.6.0): the spawn's request
  queues at the llama-server, so a busy GPU is waited on by the
  server, not by rig; the one read at dispatch refuses only a model
  that is not resident. `Stall` retires with the stall kill: a
  queued worker writes nothing and the silence kill would shoot it;
  the timeout stays the spend ceiling until 2.12.7 takes it out of the
  tool too, leaving the turn as the only bound. `Observe` and
  `SpawnCtx` stay as amended below.

- **Four swarm amendments (SPEC_SWARM)**, all defaulted to today's
  behavior. `WaitBusy` (false): a busy GPU is waited on — the busy
  check polls `busyState` on a short interval until the model runs or
  the call's context ends; the swarm's parallelism is the GPU slots,
  and a busy-check failure still fails closed. `Member` (nil): the
  worker's member in the session's room (2.11.0); with one, the spawn
  gets the fleet pipe and every frame the child sends is published as
  the worker. `SpawnCtx` (Background): the
  base context the spawn runs on, so a swarm stop kills the in-flight
  worker with it — since 2.12.7 that is the whole of it, the tool's
  spawn context is the turn's and there is no timeout to leave a
  worker to. The seam has no silence window: `Stall` left it with the
  2.6.0 retirement above, and the silence kill is the runner's per-job
  `stall`, which no delegate sets.
- **The status Observe (SPEC_SWARM 7)**: the tool gains an optional
  `Notify` seam (nil = silent, today's behavior); with it, an
  interactive delegate emits a `core.SwarmStatus` snapshot on start,
  on the spawn's stream bytes (the same Observe), and on exit — one
  worker row, the queue counts zero, throttled to a few per second
  with the exit's last frame always landing. The TUI then shows the
  delegate's worker row only; CLI/oneshot ignore the event.
- **No recursion**: the delegate sets `RIG_DELEGATE=1` on the worker's
  spawn (the `RIG_HOME` pattern, decision 2). The delegate tool's
  Exec refuses by name when the marker is set: `delegate: a worker
  cannot delegate (RIG_DELEGATE is set; no recursion)`. The
  allow-list omission below is the honest-path guard; the marker is
  the hard rule.
- **The allow-list**: `delegate` is in the embedded allow default
  (the operator's settings). The delegate spawn passes the operator's
  resolved allow-list minus `delegate` as `-allow` to the worker, so
  a worker's allow-list omits it; no recursion even before the
  marker. The marker stays as defense in depth (a worker with a
  custom allow-list that still admits `delegate` refuses by name).
- **The approval gate**: `delegate` counts as mutating (it spawns a
  worker and writes stores). Manual mode asks, and the prompt shows
  the task's first line, not the raw args JSON; `approve.Prompt`
  special-cases `delegate` (decision 7).
- No queue, no pool, no nesting: each named with its reason above.
  Async returns arrived in 2.14.0 (decision 8) as a hand-off, not a
  poll; a standing pool is still a later amendment, not this.

### 7. The approval prompt names the task

`middleware/approve`'s `Prompt` is generic (tool name + a flattened
args preview). For `delegate` the spec wants the operator to glance
the work, not the wire: when `call.Name == "delegate"`, `Prompt`
parses `call.Args` as `{task}` and renders `delegate · <first line>`
(truncated, the existing cap), falling back to the generic shape when
the args do not parse. This is a named, small change to
`approve.go`, its voice unchanged for every other tool.

### 8. The return is the next turn's (2.14.0)

The worker's outcome is published as `core.WorkerDone` on the delegate's
room member, and each frontend keeps an inbox: returns append in arrival
order and never overwrite one another (a `WorkerDone` is a story, not a
`Snapshot` — that is why it does not implement `Snapshot`, and why two
workers that finish during one turn arrive as two blocks in the order
they finished). The drain is at the top of `Input`, ahead of the
operator's text: the model reads the returns as a turn of its own, then
what the operator typed.

    delegate #2 returned · exit 0 · 4m12s · session <id>
    <the worker's stdout, capped, and its trailer>

An inbox that fills while no turn is live is itself an input: `Input`
returns the block alone and the session starts a turn, exactly as the
steer slot does (SPEC_TUI 3). A live turn is never interrupted — a
return arriving mid-turn waits for `Input` to be asked again. Nothing
polls, nothing waits, no clock: the wake is a signal, and `Input` was
already the place a turn is made.

The worker is a child of the *session's* context (the root passes it as
`Opts.Ctx`), not the turn's, which is the other half of letting go: a
worker outlives the turn that started it, and dies with the session.
The interrupt gesture with no live turn (esc in the TUI, the dashboard's
stop button) reaches the tool through a root-wired hook — the loop is
untouched and the frontends know only "stop what is running". The CLI
has no such gesture: there, SIGINT ends the session and the workers die
with it.

`store/scheduler` splits the same path rather than duplicating it:
`DelegateStart` does everything that can refuse (seams, the recursion
guard, the residency gate, the record, the jail, the fleet pipe) and
returns a `Delegation` naming the run, its session and its log; `Wait`
collects the outcome once the log and the run record are on disk.
`Delegate` is `DelegateStart` and `Wait`, so the swarm and the review
fire are untouched, and there is one implementation of both shapes.

### 9. The piped session waits (2.14.0)

A `rig -p` session ends when its turn ends: there is no next turn to
carry a return, and an async delegate there would quietly kill its
workers at exit and lose their answers. So the delegate keeps its
synchronous shape in that one shape of session — `Opts.Await`, wired by
the root when the frontend is a oneshot — and the tool result is the
worker's message, as before. Cron fires are oneshots: a scheduled job
can still fan out and wait. What the interactive session gains, the
piped session does not lose.

## testing

Named cases, failing first, in `tool/delegate` over a fake `Spawn`
(the DI seam, as `store/scheduler`'s `runner_test` does):

- **The happy path**: a fake `Spawn` returns an exit-0 worker: the
  result is fed back (the worker's stdout capped, the trailer line's
  shape; exit, duration, session id, log path), the run is recorded
  in the one scheduler store, and the session id is named. In the
  hand-off shape the same assertions hold of the *return*, and the tool
  result is the one hand-back line.
- **The hand-back arrives first**: a fake `Spawn` that blocks: `Run`
  returns the one line while the worker is still running, and the turn
  it named is not the turn that gets the answer.
- **The turn's context does not rule the worker**: cancelling the turn's
  context leaves the spawn's context alive; cancelling the session's
  context ends it. (Reverses 2.12.7's interrupted-turn test.)
- **The idle interrupt stops every running worker**: esc with no live
  turn reaches the tool, and each worker's context dies with it; under
  the jail, the worker's whole process tree dies with it.
- **The inbox is a queue, not a slot**: two `WorkerDone` events during a
  live turn arrive at the next `Input` as one block, in order, ahead of
  the operator's text; one `WorkerDone` with no live turn makes `Input`
  return that block alone (each frontend, and the TUI's golden stream).
- **The pipe carries the call, not the body**: `tool_start` round-trips
  the encoder and the pipe; a 10 KB `write` crosses as one line of at
  most 80 characters and never carries its content, its `old` or its
  `new`; a worker with no transport publishes nothing.
- **The cwd refusal**: a `cwd` outside the session cwd or the rig
  home refuses by name.
- **The busy refusal**: a held GPU refuses loudly naming the holder
  (busy:skip; never an eviction); a busy-check failure fails closed
  naming the failed check.
- **No clock on the work**: the spawn context carries no deadline, a
  worker still working past the old ten-minute default returns its
  message, and a call carrying `timeoutMs` refuses as an unknown
  field.
- **The interrupt takes the tree**: cancelling the caller's context
  cancels the spawn context, and over the real `RealSpawn` (`Setpgid`)
  both the worker and the child it backgrounded are gone by the time
  the turn ends.
- **The silence window is the runner's**: the stall kill has no test
  here because the tool sets no window; the per-job `stall` cases are
  `store/scheduler`'s.
- **The fan-out overlap**: `slots` 3: three concurrent Execs run, and
  the spawn seam's timestamps prove the three spawns overlap.
- **The one-slot sequence**: `slots` 1: three concurrent Execs run
  one after another and each succeeds.
- **The slots-full wait**: `slots` 2: two concurrent Execs run, a
  third with an expiring context waits and then refuses naming the
  full set and the wait time; no worker was spawned for it.
- **The no-recursion refusal**: `RIG_DELEGATE=1` set, Exec refuses by
  name.
- **The approval prompt shape**: `approve.Prompt` for `delegate`
  renders `delegate · <first line>`; the raw-args fallback for other
  tools is unchanged.

Freeze: `middleware/approve` gains the `delegate` branch;
`frontend/tui`'s freeze allowlist gains `tool/delegate`; the native
set grows to 18 when the fleet is configured, and the goldens
regenerate in place (SPEC_CONFIG 12: the set follows
`workers.json`; no fleet, the two worker tools are off the wire and
the goldens are the 16-tool bytes). `core/` and `loop/`
byte-identical. The embedded allow default does not carry the worker
tools; the default allow grows by them when a fleet is configured and
no operator allow stands (SPEC_CONFIG 12).

## scope

- `specs/SPEC_DELEGATE.md` (this file).
- `tool/delegate`: the new native tool (description, schema, Exec,
  the spawn, the record, the cap, the trailer), and its `PACKAGE.md`.
- `store/scheduler`: a create-without-crontab path (the ad-hoc job
  row) and a `Delegate` spawn helper reusing `jailSpawn`/`RealSpawn`/
  `busyState` with the state-store bind; the run record, the log
  path.
- `middleware/approve`: the `delegate` prompt branch.
- `cmd/rig`: the wiring (`delegate` native, `mutatingNatives`,
  the worker `-allow` construction), `main_test.go`'s native-set pin.
- `config/settings.json`: the embedded allow default gains
  `delegate`.
- `frontend/tui/freeze_test.go`: the allowlist gains `tool/delegate`.
- `cmd/rig/testdata/golden_020`: regenerated in place (the native
  set grew).
- `docs/USAGE.md`, `CHANGELOG.md`: the tool, named.
- `core/`, `loop/` frozen: the middleware set unchanged.
