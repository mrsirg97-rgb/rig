# middleware/guard

## What it is

The retry guard middleware: it bounds the model's re-issuance of a
failing tool, keyed by tool name, per turn. Every call executes exactly
once, always; what is bounded is the repeated failing issuance. After
`limit` failures of one tool in a turn, the next issuance of that tool is
refused without executing, naming the bound. The limit-th failure carries
a note telling the model to change the call. Successful re-issuance
(polling) never counts and stays unbounded. The maps sit under a mutex
since the batch (SPEC_EVT 2a): a concurrent run calls the chain from
many goroutines.

## What it includes

- `Bound(limit)` — the constructor: returns a `core.ToolMiddleware`.
- `bound` (unexported) — the per-turn state: `limit`, `counts` (tool
  name -> consecutive identical failures this turn), and `lastFailed`
  (tool name -> the args of the last failure; the streak's identity).

## How it is consumed

- `Bound` is wired at the root into the middleware chain (innermost
  order); `r.retries` is the limit.
- It is one object on the widened seam: it wraps (`Wrap`, producing the
  refusal and the note shape `(content, err)` on the way out) and
  observes (`TurnStart` clears the budget). Not two hooks.
- `TurnStart` is the loop's fan-out (SPEC_HARDENING L6): a new user
  message is a new budget.

## Gotchas

- `limit < 1` clamps to 1.
- Identical calls inside one concurrent run may all execute: each passed
  the check before any had failed. They are duplicates, not retries; the
  bound strikes the re-issuance after them (SPEC_EVT 2a, named).
- Keyed by tool name, but the streak is per args: the bound strikes
  identical retries only. A corrected call (args differing from the last
  failed args) resets the count before the guard check, so the "change the
  call" teaching never blocks the changed call.
- Not a count per (name, args): the marker is the tool's last failure
  only. Two failing calls of one tool alternating within a turn reset each
  other and never trip the bound (`TestDriftingArgsEachGetAFreshStreak`
  pins it as the accepted consequence, SPEC_HARDENING 7); the loop has no
  round cap, so the operator's interrupt is that loop's bound.
- Success clears the count and the last-failed-args marker (the bound
  tracks streaks, not history); polling that eventually succeeds never
  hits the bound.
- On the limit-th failure the note is appended below the tool's own
  content, or replaces it when the content is blank.
- The refusal returns the same message as both `content` and `error`;
  consumers must not assume the error is a distinct event.
