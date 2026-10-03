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

## the decide tool (the delegate)

The model reads what it must, but a step that is "which of these items
match X" does not need the items in context: thousands of bash calls and
reads feed whole outputs into a context the answer never used. With
`decisionUrl` set, the live tool table carries one built-in entry named
`decide` — a native of the table, not a plugin — and the fixed tool menu
and the wire sha do not move with it unset. The name is native to the
plugins: a plugin file named `decide` collides, and manual mode does not
ask for a decide call.

The arguments are one typed question — a choice with a description per
label, a yes/no, or a score with its ordered criteria — and a list of
items. The tool sends one request per item through the 2.7.0 proposer
(the state on the wire is the item, the question rides once per request
keyed by its id), up to the kernel's Parallel at once, and replies
grouped: for each label, the items that got it (numbered from 1, with
the first line), then the unsure items in full for the model to judge
itself. A choice is unsure when its top probability is under one half; a
yes/no is never unsure, so a question that needs a middle uses a choice.
The bound holds ahead of any I/O: the items' total stays under the read
ceiling (the read tool's, shared as one constant), and a list at it
refuses before a request goes out. Bad arguments refuse the same way.

A server error is a refusal the model reads, never a dead turn: nothing
was sorted and no row was written, so the retry starts clean. Every
answered item is recorded — site `decide`, the server as decider, its
confidence, unsure marked — as `final` rows; none are queued for review.
The store carries the mark as the `unsure` column (schema version 2; the
migration adds the column to a version 1 file, old rows read 0), null
confidence and false unsure for a rule that does not estimate.

When `decisionUrl` is set, one guideline joins the system prompt through
the GuidelineContributor seam: when a step is sorting or filtering many
items against a question you can state, hand the items to decide instead
of reading them. Unset, the system prompt is byte-identical.

## the reviewer

Only an interactive session reviews: headless workers and fires propose and
never review. A landing marks the reviewer dirty; the session's turn end is
the wake — a size-one channel, no timer, no poll, and a turn end with
nothing landed costs nothing. The fire is a headless worker the root wires
through the scheduler's `Delegate` (which waits on the free-slot gate) with
no tools — it reads the rows and replies on stdout — jailed like a swarm
worker. The fire names no model: it resolves to the resident model's row
as a scheduled fire does (2.5.3), with the session's active model as the
fallback when nothing is resident; it never fires on the settings default
while another model is resident (2.8.3). One fire takes the pending rows oldest first up to what the
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
reviewer's name on the row is the model that reviewed: the one the fire
resolved to, carried back on the delegate's result.

Nothing the queue or the reviewer has to say reaches stderr while a
frontend owns the screen: a dropped proposal, a decide error, a store
error, a fire that was refused all travel as a `core.Notice` (source
`decision`) through the frontend's `Notify`, rendered as one dim line by
the TUI, a `notice` frame by the dashboard, a `rig:` line on stderr by a
headless run. Before 2.8.3 these were `Fprintln(os.Stderr)` over the
TUI's frame; the torn footer the operator saw at every turn end was the
reviewer being refused and printing it.

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
- the delegate: three labels group correctly and an item whose top
  probability is under one half comes back in full under unsure; an
  over-size list refuses before any request; one request per item rides
  the wire with the item as the state; a server error refuses and writes
  no rows; unset `decisionUrl` leaves the menu, the wire sha and the
  system prompt unchanged, set it and the tool sits in the live table,
  the guideline joins, and each item writes one store row; the migration
  adds `unsure` to a version 1 file.
- the wire: the tool menu and the wire sha do not move.

## scope

New: `decision/` (the seam, the HTTP decider, the queue, the reviewer, the
bash site, the decide tool), `store/decision/` (metadata, generated, verbs,
the migration), this spec, and the two package docs. Changed: the five gate
constructors (an optional recorder, variadic, so the existing call sites
stand), `store/scheduler`'s `RunOpts` (the skip's recorder), `config`
(`decisionUrl`), `cmd/rig` (one store open, the recorders, the opt-in
wiring, the decide tool in the live table and the guideline link), and the
docs. `core/` and `loop/` are untouched but for the kernel's parallel
constant: the gates, the site, and the decide tool are ordinary middleware
and tools, and the store is an ordinary leaf the root wires once.
