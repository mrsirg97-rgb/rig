# tool/delegate

## What it is

The one-shot worker tool (`specs/SPEC_DELEGATE.md`, the workers of
`specs/SPEC_WORKERS.md`): spawn a headless worker on a task now, in a
workspace, and hand it off — the turn gets one line naming the worker,
and the worker's message comes back on a later turn (2.14.0,
SPEC_DELEGATE 8). A piped session has no later turn, so there the tool
waits and answers with the message itself (`Opts.Await`,
SPEC_DELEGATE 9). The worker model
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
- `delegate.go`: the return is an event, not a wait (2.14.0). When the
  worker ends, `settle` clears the band row, and publishes
  `core.WorkerDone` (the worker's number, its task, its content — the
  same text the synchronous result always was — its exit, duration,
  session and log) on the tool's room member; the frontends fold it
  into the next turn. The error out of `settle` is the runner's, the
  one the blocking shape used to return; a handed-off worker's failure
  is its return's exit code, not an error of the turn that handed it
  off, and a worker that never ran has no stdout to cap — the fault
  itself is its return's content, a late failure still being an
  answer. `StopAll` is the idle interrupt: it cancels every
  running worker's context (the set is keyed by the worker number, not
  by the room member, so it works with no room at all — a session
  without one simply never publishes a return). It is the gesture the
  operator makes when there is no turn to interrupt and the workers
  are the only thing still running, and it costs nothing on an empty
  set — a worker that already returned is not stopped twice.
- `doing.go`: the doing set (2.14.1, SPEC_DELEGATE 6) — one named
  constant, `bash read write edit view python web rem`, the only place
  the set is written; since 2.14.7 the registry's delegate words name
  it, so the main agent writes tasks a worker can do. `Run` passes the session's resolved allow list
  intersected with it as the worker's `-allow`, in the session's order;
  `delegate` is out by the same intersection, and an intersection that
  keeps nothing runs the worker allow-none (`-allow none`), never the
  embedded default an absent flag would resolve to. A worker does, the
  session decides: todo, scheduler and plugin are not the worker's to
  call, `rem` is.
- `delegate.go`: `Opts.Ctx` is the session's context and the worker's
  parent, `Opts.Await` is the piped session's shape, both wired by the
  root. `Run` spawns under a context derived from the session's, never
  the turn's, so ending the turn does not end the worker; everything
  that could refuse the hand-off — the empty task, the workspace
  outside the guard, a model that is not resident, a jail that will
  not start — is still an error this turn, and what moves to the next
  turn is the answer. `end` is reached only after Wait returned or
  before the spawn began, so its cancel releases the context the
  session would otherwise carry until it ends; cancel is idempotent,
  and `StopAll` races nothing by reading a map the entry has already
  left.
- `delegate.go`: the tool has no clock (2.12.7, SPEC_DELEGATE 1). No
  `timeoutMs` on the schema, no default, no ceiling: `Timeout:
  noTimeout` and `SpawnCtx:` the worker's own context (derived from
  `Opts.Ctx`) hand the worker the session's life, not the turn's: it
  lives until it exits, the session ends, or `StopAll` is called, and
  `RealSpawn`'s process-group cancel takes the tree down either way. A
  failed worker's exit rides its return (in the awaited shape it is the
  one error voice, `the worker failed (exit N)`); the
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
- `delegate_test.go`, `turn_test.go`, `async_test.go`,
  `status_test.go`: the failing-first named cases over a fake `Spawn`
  and `Fetch` (happy path, cwd refusal, busy refusal, the fan-out
  overlap and the one-slot sequence, no-recursion, the cap), the
  no-clock cases (no deadline on the spawn so a long worker returns,
  `timeoutMs` refuses as an unknown field) and the hand-off cases (the
  one-line hand-back before the worker exits, the turn's context not
  ruling the worker while the session's does, `StopAll` ending every
  worker, a failed worker's return naming its exit, the snapshot
  carrying each worker's last call), and over the real `RealSpawn` the
  idle interrupt killing the worker's whole process tree. Beside the
  cases, the harness: `newTool` is the synchronous shape (a session
  with no next turn to carry a return — a piped run — waits for its
  worker and gets its message as the tool result), `asyncTool` the
  handed-off one (`Run` answers at once and the return is published,
  so a test reads it off the room rather than off the result), both
  sharing everything up to the spawn; `waitForReturn` reads the
  returns off the room once n of them have landed, and the order they
  land in is the order the frontends are tested on; `returns` is the
  inbox a frontend would fold — the workers that came back, in the
  order they came back; `ctxDies` makes the fake end the way a killed
  process does when its context is cancelled (it stops, and its exit
  is not zero).

## How it is consumed

- Registered at the root as a native tool only while a fleet is
  configured (SPEC_DELEGATE): wired with the session's cwd-scope
  scheduler store, the scheduler home, the operator's rig home and
  state-store directory, the swap URL, `self` as the worker command,
  the fleet's model, the fleet's slots, the sandbox, and the
  operator's allow-list (the worker's is its intersection with the
  doing set, `doing.go`).

## Gotchas

- The no-recursion marker (`RIG_DELEGATE`) lives in
  `store/scheduler`'s `Delegate`, not here: a worker's inherited
  marker refuses by name. Fan-out beyond the fleet's slots is not
  refused and not polled: the send-and-wait gate (SPEC_WORKERS 2.6.0)
  lets the request queue at the server, and since 2.12.7 nothing on
  the rig side bounds how long it may wait there; since 2.14.0 the
  worker's own context is not the turn's, so what ends it is the
  session, the worker's own exit, or `StopAll`.
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
