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
- `http.go`: the HTTP `Decider` (settings `decisionUrl`): POST
  `<decisionUrl>/v1/systemone` with the state and the typed questions,
  the reply's answers stamped with the decider (the configured name,
  else the URL's host). An answer whose confidence is not a probability
  or whose value is empty drops; the reply body is capped at 1 MiB.
- `queue.go`: the proposal queue: `Propose` enqueues on a bounded channel
  (`QueueCap`), `Run` is the one goroutine that decides, writes through
  the `Sink`, and wakes the reviewer on a landing. A full queue drops
  loudly (a proposal is not a decision); a decider or sink error drops
  loudly and lands nothing.
- `review.go`: the reviewer: a size-one wake channel (no timer, no
  poll), `Run` the loop, `Drain` the pass — every pending row in one
  fire through the `Fire` seam (the root wires the scheduler's
  `Delegate`, which waits on the free-slot gate), one verdict line per
  row parsed like the swarm reviewer's (`verdict: <id> approve` or
  `verdict: <id> deny <corrected answer>`), last naming wins, a deny
  without a correction is not a verdict, unnamed rows stay pending, a
  fire that settled something and left pending rows self-wakes once, a
  fire that settled nothing waits for the next landing.
- `site.go`: the bash site: one link at the chain's inner end; after a
  bash call returns it proposes the risk question (choice: safe,
  changes, dangerous) over the command, the workspace, and how the
  call ended. The call never waits: the proposal rides the queue's
  channel and returns at once.

## How it is consumed

- The gate packages (`middleware/approve`, `middleware/perm`,
  `middleware/guard`, `middleware/paths`) and `store/scheduler`'s runner
  hold an optional `Recorder` (variadic, nil records nothing) and call it
  at their decision points.
- `cmd/rig` wires the store as the queue's `Sink` and the reviewer's
  `Reviews` (the adapters in `cmd/rig/decision.go`), the reviewer's fire
  through the scheduler's `Delegate`, and starts the queue's and the
  reviewer's `Run` on the session's context; the site link joins the
  middleware chain only when a proposer stands.
- `store/decision` implements the row shapes; `cmd/rig` is the only
  package that wires them together.

## Gotchas

- `Recorder.Record` returns nothing on purpose: a store error never fails
  a call, and the swallow is loud only where the root wired a log.
- The confidence is a probability; the store refuses anything outside
  0..1, and the HTTP client drops such answers before they reach it.
