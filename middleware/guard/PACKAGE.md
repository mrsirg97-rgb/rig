# middleware/guard

## What it is

The retry guard middleware: it bounds the model's re-issuance of a
failing tool, keyed by tool name, per turn. Every call executes exactly
once, always; what is bounded is the repeated failing issuance. After
`limit` failures of one tool in a turn, the next issuance of that tool is
refused without executing, naming the bound. The limit-th failure carries
a note telling the model to change the call. Successful re-issuance
(polling) never counts and stays unbounded. Sequential delivery means no
locking.

## What it includes

- `Bound(limit)` — the constructor: returns a `core.ToolMiddleware`.
- `bound` (unexported) — the per-turn state: `limit` plus `counts`
  (tool name -> consecutive failures this turn).

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
- Keyed by tool name, not args: drifting args cannot dodge the budget.
- Success clears the count (the bound tracks streaks, not history);
  polling that eventually succeeds never hits the bound.
- On the limit-th failure the note is appended below the tool's own
  content, or replaces it when the content is blank.
- The refusal returns the same message as both `content` and `error`;
  consumers must not assume the error is a distinct event.
