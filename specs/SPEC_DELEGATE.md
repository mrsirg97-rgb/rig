# rig: the delegate (a native tool that spawns and waits)

The interactive session sometimes needs a bounded sub-task done by a
headless worker whose result is a message — a long compute, a sweep,
a review against a foreign spec — without threading the whole turn
through it and without scheduling anything. This spec adds one native
tool, `delegate`, that spawns a worker on a task now, waits for it,
and feeds the worker's last message back into the turn.

It is a one-shot over the existing runner (SPEC_SANDBOX's jail, the
socket proxy, the busy rule), a recorded run in the one scheduler
store, and a resumable transcript in the state store. It
adds no new process topology: the worker is a `rig -p` subprocess the
delegate spawns, exactly as `run-job` spawns one.

## what it is not (named)

- **Not a distributed work queue.** The "notify the workers and the
  first to grab it acquires the job" shape is the cron runner's own:
  a fire is grabbed under flock, first-wins, and the report lands in
  the log — the right shape for unattended scheduled work where
  nobody waits. The delegate is the interactive opposite: the turn
  needs the answer back now, so it spawns its own private worker and
  blocks. A pull-based queue would need standing worker processes and
  a poll-and-await seam for a result the turn can get by spawning; it
  is the async/fan-out non-goal of this phase, named.
- **Not a scheduler.** No crontab line is written; nothing fires on a
  schedule. The run is recorded in the one scheduler store so
  `scheduler runs` and the dashboard show it beside cron runs, but
  nothing ever fires it.
- **Not async, no fan-out, no nesting.** One delegation in flight per
  session; a second call while one runs refuses. A worker cannot
  delegate (an env marker). Each is named with its reason in BOUNDS.

## goals

- One native tool, `delegate`, over the existing runner: the same
  `RunOpts`/`Spawn` path `run-job` uses, verbatim — the jail per the
  sandbox setting (fail closed exactly as workers do), the socket
  proxy, the worker command, the GPU busy rule with `busy:skip`
  semantics.
- Every delegation is a recorded run in the one scheduler store — the
  event log is the spine — under a minted ad-hoc key, so
  `scheduler runs <id>` and the dashboard show it beside cron runs
  with its log path.
- The worker's transcript is its own session in the state store; the
  tool result names that session id so the operator can
  `sessions resume <id>` it.
- The result is the worker's last assistant message, capped the way
  bash output is capped, plus one trailer line. A failed or timed-out
  worker is a tool error naming which; a timeout kills the worker's
  process tree.

## non-goals

- No standing worker pool, no queue, no assignment protocol: the
  grab-the-fire model is the cron runner's, and the interactive turn
  needs a synchronous result (see what it is not).
- No async returns, no out-of-band results: the delegate blocks the
  turn until the worker finishes or times out.
