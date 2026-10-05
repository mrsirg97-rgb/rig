# tool/delegate

## What it is

The one-shot worker tool (`specs/SPEC_DELEGATE.md`, the workers of
`specs/SPEC_WORKERS.md`): spawn a headless worker on a task now, in a
workspace, wait, and feed back its last message. The worker model
resolves at claim time — the named one, else the resident model, else
the session's default — and the gate is the live free-slot read: a
call with no free slot refuses (`no free slot; this turn holds the
only one` on a one-slot model). One tool over the existing runner; the
jail per the sandbox setting (fail closed exactly as workers do), the
socket proxy, the worker command, with a recorded run in the
cwd-scope scheduler store under a minted ad-hoc key (no crontab line,
nothing scheduled) and a resumable transcript in the state store.

## What it includes

- `Delegate`: the tool as its interface (2.12.6): `tool.Definition` (the
  description carries the claim-time resolution and the slot gate, the
  schema beside it) and `Run(ctx, task, workspace, model)`,
  the one verb; `Exec` decodes and routes, so the blank-task refusal and
  the `pathguard` workspace rule (canonicalization, the
  outside-the-session/rig-home refusal, the directory check) are met by a
  Go caller and the model alike. `Opts` is the root's wiring (carrying
  the session's default model) and `New` returns the interface; the
  adapter struct is unexported. Beside the verb:
  the output cap (bash's 256 KiB shape, the loud `[TRUNCATED: N bytes]`
  marker) and the trailer line (exit, duration, session id, log path);
  the explicit worker session id threaded through the spawn.
- `delegate.go`: the tool has no clock (2.12.7, SPEC_DELEGATE 1). No
  `timeoutMs` on the schema, no default, no ceiling: `Timeout:
  noTimeout` and `SpawnCtx: ctx` hand the worker the turn's context, so
  it lives until it exits or the turn is interrupted and
  `RealSpawn`'s process-group cancel takes the tree down. A failed
  worker is the one error voice (`the worker failed (exit N)`); the
  silence window is the runner's per-job `stall` setting, not this
  tool's, and a silent worker is shown (the swarm row's heartbeat age),
  never killed for it.
- `delegate.go`: `Room` (optional, nil = silent): the tool is a member
  of the session's `broadcast` room (`MemberID`) and publishes
  `SwarmStatus` snapshots there (SPEC_SWARM 7, 2.11.0); each running
  worker is a member below it (`MemberID - n`, the row shows `n`) that
  the spawn publishes the child's heartbeats as, and the tool's
  subscription stamps the row from the room; the loop transport keeps
  one pending per sender, so a heartbeat storm is one frame. Before 2.11.0 this was a `Notify` closure and the `status`
  emitter's clock — an interactive delegate emits `SwarmStatus`
  snapshots on start, on the spawn's stream bytes (the same Observe),
  and on exit, one worker row, zero queue counts, throttled to a few
  per second with the exit's last frame always landing.
- `delegate_test.go`, `turn_test.go`: the failing-first named cases
  over a fake `Spawn` and `Fetch` (happy path, cwd refusal, busy
  refusal, the fan-out overlap and the one-slot sequence,
  no-recursion, the cap) and the no-clock cases (no deadline on the
  spawn so a long worker returns, `timeoutMs` refuses as an unknown
  field, an interrupted turn cancels the spawn context, and over the
  real `RealSpawn` it kills the worker's whole process tree).

## How it is consumed

- Registered at the root as a native tool only while a fleet is
  configured (SPEC_DELEGATE): wired with the session's cwd-scope
  scheduler store, the scheduler home, the operator's rig home and
  state-store directory, the swap URL, `self` as the worker command,
  the fleet's model, the fleet's slots, the sandbox, and the
  operator's allow-list (the worker's omits `delegate`).

## Gotchas

- The no-recursion marker (`RIG_DELEGATE`) lives in
  `store/scheduler`'s `Delegate`, not here: a worker's inherited
  marker refuses by name. Fan-out beyond the fleet's slots is not
  refused and not polled: the send-and-wait gate (SPEC_WORKERS 2.6.0)
  lets the request queue at the server, and since 2.12.7 nothing on
  the rig side bounds how long it may wait there — the turn's context
  is the only thing that ends it.
- The jailed worker's transcript lands at the operator's state-store
  path via `jailSpawn`'s sessions-dir bind (SPEC_DELEGATE 3); the
  parent mints its id and passes it as `-session-id`, so concurrent
  delegates cannot claim one another's transcript.
- `workspace` containment is the one rule in `pathguard` (shared with
  the scheduler tool): the requested directory and both allowed roots resolve
  symlinks before the worker starts, a lexical child that resolves outside
  refuses, and a file is not a workspace (it refuses at the boundary, not at
  spawn).
- `Exec` reads `os.Getwd()` for the session cwd, so the tests pin the
  real test cwd, not a fixture path.
