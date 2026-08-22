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
many goroutines. Beside the bound (SPEC_HARDENING decision 9, "the two
bounds"): `Rounds`, the per-turn cap on tool calls, and `Cap`, the wall
that bounds every tool result before the transcript.

## What it includes

- `Bound(limit)` — the constructor: returns a `core.ToolMiddleware`.
- `bound` (unexported) — the per-turn state: `limit`, `counts` (tool
  name -> consecutive identical failures this turn), and `lastFailed`
  (tool name -> the args of the last failure; the streak's identity).
- `Rounds(n)` — the round cap: counts every call in a turn, past `n`
  refuses without executing (a teaching voice naming the cap and what to
  do), cleared at `TurnStart`. It caps the alternation the bound does not
  (SPEC_HARDENING 7's named consequence) and a runaway batch (a
  concurrent run of 50 reads counts 50; the cap is on calls, not turns).
- `Cap(bytes)` — the result bound: truncates an oversized result to the
  head and the tail with the loud `[TRUNCATED]` marker naming the full
  size, plus the teaching line "re-read a narrower range". A small result
  is byte-identical.

## How it is consumed

- `Bound`, `Rounds`, and `Cap` are wired at the root into the middleware
  chain (innermost order): first-listed is innermost, so `Bound` is the
  innermost, `Rounds` wraps it (it sees every call, including the bound's
  refusals), and `Cap` wraps both (the wall behind the whole chain).
  `r.retries`, `r.rounds`, and `r.resultCap` are the numbers; a zero
  round/result-cap in a directly-constructed root descends to the
  defaults (200 and 64 KiB), the config layer always resolves one.
- Each is one object on the widened seam: it wraps (`Wrap`, producing the
  refusal and the note shape `(content, err)` on the way out) and
  observes (`TurnStart` clears the budget). Not two hooks.
- `TurnStart` is the loop's fan-out (SPEC_HARDENING L6): a new user
  message is a new budget.
- Workers and delegated workers get the same chain: the `-p` worker path
  runs through the same `wire()`.

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
- `Rounds`: `n < 1` clamps to 1; the counter sits under a mutex (a
  concurrent batch calls the chain from many goroutines). The n+1th call
  and every call after it is refused without executing, and the refusal
  is both `content` and `error`.
- `Cap`: `bytes < 1` clamps to 1; a tiny cap may elide the content whole
  and return the marker alone (the marker names the full size). Every
  tool's own cap stays; this is the wall behind them, so a result the
  tool already capped can still be further bounded here.