- No fan-out: one worker per call, no parallel sub-delegates.
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
    "cwd":        {"type": "string"},
    "model":      {"type": "string"},
    "timeoutMs":  {"type": "integer", "minimum": 1}
  },
  "required": ["task"]
}
```

- `task` (required): the prompt the worker runs.
- `cwd` (default the session's cwd): canonicalized, must be under the
  session's cwd or the rig home — anything else refuses by name. The
  canonicalization mirrors `middleware/perm`'s `normalizePath` (the
  file tools' absolute-and-clean), so the path test and the worker's
  chdir agree on the same directory.
- `model` (default the workers file's `model` — SPEC_CONFIG 12's
  fleet): the worker row, exactly as `scheduler create` defaults. The
  fleet's model is a row of the operator's models table; there is no
  fallback baked into the binary.
- `timeoutMs` (default 10 minutes): capped at the runner's
  `DefaultRunTimeout` (30 minutes) — the ceiling, named; a larger
  value clamps to it. The timeout bounds the work an untrusted caller
  can induce.
- The tool registers only when the fleet is configured (SPEC_CONFIG
  12's presence rule): no `workers.json`, no `delegate` on the wire —
  there is no worker to spawn, and a tool that can only refuse is
  menu weight.

The description teaches when to delegate: a bounded sub-task whose
result is a message, not a conversation — a long compute, a sweep, a
review — done on a separate worker row without threading the whole
turn through it. It also names the busy rule and the resumable
transcript.

### 2. One tool over the existing runner, verbatim

The delegate spawns the worker through the existing `RunOpts`/`Spawn`
seam, reusing the exact pieces `run-job` uses:

- **The jail** (SPEC_SANDBOX 1): `jailSpawn` when the sandbox profile
  is `jailed` — the bwrap argv, the socket proxy, the scratch home,
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
  process-group cancel) — a timeout kills the worker's process tree,
  and an interrupted turn (the turn ctx dies) kills it the same way.

The worker prompt is `task + ReportBack` (`ReportBack`, the runner's
standing directive), exactly the prompt `run-job` builds. The worker
runs `rig -p <prompt> -base-url <swap>/v1 -model <model>`, with the
jail argv carrying `-base-url unix:<sock>`.

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

The tool finds the worker's session id after the spawn: the worker is
a fresh `-p` one-shot, so its session row is the newest in the
cwd-hash state file started after the spawn — the delegate reads the
state store for it (deterministic: one delegate in flight, the
operator's own session predates the spawn). The tool result names it.

### 4. The record: a minted ad-hoc run

Every delegation is a recorded run in the one scheduler store, the
event log as the spine, under a minted ad-hoc key — not a crontab
line, nothing scheduled.

- The delegate mints a job id via the fold's `mintID()` (`jN`, forward
  over tombstones, the id space shared with cron jobs so they never
  collide — the one store's single sequence), key `jN`, and appends a
  `create` event — **without** writing a crontab line (the store gains
  a create-without-crontab path; nothing is scheduled).
- The job row's `Name` is `delegate:<task first line>` (the first
  line, truncated — the approval prompt's shape, decision 7), `Cron`
  `once` with `At` = the spawn start (so the list renders "at
  passed" and reads as a one-shot record), `Cwd` = the delegate cwd,
  `Model` = the delegate model, `Busy` = `skip`. The list shows it
  with `drift: no crontab line` — the clear non-cron marker.
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
final assistant text and faults — SPEC_HARDENING's "the worker's
stdout is the assistant text and faults, byte-identical"). The result
is that text, capped the way bash output is capped: the loud
`[TRUNCATED]` marker with the full size. One trailer line:

    delegate: exit N · 123ms · session <id> · log <rel path>

A failed or timed-out worker is a tool error naming which:
`delegate: the worker failed (exit N): <last message>` / `delegate:
the worker timed out after <dur> (process tree killed): <last
message>`. The trailer still rides the error, so the operator always
has the session id and log path.

### 6. Bounds, named

- **The fleet's slots gate the in-flight count (SPEC_CONFIG 12)**:
  `slots` (the fleet's, default 1) is how many delegates may run at
  once per session. The delegate takes a non-blocking flock on the
  per-session slot lock files in the scheduler home (one file per
  slot, `delegate:<session>:<i>`), trying the slots in order and
  taking the first free, releasing it after. A call beyond the slots
  refuses: with `slots` 1 the voice is the standing one (`delegate: a
  delegation is already in flight (this session)`); with `slots` > 1
  it names the full set (`delegate: the session's delegate slots are
  full (slots N)`). Sequential delivery makes the overlap rare in the
  loop; the flocks also guard stale workers from interrupted turns
  (they release on death). It is the run-job `acquireLock` shape,
  keyed per session per slot. One slot today, the pool later — the
  gate already counts, so raising `slots` is a file edit, not a code
  change.
- **No recursion**: the delegate sets `RIG_DELEGATE=1` on the worker's
  spawn (the `RIG_HOME` pattern, decision 2). The delegate tool's
  Exec refuses by name when the marker is set: `delegate: a worker
  cannot delegate (RIG_DELEGATE is set — no recursion)`. The
  allow-list omission below is the honest-path guard; the marker is
  the hard rule.
- **The allow-list**: `delegate` is in the embedded allow default
  (the operator's settings). The delegate spawn passes the operator's
  resolved allow-list minus `delegate` as `-allow` to the worker, so
  a worker's allow-list omits it — no recursion even before the
  marker. The marker stays as defense in depth (a worker with a
  custom allow-list that still admits `delegate` refuses by name).
- **The approval gate**: `delegate` counts as mutating (it spawns a
  worker and writes stores). Manual mode asks, and the prompt shows
  the task's first line, not the raw args JSON — `approve.Prompt`
  special-cases `delegate` (decision 7).
- No fan-out, no async, no nesting in this phase: each named with its
  reason above. A queue, a pool, and nested delegates are later
  amendments, not this.

### 7. The approval prompt names the task

`middleware/approve`'s `Prompt` is generic (tool name + a flattened
args preview). For `delegate` the spec wants the operator to glance
the work, not the wire: when `call.Name == "delegate"`, `Prompt`
parses `call.Args` as `{task}` and renders `delegate · <first line>`
(truncated, the existing cap), falling back to the generic shape when
the args do not parse. This is a named, small change to
`approve.go`, its voice unchanged for every other tool.

## testing

Named cases, failing first, in `tool/delegate` over a fake `Spawn`
(the DI seam, as `store/scheduler`'s `runner_test` does):

- **The happy path**: a fake `Spawn` returns an exit-0 worker; the
  result is fed back (the worker's stdout capped, the trailer line's
  shape — exit, duration, session id, log path), the run is recorded
  in the one scheduler store, and the session id is named.
- **The cwd refusal**: a `cwd` outside the session cwd or the rig
  home refuses by name.
- **The busy refusal**: a held GPU refuses loudly naming the holder
  (busy:skip — never an eviction); a busy-check failure fails closed
  naming the failed check.
- **The timeout kill**: a `timeoutMs`-deadline spawn returns a
  timed-out result; the error names the timeout, and the fake
  `Spawn` saw the deadline kill.
- **The one-in-flight refusal**: two concurrent Execs; one runs, the
  second refuses naming the in-flight delegation (slots 1, the
  default).
- **The slots gate**: `slots` 2 — two concurrent Execs run, a third
  refuses naming the full set; the gate counts, not just blocks.
- **The no-recursion refusal**: `RIG_DELEGATE=1` set, Exec refuses by
  name.
- **The approval prompt shape**: `approve.Prompt` for `delegate`
  renders `delegate · <first line>`; the raw-args fallback for other
  tools is unchanged.

Freeze: `middleware/approve` gains the `delegate` branch;
`frontend/tui`'s freeze allowlist gains `tool/delegate`; the native
set grows to 18 when the fleet is configured, and the goldens
regenerate in place (SPEC_CONFIG 12: the set follows
`workers.json` — no fleet, the two worker tools are off the wire and
the goldens are the 16-tool bytes). `core/` and `loop/`
byte-identical. The embedded allow default does not carry the worker
tools; the default allow grows by them when a fleet is configured and
no operator allow stands (SPEC_CONFIG 12).

## scope

- `specs/SPEC_DELEGATE.md` (this file).
- `tool/delegate` — the new native tool (description, schema, Exec,
  the spawn, the record, the cap, the trailer), and its `PACKAGE.md`.
- `store/scheduler` — a create-without-crontab path (the ad-hoc job
  row) and a `Delegate` spawn helper reusing `jailSpawn`/`RealSpawn`/
  `busyState` with the state-store bind; the run record, the log
  path.
- `middleware/approve` — the `delegate` prompt branch.
- `cmd/rig` — the wiring (`delegate` native, `mutatingNatives`,
  the worker `-allow` construction), `main_test.go`'s native-set pin.
- `config/settings.json` — the embedded allow default gains
  `delegate`.
- `frontend/tui/freeze_test.go` — the allowlist gains `tool/delegate`.
- `cmd/rig/testdata/golden_020` — regenerated in place (the native
  set grew).
- `docs/USAGE.md`, `CHANGELOG.md`: the tool, named.
- `core/`, `loop/` frozen; the middleware set unchanged.
