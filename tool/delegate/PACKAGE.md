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

- `delegate.go`: `Opts` (the root's wiring, carrying the session's
  default model) and `New`, the adapter with the description (the
  claim-time resolution and the slot gate), schema, and `Exec`;
  the `pathguard` workspace rule (canonicalization, the
  outside-the-session/rig-home refusal, the directory check); the
  output cap (bash's 256 KiB shape, the loud `[TRUNCATED: N bytes]`
  marker) and the trailer line (exit, duration, session id, log path);
  the explicit worker session id threaded through the spawn.
- `delegate.go`: `stallMs` rides the schema beside `timeoutMs`
  (0 = off, today's plain timeout; the tool keeps its own 30-minute
  `timeoutMs` ceiling), and `Stall` rides `DelegateInput`.
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
- `delegate_test.go`: the failing-first named cases over a fake
  `Spawn` and `Fetch` (happy path, cwd refusal, busy refusal, timeout,
  the stall kill, the fan-out overlap and the one-slot sequence, the
  slots-full wait, no-recursion, the cap).

## How it is consumed

- Registered at the root as a native tool only while a fleet is
  configured (SPEC_DELEGATE): wired with the session's cwd-scope
  scheduler store, the scheduler home, the operator's rig home and
  state-store directory, the swap URL, `self` as the worker command,
  the fleet's model, the fleet's slots, the sandbox, and the
  operator's allow-list (the worker's omits `delegate`).

## Gotchas

- The no-recursion marker (`RIG_DELEGATE`) and the per-slot flocks
  live in `store/scheduler`'s `Delegate`, not here: a worker's
  inherited marker refuses by name, and a call that finds the
  session's slots full waits on a short poll for one to free until
  its call context ends (the lock check precedes the marker); the
  standing "already in flight" voice at one slot, the full-set
  "slots are full (slots N)" voice naming the wait time otherwise.
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
