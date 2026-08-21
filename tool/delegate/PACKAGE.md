# tool/delegate

## What it is

The one-shot worker tool (`specs/SPEC_DELEGATE.md`): spawn a headless
worker on a task now, in a cwd, wait, and feed back its last message.
One tool over the existing runner — the jail per the sandbox setting
(fail closed exactly as workers do), the socket proxy, the worker
command, the GPU busy rule with `busy:skip` semantics — with a
recorded run in the cwd-scope scheduler store under a minted ad-hoc
key (no crontab line, nothing scheduled) and a resumable transcript in
the state store.

## What it includes

- `delegate.go` — `Opts` (the root's wiring) and `New`, the adapter
  with the description, schema, and `Exec`; the cwd canonicalization
  and the outside-the-session/rig-home refusal; the output cap (bash's
  256 KiB shape, the loud `[TRUNCATED: N bytes]` marker) and the
  trailer line (exit, duration, session id, log path); the worker
  session-id discovery in the state store.
- `delegate_test.go` — the failing-first named cases over a fake
  `Spawn` and `Fetch` (happy path, cwd refusal, busy refusal, timeout,
  one-in-flight, no-recursion, the cap).

## How it is consumed

- Registered at the root as a native tool (SPEC_DELEGATE): wired with
  the session's cwd-scope scheduler store, the scheduler home, the
  operator's rig home and state-store directory, the swap URL, `self`
  as the worker command, the default worker model, the sandbox, and
  the operator's allow-list (the worker's omits `delegate`).

## Gotchas

- The no-recursion marker (`RIG_DELEGATE`) and the one-in-flight flock
  live in `store/scheduler`'s `Delegate`, not here: a worker's
  inherited marker refuses by name, and a concurrent operator call
  refuses "already in flight" first (the lock check precedes the
  marker).
- The jailed worker's transcript lands at the operator's state-store
  path via `jailSpawn`'s sessions-dir bind (SPEC_DELEGATE 3); the
  session id is read back as the newest row started after the spawn.
- `Exec` reads `os.Getwd()` for the session cwd, so the tests pin the
  real test cwd, not a fixture path.
