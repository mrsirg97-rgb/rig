# decision

## What it is

The decision seam (SPEC_DECISION): the typed vocabulary of a rig decision
and the two interfaces a decision consumer holds — `Decider`, the seam a
decision server implements (`Decide(ctx, state, questions) -> answers`), and
`Recorder`, the seam the gates hold to record a final row. A question is
typed (`choice` with its choices, `score`, `binary`); an answer carries a
value, a confidence (the probability, 0..1), and its decider (who produced
it). Stdlib-only leaf; no imports of the stores; its one leaf import is
`pathguard`, whose containment answer the bash site's strip borrows.

## What it includes

- `decision.go`: the types (Question, Answer, Final), the two interfaces,
  the kind/status/site vocabulary as constants, and the question
  constructors (`Choice`, `Score`, `Binary`). A choice carries a
  description per label (`Description`), a score carries its ordered
  criteria list (`Criteria`).
- `http.go`: the HTTP `Decider` (settings `decisionUrl`), speaking
  Laya's wire: POST `<decisionUrl>/v1/systemone` with the state and the
  questions keyed by id (`type`, `instructions`, `criteria` — the map of
  label to meaning for a choice, the ordered list for a score, nothing
  for a noul), the answers keyed by id and decoded tolerantly (the
  model, the usage, the routing, and each answer's action block are
  ignored). An answer's confidence is the mass on the value it names:
  a choice's is its label's entry in `probabilities`, a noul's is the
  mass on the side its probability names (yes at 0.5, no below), a
  score's is the top entry; `answer_confidence` is not read. The
  decider recorded is the configured name, else the URL's host. An
  answer whose confidence is not a probability, whose value or its
  mass is missing, or whose type is unknown drops; the reply body is
  capped at 1 MiB.
- `queue.go`: the proposal queue: `Propose` enqueues on a bounded channel
  (`QueueCap`), `Run` is the one goroutine that decides, writes through
  the `Sink`, and calls `land` on a landing — a landing marks the
  reviewer dirty; the wake is the session's turn end. `land` is nil
  where nothing reviews. Before the decider is called the queue asks
  the store what is settled (`Settled`, the one read it holds): the
  most recent approved or denied row for the same site, question id and
  state answers a twin, which lands a final row (the store's answer,
  confidence 1, decider `reviewed`) through the `Recorder` and proposes
  nothing — the model is never asked a question the store has answered,
  and the landing marks nothing, since a final row is not review work.
  The queue holds two write doors, the `Sink` for pending rows and the
  `Recorder` for finals. A store error on the read says itself and
  falls through to the proposer: the read fails open, the queue never
  blocks. The settled read and the recorder are
  constructor arguments and are refused nil — a hit with either missing
  would vanish the proposal. A full queue drops loudly (a proposal is
  not a decision); a decider or sink error drops loudly and lands
  nothing.
- `review.go`: the reviewer: `Reviews` is the store's face for review
  rows — the settled read the queue holds before its model plus
  `Pending` and `Settle`. A landing (`Land`) marks it dirty, the
  session's turn end (`Wake`, through `TurnEnds`' frontend wrap) is the
  wake, and a turn end with nothing landed costs nothing. Since 2.11.0
  the wake posts the bite on the kernel's engine at `rig.PriorityReview`,
  below the turn and the fleet, so it starts only when nothing else is
  queued; the bite takes its rows on the loop, fires in a goroutine (the
  loop never waits on the world), and the completion posts the settle
  at the same priority. The fire rides a context of its own, a child of
  the session's, and `Halt` ends it: the wrap's `Input` return (the
  operator's line) and the root's idle interrupt call it, so the bite
  in flight dies when the operator speaks instead of running headless
  under the turn that took the slot. A fire the halt kills — its
  context dead and its return an error — settles nothing, closes its
  phase naming `interrupted`, and leaves the rows pending for the next
  landing; the fire's context is released when the bite ends, whatever
  the ending. One bite is posted at a time.
  There is no `Run`
  goroutine; the engine and the context are constructor arguments. `Drain`
  is the same take-fire-settle done synchronously, for a caller that
  wants the result. The old sentence: `Run` is the
  loop, `Drain` the pass — the pending rows oldest first, up to what the
  reviewer's model row leaves for a prompt (the window minus its
  reserve, at four bytes to the token; a row that cannot fit alone
  still goes), the rows of the reviewer's scope or `global` only (2.12.2:
  a row's scope is a key, not a path, so a fire reads code only in its own
  project; the rest wait for a session there and the drain counts them),
  one fire through the `Fire` seam, which takes the minted
  member the fire's worker speaks as; the worker's `verdict` tool calls
  arrive as `core.Verdict` messages on the reviewer's member and settle
  on the loop after the fire ends, last naming wins, a reject without a
  correction is not a verdict, unnamed rows stay pending. The reviewer
  takes the room in its constructor (2.11.0); nothing parses stdout. A
  bite is a `reviewing` phase (2.11.7): the reviewer opens it, turns the
  fire's reasoning deltas (the voice it minted, heard on its own member)
  into phase deltas, and closes it at settle with the count, the fire's
  error, or `interrupted` when the operator ended it, so the operator
  watches the review think and sees it check out. A
  fire that settled something and left pending rows leaves the reviewer
  dirty, so the next turn end takes the rest; a fire that settled
  nothing waits for the next landing.
