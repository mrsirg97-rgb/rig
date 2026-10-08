# decision: rig's decisions, kept where a model can learn from them

rig decides all day — the gates refuse, the runner skips, bash calls run —
and none of it lands anywhere a decision model could train on. This spec
adds one seam, one store, and (opt-in) one proposer plus one reviewer. The
rule that names the shape: **an answer is a proposal an LLM reviews, never
an action.** Recording never changes a decision, and a store error never
fails a call.

## the seam (`decision/`)

Stdlib-only leaf beside `pathguard`, imported by the gate packages.

- `Question` is typed: `choice` (with `Choices`), `score`, `binary`.
- `Answer` carries `Value`, `Confidence` (the probability, 0..1) and
  `Decider` (who produced it).
- `Decider` is the seam a decision server implements:
  `Decide(ctx, state string, questions []Question) []Answer`.
- `Final` is the gate's recording (site, state, question, answer, decider,
  scope, session) and `Recorder` is the seam the gates hold: `Record(ctx, Final)`,
  returns nothing. A nil recorder records nothing; the wired one swallows
  store errors (said in the room when the root gives it a voice, 2.11.0),
  because the call's
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

The store answers before the model. The proposed state carries the
normalized command — one leading `cd <path> &&` (or `;`) stripped, once,
and only when the path it names is the workspace the call named or under
it, a relative path resolved against the working directory, runs of
whitespace collapsed, ends trimmed — so twins of one command share a
state while a `cd` that leaves the workspace stays in the state: the risk
answer is workspace-relative by its own words, and stripping any `cd`
would twin `cd ~/Projects/rig && rm -rf build` with `cd /etc && rm -rf
build`. Before the decider is called, the queue asks the store what is
settled: the most recent approved or denied row for the same site,
question id and state answers a twin, which lands a final row (the
store's answer, confidence 1, decider `reviewed`) and proposes nothing —
a question whose answer is already settled is never asked again, and a
denied row's answer is its correction. A store error on the read falls
through to the proposer: the read fails open, the queue never blocks.

### the pack site (2.10.0)

The code map's task pack scores each candidate with one yes/no — "Does
this symbol matter for the task?" — one request per candidate through
the exported fan-out (`FanOut`, the seam decide's per-item fan-out
already is), the kernel's Parallel the bound, the state on the wire the
task and the item (the symbol's live signature and file). The pack
scorer holds the sink: every answered candidate is one pending row
(site `pack`, the server as decider), the reviewer settles them at turn
end as it settles bash rows, and a deny names the right answer. A
transport error refuses the score and writes no rows; a reply that
skips a candidate hides it — the pack lists the server's declines by
name for the model to judge and shows the never-answered nowhere.

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

When `decisionUrl` is set, the decide tool joins the menu and its
registry description carries the trigger; the system prompt stays
byte-identical with or without it.

## the reviewer

Only an interactive session reviews: headless workers and fires propose and
never review. A landing marks the reviewer dirty; the session's turn end is
the wake — since 2.11.0 a closure posted on the kernel's engine at
`rig.PriorityReview`, the lowest rung, so a bite starts only when the
operator's input, the turn's events and the fleet's messages have all
run (SPEC_EVT 8); the bite takes its rows on the loop, fires in a
goroutine and posts the settle back at the same priority; the fire rides
a context of its own, a child of the session's, and the operator's input
ends it — the frontend wrap's `Input` return and the idle interrupt call
`Halt` (2.14.4), an ended bite settles nothing, closes its phase naming
`interrupted`, and leaves the rows pending for the next landing; before that a
size-one channel on a goroutine of its own — no timer, no poll — and a turn end with
nothing landed costs nothing.

The wake is the metronome, not the work. Run j31 woke at a turn end with
264 pending bash rows and fired them all as one 177 KB prompt: the worker
reasoned 1,344 s over a slot the operator was about to want, died
`killed by signal 13` on 131 KB of streamed reasoning, settled nothing,
and re-fired the same batch at the next turn end while the operator
typed. The wake was right; the bite was wrong. One fire now takes the
oldest rows up to `reviewBatch` (settings.json, 2.9.4: rows per fire;
the default is 3 since 2.11.0, down from 10: a bite is a glance at the
queue mid-work, three at most, and the backlog converges across turn
ends rather than in one sitting), settles what the fire answers, and
leaves the rest
pending for the next turn end, so the backlog converges in bites while
the slot stays between turns. `reviewBatch` 0 leaves the reviewer off
while proposals keep landing; a negative or a non-integer refuses at
start, naming the key.

Two ceilings stay underneath the batch, never above it:

- The reply. The fire answers with one `verdict` tool call per row, so
the rows per fire are bounded by the reviewer row's max output tokens
over a verdict call's token cost — the cost derived from the call's own
arguments at their worst (a full-width id, a correction at the
1,024-byte cap), never a constant.
- The window minus its reserve, at four bytes to the token. A row that
cannot fit alone still goes, so one huge row cannot wedge the queue.

A bite that answers rows and leaves the backlog marks the reviewer
dirty, so a quiet turn end with a backlog still takes the next bite; a
fire that answers nothing waits for the next landing, so a garbage fire
cannot spin.

The fire is a headless worker the root wires through the scheduler's
`Delegate` (which waits on the free-slot gate), jailed like a swarm
worker. Through 2.12.1 it had the verdict tool alone, and on pack rows it
guessed: row 1960's fire reasoned "this is genuinely ambiguous without
the code" over a state that was one line, the task and the symbol's
file:line, and judged anyway. Since 2.12.2 the fire has `read` and `rem`
beside `verdict`, and the contract says to look before judging a row
about code and to name what was read in the reason. A grounded fire is
several turns, so a bite is slower; the batch of three is what keeps it a
glance.

A fire reviews the rows it can see. A row's `scope` is the project's key
(`scope.Key`), not a path, so a fire in one project cannot open another's
files: the bite takes rows of the session's scope or `global`, oldest
first up to `reviewBatch`; rows of other scopes stay pending for a session
opened in their project, and the drain's report counts them (`n wait for
their project`). The filter is one predicate on the pending set, not a
second queue. The fire
names no model: it resolves to the resident model's row as a scheduled
fire does (2.5.3), with the session's active model as the fallback when
nothing is resident; it never fires on the settings default while another
model is resident (2.8.3). And it thinks cheap: the spawn asks for the
row's lowest `efforts` level as `-effort` (2.14.2), accepting or
rejecting one row is a glance — through 2.14.1 the worker thought at the
row's default, xhigh on the operator's Qwen row, to judge a row.

Two fixes ride the fire (2.9.4). The headless worker (`-p`) ignores
SIGPIPE, so a broken stderr costs the reasoning stream and never the
run. And the fire's error carries the delegate's reason — the
spawnReason, the signal — and the run log path joined onto the scheduler
home, so the death is readable instead of a bare `exit -1`.

The prompt reaches the fire on stdin (`rig -p -`, 2.9.3): on the
operator's box 262 pending bash rows made a 176 KB prompt, over Linux's
128 KiB cap on one argument, and every fire died with `argument list
too long` until the carrier moved.

It answers with the `verdict` tool, one call per row naming the row
(2.11.0; through 2.10.x it was one `verdict: <id> approve|deny <answer>`
line per row scraped from stdout). Each call crosses the fleet pipe as
`core.Verdict` published as the fire's minted member; the reviewer hears
them on its `rig.MemberDecision` member on the loop and settles after the
fire ends. Last naming wins, an unnamed row stays pending, a reject
without a correction is not a verdict (the tool refuses it before it
crosses). A drain reports what it fired, what settled, and what stays
pending; the row's reviewer name is the model that reviewed: the one the
fire resolved to, carried back on the delegate's result. The fire runs
bare: no report-back brief; its tools are read, rem and verdict. The
bite is a `core.Phase` named `reviewing` (2.11.7): opened when the fire
starts, its deltas the fire's own reasoning crossing the fleet pipe and
renamed by the reviewer, closed at settle with `n rows settled` or the
fire's error; the TUI shows it in the indicator's row with the elapsed
time and streams the thinking under it.

Nothing the queue or the reviewer has to say reaches stderr while a
frontend owns the screen: a dropped proposal, a decide error, a store
error, a fire that was refused all travel as a `core.Notice` (source
`decision`) through the frontend's `Notify`, rendered as one dim line by
the TUI, a `notice` frame by the dashboard, a `rig:` line on stderr by a
headless run. Before 2.8.3 these were `Fprintln(os.Stderr)` over the
TUI's frame; the torn footer the operator saw at every turn end was the
reviewer being refused and printing it.

## training (2.13.0)

The rows the reviewer settles are gold labels, and until 2.13.0 they
trained nothing: the pipeline that fine-tuned the decision model lived
outside rig (`~/laya/retrain_rig.py`), read rig's store through a
read-only sqlite handle, and its output — the checkpoint the decision
server serves — was whatever that script's last run left. The evidence
that made this worth owning in: 210 settled rows (bash `risk`, pack
`matter`) with gold labels and 100-odd corrections with rationales; the
base Laya at 127.0.0.1:8095 scores 38% on held-out bash against a
constant 59% and carries no pack signal at all (mean p(yes) 0.11 on
relevant against 0.13 on not); on 2026-10-06 a full fine-tune of
`convaiinnovations/laya` on 167 of those rows, 4 epochs, CPU, 281 s,
scored 81% bash and 82% pack on 43 held-out rows with p(yes) 0.33
against 0.03. Head-only training (`--freeze-encoder`) equals the base
model: the encoder must move, so the gradient step is Python, never Go,
and Go never imports a tensor library.

### the zone (`train/`)

One trainer is one Python file under the rig home's `train/`, the name
the filename stem (`laya.py` first), beside `pending/` and `disabled/`
subzones, loaded through the machinery the plugins share (SPEC_PLUGINS,
the 2.13.0 amendment): the same filename rule, the same contract check
with a different contract, the same zone moves. The trainer contract is
two callables: `train(rows_path, out_dir) -> report` and
`evaluate(checkpoint, rows_path) -> report`, where a report is JSON —
per-question `n`, `correct`, `accuracy`, and for a noul question the
mean predicted p(true) by gold class (`pTrue.goldTrue`,
`pTrue.goldFalse`), the two numbers that separated signal from none
above. A train report carries one field more, `checkpoint`: the
directory it wrote under `out_dir`.

Trainers do not load into the session kernel: torch is two gigabytes
and the kernel's venv is not where it belongs. The zone is read through
its own interpreter, settings `trainPython` beside `decisionUrl`
(empty refuses the run, naming the key), one kernel per run — so
discovery is per-run, which is the reload a long-lived process would
have needed.

### the export, the split, the constant

`rig decision train <trainer>` is the only runner and it is a headless
command job, never a turn. It reads the settled rows — approved → the
proposer's answer is gold; denied → the reviewer's label parsed from
`reviewer_answer` free text (the regex `~/laya/retrain_rig.py` carried
is the start, ported to Go; every rationale shape in the live store's
history parses against it) — unparseable rows counted and skipped, not
guessed. The question id and kind come off the row's stored question
(the legacy `yesno` reads as binary); a kind the laya wire does not
speak (a score) is counted and skipped. Rows leave in laya's shape —
`{state, questions: {<id>: {type: choice|noul, instructions,
criteria}}, expected}` — under the rig home's `decision/train/<run>/`
as `rig-train.jsonl` and `rig-heldout.jsonl`.

The split is stratified and deterministic: rows grouped by question id
and gold label, each group ordered by (ts, id), every fifth row
(zero-based) held out, the rest training. Same rows in, same split out;
no seed, no shuffle.

The constant baseline is computed, not assumed: per question the
majority gold label of the train rows (ties break to the label that
sorts first), scored against held-out; for a noul question its p(true)
is the train prior, the same mass on both gold classes — the shape "no
signal" takes. It scores the same held-out rows the candidate and the
incumbent score, so the three reports share one table.

### the run, the table, the promotion

The run discovers the zone, calls the trainer's `train` (the cell bound
is the kernel's max cell timeout, 600 s; the real bound is the
scheduler's per-job timeout), then `evaluate` three ways on the same
held-out rows: the candidate's checkpoint, the incumbent, and the
constant. The incumbent is the currently served checkpoint, read from
the unit file settings `decisionUnit` names (`~/.config/systemd/user/
laya.service`): the `Environment=` line carrying
`RIG_DECISION_CHECKPOINT` (`%h` expanded). Since 2026-10-06 the server
is `~/laya/serve_rig.py` under the operator's `laya` user unit, and the
incumbent is `~/laya/ft-rig-20261006-1658` (81% bash, 82% pack
held-out). With no unit named, the run scores candidate against the
constant and promotes nothing — there is nothing served to beat.

Promotion is a measured win, never a loss: the candidate's held-out
accuracy must be strictly greater than the constant's AND the
incumbent's on every question the held-out rows carry; a question
missing from any report is not a beat. The p(true) means ride the
report and the table as the signal's evidence; they never gate.

One `core.Notice` (source `decision`) carries the held-out table — the
three reports per question and the promoted word — and the run is
recorded in the decision store's `trainings` table: started, trainer,
rows, skipped, split, run dir, candidate checkpoint, the three held-out
reports, promoted or not. A run that dies before the three reports
exist records nothing; the job's own status carries the death.

Promotion writes the checkpoint path the decision server loads: the one
`Environment=` line in the unit file named by `decisionUnit` is
rewritten, and the reply names what the operator runs — `systemctl
--user daemon-reload && systemctl --user restart laya`. rig restarts no
service it does not own: the reload is printed, never executed.

### the doors

`decision train <trainer>` (the slash command) enqueues the run: a
scheduler command job (`<rig> decision train <trainer>`, cron `once`,
at the next even minute at least two minutes out — the crontab's minute
granularity is the margin), named `decision-train-<trainer>-<tag>`. The
nightly is the operator's one line — a command job on a 5-field cron
running the same command (docs/SETUP.md). Nothing trains inside a turn:
the door only enqueues, and the command the job runs is the CLI
subcommand.

## testing

- reviewer scope: a bite sees its own project's rows and global ones,
  never another project's; the drain names how many wait for their project.
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
  corrected answer; a partial reply leaves the unnamed row pending; a bite
  takes the oldest rows up to the batch and the rest stay pending for the
  next turn end; 264 rows against a batch of 10 bite across 27 wakes, 26
  of ten and one of four, oldest first; a quiet turn end with a backlog
  still takes a bite; `reviewBatch` 0 wakes and fires nothing while rows
  keep landing; a batch above the reply ceiling is cut to it; the window
  minus the reserve holds under a batch that fits everything; a max output
  under one verdict line still fires one row; a fire that answers nothing
  waits for the next landing; a landing marks the reviewer dirty and a
  turn end with none costs nothing; an in-flight bite ends when the
  operator's input lands or `Halt` is called, settling nothing, closing
  its phase `interrupted`, the rows staying pending; a negative or non-integer batch
  refuses at start, naming the key; a no-tools run executes nothing,
  plugins included.
- the delegate: three labels group correctly and an item whose top
  probability is under one half comes back in full under unsure; an
  over-size list refuses before any request; one request per item rides
  the wire with the item as the state; a server error refuses and writes
  no rows; unset `decisionUrl` leaves the menu, the wire sha and the
  system prompt unchanged, set it and the tool sits in the live table,
  the guideline joins, and each item writes one store row; the migration
  adds `unsure` to a version 1 file.
- the wire: the tool menu and the wire sha do not move.
- training: the export parses every reviewer rationale shape in the
  live store's history, pinned as fixtures; unparseable rows count and
  skip; an approved row takes the proposer's answer, a denied row the
  parsed correction; the split is stratified and deterministic (same
  rows in, same split out, every fifth of each question-and-label
  group); the constant baseline is computed from the train rows, never
  assumed; the promote rule needs a strict win over the constant AND
  the incumbent on every question and a missing question is not a beat;
  a fake trainer (a Python file in the test's scratch home returning
  fixed reports) drives the promote and the no-promote both ways; the
  trainer contract check refuses a file missing `evaluate`; the unit
  rewrite touches the one `Environment=` line and prints the restart,
  never runs it; an empty `trainPython` or a promotion with no
  `decisionUnit` refuses loudly naming the key; a trainings row
  round-trips the three reports and the promoted word.

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
