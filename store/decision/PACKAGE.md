# store/decision

## What it is

The decision store (SPEC_DECISION), Go over the generated substrate: one
sqlite file under the rig home (`decision/decision.sqlite`), scoped like
todo — rows carry the project scope, the file does not. A row per
decision: site, state (the site's own JSON), the typed question (JSON),
answer, confidence (null for a rule, which does not estimate), unsure
(false for a rule; the decide tool marks the choices whose confidence is
under one half), decider, session (null for a fire), status (`final`,
`pending`, `approved`, `denied`), reviewer, the reviewer's answer, the
outcome once known, scope, ts. The gates write `final` rows, the proposer
writes `pending` rows, the reviewer settles to `approved`/`denied` naming
itself, and `Outcome` writes the one fact that arrives later.

## What it includes

- `decision.go`: the package doc, `SchemaVersion` (2), `Statements` (the
  generated DDL plus the extra indexes), `FilePath`.
- `verbs.go`: `RecordFinal`, `Propose` (refuses a confidence outside
  0..1), `Pending` (the pending rows in id order; a row whose question
  JSON does not parse is skipped, never thrown), `Settle` (keys on
  `status='pending'`, so two reviewers racing settle once and the
  loser's write is a no-op), `Settled` (the most recent approved row,
  or denied row that carries a correction, for a site, question id and
  state — newest id first; a row whose question JSON does not parse or
  whose question id differs is skipped, never thrown; the denied
  answer is its correction), `Outcome` (refuses by name when the id is
  unknown). Ids are minted max+1 inside the caller's transaction; the
  state column is bounded at 4096 bytes at the boundary.
- `recorder.go`: the `decision.Recorder` adapter — the gates' seam. It
  fills the scope from the root when the row carries none, names the
  ctx session, bounds nothing else, and swallows store errors (said in
  the room through the wired `Voice`, a `broadcast.Member`): recording
  never changes a decision.
- `migrate.go`: the schema migration. `unsure` is schema version 2; the
  migration adds the column to a version 1 file (old rows read 0) and
  `store.Open` runs it at both open sites.
- `metadata/`: hand-written metadata; `ddl/` and `domain/` are generated
  from it (edit and regenerate, never hand-edit).

## How it is consumed

- `cmd/rig` opens the store (with the migration), wires the recorder
  into the five gates, the runner's `RunOpts.Decisions`, and the decide
  tool, and (with `decisionUrl` set) hands the proposer and the reviewer
  their sinks and sources.

## Gotchas

- A row without a scope is refused: the scheduler's pre-row skips (lock
  held, no crontab line, no job row) have no project and record nothing.
- `Pending` skips an unparseable question rather than failing the drain:
  one corrupt row must not block the review of the rest.
