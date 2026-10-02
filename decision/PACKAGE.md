# decision

## What it is

The decision seam (SPEC_DECISION): the typed vocabulary of a rig decision
and the two interfaces a decision consumer holds — `Decider`, the seam a
decision server implements (`Decide(ctx, state, questions) -> answers`), and
`Recorder`, the seam the gates hold to record a final row. A question is
typed (`choice` with its choices, `score`, `yesno`); an answer carries a
value, a confidence (the probability, 0..1), and its decider (who produced
it). Stdlib-only leaf beside `pathguard`; no imports of the stores.

## What it includes

- `decision.go`: the types (Question, Answer, Final), the two interfaces,
  the kind/status/site vocabulary as constants, and the question
  constructors (`Choice`, `Score`, `YesNo`).

## How it is consumed

- The gate packages (`middleware/approve`, `middleware/perm`,
  `middleware/guard`, `middleware/paths`) and `store/scheduler`'s runner
  hold an optional `Recorder` (variadic, nil records nothing) and call it
  at their decision points.
- The HTTP proposer, the proposal queue, and the reviewer (this package,
  SPEC_DECISION) implement `Decider` and drive `store/decision` through
  the sinks the root wires.
- `store/decision` implements the row shapes; `cmd/rig` is the only
  package that wires them together.

## Gotchas

- `Recorder.Record` returns nothing on purpose: a store error never fails
  a call, and the swallow is loud only where the root wired a log.
- The confidence is a probability; the store refuses anything outside
  0..1, and the HTTP client drops such answers before they reach it.
