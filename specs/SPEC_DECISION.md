# decision: rig's decisions, kept where a model can learn from them

rig decides all day — the gates refuse, the runner skips, bash calls run —
and none of it lands anywhere a decision model could train on. This spec
adds one seam, one store, and (opt-in) one proposer plus one reviewer. The
rule that names the shape: **an answer is a proposal an LLM reviews, never
an action.** Recording never changes a decision, and a store error never
fails a call.

## the seam (`decision/`)

Stdlib-only leaf beside `pathguard`, imported by the gate packages.

- `Question` is typed: `choice` (with `Choices`), `score`, `yesno`.
- `Answer` carries `Value`, `Confidence` (the probability, 0..1) and
  `Decider` (who produced it).
- `Decider` is the seam a decision server implements:
  `Decide(ctx, state string, questions []Question) []Answer`.
- `Final` is the gate's recording (site, state, question, answer, decider,
  scope, session) and `Recorder` is the seam the gates hold: `Record(ctx, Final)`,
  returns nothing. A nil recorder records nothing; the wired one swallows
  store errors (loud when the root gives it a log), because the call's
  result must be untouched by the store.

## the store (`store/decision/`)

One sqlite file under the rig home, `decision/decision.sqlite`, scoped like
todo: rows carry the project scope, the file does not. Lift generates
`ddl/` and `domain/` from `metadata/` (hand-written, never edited by
machine); ids are minted max+1 inside the caller's transaction, ts is the
caller's clock, session is nullable (a fire has none).

A row per decision: `site` (approve, perm, paths, guard, scheduler, bash),
`state` (what the decision was about, JSON), `question` (the typed question,
JSON), `answer`, `confidence` (null for a rule; a rule does not estimate),
`decider`, `session`, `status` (`final`, `pending`, `approved`, `denied`),
`reviewer`, `reviewer_answer`, `outcome` (null until known), `scope`, `ts`.

- The gates write `final` rows: approve the ask's verdict (yes and no — the
  operator's label is the gold one), perm a denial, paths an expansion it
  applied, guard a bound or round-cap refusal, the scheduler a skip (a fire
  past the job-row read; the pre-row skips carry no scope and no row).
  Allowed calls write nothing: the allowlist's yes is the default path, and
  one row per tool call is noise, not signal.
- `Settle` moves `pending` to `approved` or `denied` and names the reviewer;
  the correction rides `reviewer_answer`. It keys on `status='pending'`, so
  two reviewers racing settle once and the loser's write is a no-op.
- `Outcome` writes the one fact that arrives later, whether the answer held.

## the proposer (settings `decisionUrl`)

Unset: none — no queue, no reviewer, no middleware link, the chain and the
wire byte-identical. Set: an HTTP `Decider` speaking Laya's wire —
`POST <decisionUrl>/v1/systemone` with `{"state": ..., "questions":
{"<id>": {"type": "choice", "instructions": ..., "criteria": ...}}}` — a
choice's criteria is the map of label to what it means, a score's is the
ordered criteria list, a yes/no rides as type `noul` with no criteria —
replying `{"answers": {"<id>": {"type", "choice", "probabilities", ...}}}`
beside the model, the usage, and the routing the decode ignores. The
confidence a row keeps is the mass on the value the answer names: a
choice's is its label's entry in `probabilities`, a noul's is the mass on
the side its probability names (yes at 0.5, no below), a score's is the
top entry; `answer_confidence` is not read. The decider recorded is the
URL's host. One
site: every `bash` call gets a pending risk proposal — `choice: safe,
changes, dangerous`, each label described on the wire — over the command,
the workspace, and how the call ended, written after the call returns. The
call never waits on it: the site enqueues on a bounded channel and one
goroutine decides and writes. A full queue drops the proposal (a proposal
is not a decision), and a decider error drops it loudly.

## the reviewer

Only an interactive session reviews: headless workers and fires propose and
never review. A landing marks the reviewer dirty; the session's turn end is
the wake — a size-one channel, no timer, no poll, and a turn end with
nothing landed costs nothing. The fire is a headless worker the root wires
through the scheduler's `Delegate` (which waits on the free-slot gate) with
no tools — it reads the rows and replies on stdout — jailed like a swarm
worker. One fire takes the pending rows oldest first up to what the
reviewer's model row leaves for a prompt (the window minus its reserve, at
four bytes to the token); the rest stay pending for the next turn end, and
a row that cannot fit alone still goes, so one huge row cannot wedge the
queue. It replies with one verdict line per row, parsed like the swarm
reviewer's:

    verdict: <id> approve
    verdict: <id> deny <corrected answer>

Lines scan in reply order, last naming wins, an unnamed or malformed row
stays pending, a deny without a correction is not a verdict. A fire that
settled something and left pending rows leaves the reviewer dirty, so the
next turn end takes the rest and a partial converges; a fire that settles
nothing waits for the next landing, so a garbage fire cannot spin. The
reviewer's name on the row is the model that reviewed.

## testing

- store: a final row round-trips with its decider; a proposal is pending;
  settle approves, denies with the corrected answer, and is a no-op on a
  settled row; pending lists pending only; outcome writes once.
- gates: each writes one final row with its decider (approve both verdicts,
  perm the denial, paths the expansion, guard the bound and the round cap,
  the scheduler's skip); a store that cannot be written leaves the call's
  content and error byte-identical to the no-recorder run.
- proposer: no `decisionUrl`, no link in the chain and nothing proposed;
  a bash call's proposal is pending and the reply is unchanged; the HTTP
  client speaks Laya's wire shape (the fake server replies the full Laya
  payload, unknown fields included); a reply with probabilities and no
  `answer_confidence` stores the mass on the value named, and an
  out-of-range confidence drops.
- reviewer: three pending rows are reviewed in one fire; a deny stores the
  corrected answer; a partial reply leaves the unnamed row pending; a fire
  that settles nothing waits for the next landing; a landing marks the
  reviewer dirty and a turn end with none costs nothing; a fire takes the
  oldest rows that fit the window minus the reserve and the rest stay
  pending for the next turn end; a no-tools run executes nothing, plugins
  included.
- the wire: the tool menu and the wire sha do not move.

## scope

New: `decision/` (the seam, the HTTP decider, the queue, the reviewer, the
bash site), `store/decision/` (metadata, generated, verbs), this spec, and
the two package docs. Changed: the five gate constructors (an optional
recorder, variadic, so the existing call sites stand), `store/scheduler`'s
`RunOpts` (the skip's recorder), `config` (`decisionUrl`), `cmd/rig` (one
store open, the recorders, the opt-in wiring), and the docs. `core/` and
`loop/` are untouched: the gates and the site are ordinary middleware, and
the store is an ordinary leaf the root wires once.