- `site.go`: the bash site: one link at the chain's inner end; after a
  bash call returns it proposes the risk question (choice: safe,
  changes, dangerous, each label described) over the command, the
  workspace, and how the call ended. The command is normalized first
  (`normalize`, with the call's workspace and the site's cwd): one
  leading `cd <path> &&` (or `;`) is stripped — once, and only when the
  path it names is the workspace the call named or under it, a relative
  path resolved against the working directory, a `~` against the home —
  so twins of one command share a state while a `cd` that leaves the
  workspace stays in the state: the risk answer is workspace-relative by
  its own words, and stripping any `cd` would twin `cd
  ~/Projects/rig && rm -rf build` with `cd /etc && rm -rf build`. Runs
  of whitespace collapse and the ends trim either way. The call never
  waits: the proposal rides the queue's channel and returns at once.
- `pack.go`: the pack scorer (2.10.0, SPEC_DECISION's pack site): the
  code map's task pack holds one and hands it the candidate items; each
  is scored with one yes/no through the fan-out, the task and the item
  as the state on the wire, every answered candidate one pending row
  (site `pack`, the sink it holds) with the reviewer marked dirty per
  row. The verdict says yes (a confident affirmation), unsure (decide's
  rule: an answer whose confidence is under one half — the model judges
  it) or neither (a confident no or no answer; hidden). A transport
  error refuses and writes no rows; a sink error is loud and never
  fails the score.
- `decide.go`: the decide tool, the model's delegate door (SPEC_DECISION,
  the delegate section): one typed question — a choice with a
  description per label, a yes/no, or a score — and a list of items. As
  of 2.12.6 the tool is its interface: `NewDecide` returns `Decide` (the
  struct is unexported), `Exec` decodes the args and routes on `kind`,
  and one method carries each question kind — `Choice(ctx, prompt,
  labels, items)`, `Binary(ctx, prompt, items)` and `Score(ctx, prompt,
  criteria, items)`. The three kinds are the tool's three verbs: the
  prompt, label and criterion refusals live in the kind that owns them,
  the item refusals and the read ceiling in the shared run, so a Go
  caller and the model hit the same checks. `Label` is the exported
  label (the wire's `{label, description}`), where the question kinds
  themselves stay the package's `Choice`/`Score`/`Binary` constructors.
  One request per item through the `Decider` (the state on the wire is
  the item), up to the tool's `Parallel` at once, and the reply grouped:
  for each label the items that got it (numbered from 1, first line), then
  the unsure items in full for the model to judge itself. A choice is
  unsure when its confidence is under one half; a yes/no and a score are
  never unsure. The bound holds ahead of any I/O: the items' total stays
  under the read ceiling (`file.ReadCap`, the read tool's), and bad
  arguments refuse before a request goes out. A server error refuses —
  nothing was sorted and no row was written. Every answered item is
  recorded through the `Recorder` (site `decide`, final, the server as
  decider, its confidence, unsure marked); a nil recorder records
  nothing. The per-item fan-out is exported as `FanOut` — one request
  per state, bounded by its parallel, a reply that skips an item left
  silent for the caller to judge. `Guide` is the one-link
  `ToolMiddleware` whose `Guidelines()` joins the system prompt when the
  tool is wired.

- `train.go`: the training surface (2.13.0, SPEC_DECISION's training
  section): `Report` (per-question `n`, `correct`, `accuracy`, and for a
  noul question `pTrue`, the mean predicted p(true) by gold class),
  `ParseReport` (the shape check: accuracy a probability, n positive, a
  report with no questions is a train report and legal), `GoldRow` and
  `GoldRows` (the settled rows in laya's shape — an approved row takes
  the proposer's answer, a denied row the correction parsed by
  `ParseReviewerLabel`, whose regex is the one `~/laya/retrain_rig.py`
  carried; unparseable rows, a score, and a label outside the choices
  count and skip), `Split` (stratified and deterministic: groups by
  question and gold label, ordered by row id, every fifth held out),
  `ConstantReport` (the majority label of the train rows scored on
  held-out, a noul's p(true) the train prior on both gold classes —
  computed, never assumed), and `Beats` (the promotion rule: strictly
  greater than the constant AND the incumbent on every question any of
  the three reports carries; a missing question is not a beat, an empty
  report promotes nothing). Pure; the orchestration is `cmd/rig`'s.
  A trainer whose stem names a package it imports must drop that key
  from `sys.modules` first: the discovery cell registers the file under
  its stem, and the shadow breaks the package's own submodule imports.

## How it is consumed

- The gate packages (`middleware/approve`, `middleware/perm`,
  `middleware/guard`, `middleware/paths`) and `store/scheduler`'s runner
  hold an optional `Recorder` (variadic, nil records nothing) and call it
  at their decision points.
- `cmd/rig` wires the store as the queue's `Sink` and the reviewer's
  `Reviews` (the adapters in `cmd/rig/decision.go`), the reviewer's fire
  through the scheduler's `Delegate` with `NoTools` (the worker runs
  `-allow none` and no report-back) and the sandbox the operator chose,
  and starts the queue's and the reviewer's `Run` on the session's
  context. Only an interactive session gets the reviewer; a headless
  worker gets the queue with no `land` and proposes and never reviews.
  The site link joins the middleware chain only when a proposer stands.
- `store/decision` implements the row shapes; `cmd/rig` is the only
  package that wires them together.

## Gotchas

- `Recorder.Record` returns nothing on purpose: a store error never fails
  a call, and the swallow is loud only where the root wired a voice (a
  `broadcast.Member`; the notice carries source `decision`).
- The confidence is a probability; the store refuses anything outside
  0..1, and the HTTP client drops such answers before they reach it.
  The confidence a row keeps is the mass on the value the answer
  names, read off `probabilities` — `answer_confidence` and the
  entropy `confidence` beside it are not read.
- `-allow none` (the delegate's `NoTools`) runs a worker with no tool at
  all: the allowlist denies every native tool and the plugin door is
  shut with it. An allow list that is nil means no tools; a run with
  tools keeps the door open.
