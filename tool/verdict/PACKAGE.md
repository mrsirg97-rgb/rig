# tool/verdict

## What it is

The reviewer's one word (2.11.0). A worker asked to review delivers its
decision by calling `verdict`, and the call crosses the fleet pipe as a
`core.Verdict` message published as the worker's member. Through 2.10.x
the verdict was a `verdict: ...` line the model had to write last and the
parent had to scrape from stdout; two parsers, two vocabularies, and a
slightly misspelled line counted as a dead worker.

## What it includes

- `Verdict`: the tool as its interface (2.12.6): `tool.Definition` (the
  words are the registry's `verdict` entry), `Exec` as the one JSON door —
  decode `{row?, accept, reason?}` strictly and route — and
  `Deliver(ctx, row, accept, reason)`, the one verb, named for what the
  registry says it delivers. The reject-without-a-reason refusal lives in
  the verb, before anything crosses, so a Go caller and the model are
  refused by the same words. One `core.Verdict{Row, Accept, Reason}` goes
  out on the transport as the transport's id; the reply is `recorded`.
  `New(fleet broadcast.Transport)` returns the interface; the struct is
  unexported.

## How it is consumed

The root registers it only when the process holds a fleet pipe
(`sched.Fleet()` found `RIG_FLEET`), as a conditional native beside
`decide`. The swarm adds `verdict` to a reviewer worker's allow list;
the decision bite runs bare with `verdict` as its only tool. The parent
side publishes the frame as the worker's member: the swarm supervisor
stamps it on the worker, the decision reviewer keys it by `Row`.

## Gotchas

- `row` is for a batch of recorded decisions; a swarm reviewer leaves it
  out, since the supervisor knows which task its worker holds.
- The transport is a constructor argument; a tool without a fleet is a
  construction error, never a silent no-op.
