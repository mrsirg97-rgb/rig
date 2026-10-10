# Changelog
## [2.14.16]: the copies go to the seam

A refactor release: no refusal moves and no wire byte moves, and the
branch carries `-refactor` — the freeze gate's own escape hatch doing
the one job it documented, named here in the PR. The tree loses its
hand-rolled copies to seams that already existed, and the tests lose
the assumptions that were never theirs alone.

The code (46,138 → 45,998) now holds one of each:

- one store open (`cmd/rig/stores.go`): `openStore` is the directory,
  the open, the quarantine line and the migration report, named by the
  store it opens — five hand-rolled blocks in main go, and with them
  the two spellings of the report line (an `Fprintln` with a live
  `%s`, an `Fprintf`), same bytes on the wire, one spelling now.
- one boot: `boot` and `noticePrinter` are the headless jobs'
  prologue — the rig home, the working directory, the config, the
  notices, and a fleet member that prints every notice to stderr.
  `runJob` and `decisionTrain` were two copies of each.
- one strict door (`tool/decode.go`): `tool.Decode` replaces five
  byte-identical `strictDecode`s across bash, file, delegate,
  verdict and view; view keeps its empty-payload default and rides
  the door like the rest. The loose tools stay loose — strictness is
  the security posture the strict five declared.
- one refusal row: `decision.Deny` records the row every gate writes
  — the site is both the row's site and its decider, because the gate
  speaks for itself — and `decision.FirstRecorder` is the middleware
  constructor's door for the variadic seam. Six copies across guard,
  perm, approve and paths go, nil checks included (a nil recorder is
  no row, by contract).
- one anon lift: todo's `mutate` takes the session, resolves it once,
  and hands the resolved name to its action — sixteen hand-rolled
  copies across the verbs, the reads and the append go. The two
  read-only paths take the one helper by name.
- one zone read: `plugins.List` is `Zone` with no zone join, and
  cmd/rig's reload is the `Ecosystem.Reload` it already held — the
  reload failure's prefix follows the ecosystem's voice (`plugin:
  reload:`), nothing pinned the old one.
- web loses `NewDefault`, `DefaultProxy` and `IPisPrivate`, dead in
  production; the pin tables stay, driving `publicAddr` directly.
- the exec door is named: `landlockExec` is the jailed worker's
  entry (the wall lands, the thread locks, the command replaces the
  process) instead of an anonymous block at the top of main.

The 30k aim is named, not reached, and the reachability is the
reason. The two big lifts left on the table are the generated domain
tail — 83 of 127 accessors are test-only, ~2,200 lines inside code
this pass does not touch — and an event-log leaf under todo and
scheduler's near-clone fold machinery (~300 lines, two compact paths
that must stay bug-compatible by hand). The recon pass's ranked list
of what remains — config's field table riding parse, merge and the
env ladder (~230 lines), the command picker harness (~100),
`sqlx.Transact` (~100), a `frontend/session` leaf under the cli/web
clone (~120), the output-cap leaf (~55) — is named in the PR for the
next pass.

The tests (73,424 → 66,829) ran one bar: cut a test when another pins
the same invariant through the same surface, when it asserts an
internal mechanism with no behavioral consequence, or when it pins a
dead production path. Half was the aim; the honest floor is what the
bar yields, because the protected set — refusal voices, wire and
golden bytes, journeys, races, tables — is the bulk of every cluster.
What the pass cut, cluster by cluster with the invariant and the
redundancy that justified each cut, is in the PR.

## [2.14.15]: the schema says what the code takes

The tool surface is the agent's world, and the descriptions had
drifted from what the code accepts in small ways that cost real
calls. This round's own todo queue refused five of six creates
because a link named a task that did not exist yet — the refusal was
strict by design, the prose never said so. The registry now speaks
the code, schema cells included.

- **a link names a task that exists** (`tool/todo`): the guidelines
  carry the clause — create first, then link; a link to a task that
  does not exist refuses — and `requires`/`blocks` say the target
  must already exist. The `action` description maps all fourteen
  verbs, `id` names the nine that take one, and `note` says reject
  refuses without a reason.
- **the scheduler schema carries workspace and n** (`tool/scheduler`):
  create and update take the workspace the job runs in and `runs`
  takes how many recent runs to list — both parsed by the code,
  absent from the schema, so an agent reading the wire could not pass
  them. `update` is named where it lives: in the `id` description and
  a gloss of what a partial rewrite keeps. The schema's stale claim
  that workspace must be gone went with it.
- **no undocumented cell left** (`tool/rem`, `tool/sessions`,
  `tool/web`, `tool/bash`): every property now carries a description
  — rem's action, verb and supersedes; sessions' action, project and
  n — the web action reads as the verb it is, and bash's workspace
  names its default. The params were live all along; the words were
  missing.

## [2.14.14]: the train lands the line run-job reads

The `/decision train` job never fired: the enqueue tagged its crontab
line with the scheduler store's directory while run-job looked the
line up under the rig home, so every fire recorded
`skip · no crontab line (drift)` — j145 and j215 both sat out, and
2.14.12's test pinned the fire time but never asked whether the line
run-job reads exists.

- **the enqueue tags with the rig home** (`cmd/rig`): the wiring hands
  `decisionTrainEnqueue` the one rig home main resolves, named
  `rigHome` like the `RunOpts.RigHome` run-job reads — one value,
  passed once, the same one the scheduler tool and every worker
  wiring already pass. The crontab tag format is untouched; the
  operator's other jobs keep firing.
- **the test asks whether run-job can find the line** (`cmd/rig`): the
  enqueue lands a training job against a fake crontab, the job runs
  the real `sched.RunJob` path with the homes main wires, and the
  fire must reach the spawn — not a drift skip. It failed first: the
  line carried the store dir's tag.
- **one `//` unescape** (`frontend/cli`): the steered slot and the
  typed delivery each ran the prefix check and `command.Unescape`;
  both branches now call one `inputText`, the path the existing
  steer and quiet-prompt tests pin.

## [2.14.13]: the specs read the tree again

The 2.14.10 audit's remaining spec/doc findings, amended in place
against the code — no Go moved but the version. The specs named rows
and shapes the code had outgrown: a model switch that writes the row
it was said not to touch, a settings block listing ten of twenty-five
fields, fixtures two generations of tooling retired, and the words
2.4.0 through 2.13.1 amended elsewhere but never here.

- **the row follows the switch** (SPEC_COMMANDS): `models <id>` writes
  the session row's `model` column — the "historical record" claim was
  never amended — and the usage-line refusals, the `sessions show`
  tool-row bullet, and decision 2's struct sketches are shape-current.
- **the settings block carries the keys it has** (SPEC_CONFIG): all 25
  fields listed; decision 3 tells the workers.json-era story the code
  tells (presence-only read, the `defaultJobModel` notice, nothing
  mints); decision 6's system-prompt formula carries the role segment
  `command.RoleProse` always emits; the layout names `parse.go` and
  `embed.go`, and the embedded JSON carries no `model` key.
- **the build spec describes the gate as built** (SPEC_BUILD): the
  documented install line (`tryrig.ai`) and its five paths; the CI
  decision names the inline vet+test, the measured-check, shellcheck
  over both files, and the three jobs; the pages job's build step and
  `workflow_dispatch` are named.
- **the wire block carries what crosses** (SPEC_CORE, SPEC_EVT):
  `EmptyTurn.Model`, `Compacted.Model`, and the `Compacting` event;
  the retry guard sits mid-chain as SPEC_HARDENING says; the layout
  names `policy/operator/`; the menu pin is `TestWireDump`'s
  arithmetic plus the wire-check wall; the engine options list
  `WithCapacity`.
- **the fleet-era words land** (SPEC_SWARM, SPEC_DELEGATE,
  SPEC_WORKERS): the router owns claiming (no worker poll), the
  `Stall`/`Slots`/`WaitBusy` fields are retired by name, model
  resolution is resident → default, the delegate's status rides the
  fleet pipe rather than a stream `Observe`, the no-recursion voice
  carries its em dash, and the graph tap is the chain's outermost
  link.
- **the gestures and the queue** (SPEC_TUI, SPEC_UX): the Esc stop is
  the two-step arm with its 2-second window; the layout names the
  package's 25 files; `create` is one item per call (`added tN` /
  `tN already there`) with the empty create refused at both doors;
  bash's trailing line names the workspace.
- **the counts and caps** (SPEC_PLUGINS, SPEC_SANDBOX, SPEC_HOSTED,
  SPEC_COMPACT, SPEC_DECISION): the native set is 15 with
  `plugins.max` named; the `/plugins` voices are the 2.8.3 list shape;
  the concurrency claims that survived their own 2.4.0 amendment are
  struck and the schema labels read v5/v7; the effort wire claim names
  the remote carve-out, the calibration clamp is `[0.5, 2.0]`, the
  summary floor carries its one-token guard, the phase the TUI shows
  is `summarizing`, the pack grammar describes the hand-pack fallback,
  the split orders by id, and the door's schema names its six actions
  with the toolset's `Tool`/`Plugin` seam.
- **the leaves name the shipped surface** (SPEC_SERVE, SPEC_WEB,
  SPEC_PYTHON, SPEC_DIFF, SPEC_STREAMLINE, SPEC_GROWTH): the phase-2
  no-fleet claims yield to the 2.4.0 amendment; the web const is
  `defaultMaxChars`; python's layout and interfaces name the shipped
  `Python` surface; diff's engine shape and the wire job replace the
  retired goldens; streamline's testing names the wire-check render.
- **the docs** (docs/SETUP.md, README.md): the theme slots carry
  `ember` and the seven effort levels; the models.json row names the
  retired `concurrency` key; the rem rows carry `index`/`pack`.

## [2.14.12]: the escape the steering slot skipped

The 2.14.10 audit's findings shipped as fixes: one escape one path
forgot, a retired route that answered as if it worked, a key the help
advertised and the parser refused, a dead chain the spec retired in
2.6.0, shapes written twice, a margin that landed short, and the
queued-delegate words the 2.14.9 doc pass missed.

- **the CLI's `//` escape survives a steer** (`frontend/cli`): a line
  typed into a live turn rode the steering slot raw, so `//home/ng/x`
  reached the model as `//home/ng/x` — the escape ran only on the
  quiet-prompt path, exactly the user it exists for. The slot's
  delivery unescapes like the quiet prompt's; the TUI's single consume
  point always did. Mid-turn commands still dispatch at re-entry.
- **the dashboard's todo loses the start door for good**
  (`frontend/web`, docs, specs): `/api/todo/start` sat in the method
  gate with no dispatch case, so a POST fell through the switch and
  answered a silent empty 200. The gate drops it — the POST is a named
  404 — and SETUP.md and SPEC_SERVE 15 name the two hands the page
  has.
- **`scheduler update` stops advertising the busy key** (`command`):
  the Sub hint and the usage line named `[busy <skip|force>]`, which
  the parser refused and the store retired; the advertised shape and
  SPEC_COMMANDS 8 name the six live keys.
- **the retired slot read is gone** (`store/scheduler`):
  `FleetCapacity` → `FreeSlots` → `slotRead` survived with zero
  callers against SPEC_WORKERS 2.6.0's "the slot read goes"; the gate
  fake's `/slots` fixtures and its never-asserted counter went with
  them.
- **the todo adapter carries no dead shapes** (`command`): the
  claim/accept/note-reject case groups were written twice in
  `todoArgs` and again in its error switch, and `isUpdateKey` had no
  caller; each appears once.
- **the decision train lands its named margin** (`cmd/rig`): the
  once-job took `now+2m` and the crontab took that exact minute, so a
  `:05:37` enqueue fired a minute and change out. The `at` now rounds
  up to the next even minute at least two minutes out, as SPEC_DECISION
  names it, and the crontab seam is a constructor argument so the test
  fakes it.
- **the queued-delegate words land** (specs, docs): SPEC_SWARM 7,
  SPEC_TUI 3a and USAGE's delegate paragraph name the 2.14.9 queue —
  the snapshot carries queued workers, the head inserts `N queued`, and
  a call past the cap answers the queued line.

## [2.14.11]: the worker window

Three workers on one slot at 180k each hold prompt-cache entries of
four and a half to five gigabytes; a 16 GB cache keeps three, and the
fourth evicts one, which re-reads 120k tokens. The cap on how many run
at once was half the budget; how deep each one runs is the other.

- **`workerWindow` caps a worker's window** (`config`, `models`,
  `cmd/rig`): settings `workerWindow: N` lowers the window of every
  process started as a worker (`RIG_DELEGATE`) to N, scaling reserve,
  keepRecent and maxTokens by the same ratio — huihui-alpha at 131072
  compacts near 93k with a 38k reserve. The session keeps its row; `0`
  or a value at or past the row's window leaves the row alone; negative
  refuses at load.

## [2.14.10]: the report that came back as a short write

A spec-audit worker ran nine and a half hours, wrote its fourteen-kilobyte
report, exited 0 — and the session read `delegate: spawn: spawn: short
write`, exit -1, 0s, and fired the group again. The spawn's output
capture keeps the first and last 128 KB of a child's stream; a write
that crossed the head's edge was trimmed to the room left and the
capture returned the trimmed length. `os/exec` copies the pipe with
`io.Copy`, which reads a short count as `io.ErrShortWrite`, stops
copying, and fails the run: the child's next write meets a closed pipe,
and whatever it printed after — the report — is gone. Every worker
that talks past 128 KB hit it at the crossing write; long workers
always do.

- **the capture accepts every byte it is handed** (`store/scheduler`):
  `capture.Write` returns the length it was given, keeping the head and
  tail it always kept. A worker that talks past the cap is a result
  again, its report in the tail.
- **the delegate band's call row takes the warn color**
  (`frontend/tui`): `#8 read specs/SPEC_HARDENING.md` paints like
  `auto`, the ` · ` and the age stay dim, the `—` before a first call
  stays dim; the text and its width do not move. The goldens move by
  that row only.
## [2.14.9]: the cap on the fleet

An audit fanned out eight delegates on one slot and ran nine hours. The
model was not slow: each worker's cached prefix was 1.5–4.4 GB at depth,
a 16 GB prompt cache holds five, and eight workers taking turns evicted
each one just before its turn came back. Every turn re-read 74–113k
tokens at ~565 tok/s — three minutes of prefill for half a minute of
decode, and under two of the nine hours were generation.

- **settings `maxWorkers`** (`config`): `0` (absent) is no cap; `N`
  runs at most N workers at once, the session's delegates and its swarm
  together, one counter held from spawn to exit (`store/scheduler`
  `WorkerCap`). A negative or non-integer value refuses at start.
- **a delegate past the cap queues** (`tool/delegate`): the call hands
  back `delegate: worker #n queued · N workers run at once (settings
  maxWorkers); it starts when one returns` and the worker starts when a
  slot frees, returning like any other. It never refuses for the cap;
  a queued worker stopped before it starts returns its stop and never
  spawns. A piped (await) delegate waits on the turn's context.
- **a swarm worker waits** (`swarm`): each drain worker holds the cap
  around its spawn; `swarm start 8` under `maxWorkers: 3` runs three.
- **the band counts the queue** (`frontend/tui`): `delegating · 3
  workers · 5 queued · 2m`.
- SPEC_WORKERS 5, SPEC_CONFIG and SETUP name the key.
## [2.14.8]: todo loses start

A task was created, started, and completed: three calls for two facts.
`start` only flipped a row to `active` so the board could show it in
progress, and nothing read that state — `complete` and `fail` already
land a pending task, writing the start event themselves, and the
swarm's `claim` already marks its holder. On a ten-task plan the brain
spent ten round trips decorating the board.

- **the tool loses `start`** (`tool/todo`, `tool/registry.json`): the
  verbs are create, claim, complete, fail, release, retry, read, note,
  notes, finished, and the session's prune, accept, reject, move. The
  words say "create each task before the first edit, complete or fail
  it when done"; the embedded system prompt says the same (`config`).
  The headless menu shrinks with it.
- **the command and the dashboard follow** (`command`, `frontend/web`):
  `/todo start` is gone, `/todo done <id>` lands a pending task; the
  dashboard's pending rows offer `done`, the `/api/todo/start` route
  is gone.
- **the store keeps the event** (`store/todo`): `start` stays in the
  event log's vocabulary — history folds as before, `Complete` and
  `Claim` still write it — and `Start` stays as the store's own door
  (the fixtures reach for it); no tool, command or page offers it.
- the wire moves by the removed words only (menu −40 chars); the
  interactive menu's enum loses one value.
## [2.14.7]: the delegate, seen and heard

The delegate's presence in the session had six ways of lying. The swarm
spoke about other publishers: the controller's `receive` emitted a
`SwarmStatus` for every message it saw, so a delegate running alone was
erased within the tick — the supervisor's empty echoes overwrote the
delegate's own frames in every frontend's latest-wins store. `receive`
now emits only when one of its own workers moved a field the snapshot
carries: an idle controller says nothing, and the TUI and the web chat
hold the swarm's status and the delegate's status side by side, one
status per publisher, no merging of rows.

A worker's menu carried the full tool table though its hands hold only
the doing set: the model could see every name and could be taught to
call one its execution path refuses. For a delegated worker the carrier
now narrows the wire's menu to exactly the names `-allow` resolves to
(allow-none sends none); an interactive session and every non-delegated
run stay byte-identical on the wire, and the registry's delegate words
name the doing set so the main agent writes tasks a worker can do.

Esc on an empty prompt was a hair trigger: with workers running, one
keystroke stopped the batch. It asks twice now — the first Esc paints
`esc again to stop 1 worker` on the indicator row and stops nothing,
the second calls `StopAll`; any other keystroke clears the arm and it
expires after two seconds. With no workers, Esc keeps clearing the
prompt.

The decision reviewer bit while workers ran: a turn ending under a
running delegate fired a bite over rows that were still moving. The
reviewer keeps one fact per `SwarmStatus` publisher; a status whose
rows run defers the bite, and the status that empties the last running
row is the re-wake. `/review` is the operator's hand and fires
regardless; the halt rule (2.14.4) is untouched.

And the drained return was painted as the prompt row: newlines shown as
`⏎`, the head line twice. A return now commits as the tool block
grammar — `● delegate #9 · <the task's first line>`, the bounded
preview, `delegate ✓ <duration> · session <id cut to 8>` (the glyph by
the exit, a failed worker closing with the fault glyph and its exit) —
and the turn's own row is one line naming the batch. The model's
`WorkerBlock` is byte-identical; the CLI prints the same head line it
always did.

testenv unsets `RIG_DELEGATE` and `RIG_FLEET` before the suite runs,
with a self-exec test that starts the delegate's own tests inside a
worker's environment.

## [2.14.6]: the comments go home

A hygiene release: no behavior moves, and the wire reads the same words
it read yesterday. The one-comment rule had been drifting — thirteen
packages carried 215 lines of it, doc comments on the broadcast seams,
design notes in the TUI's frame ticker, the delegate's settle, the
runner's hand-off. The sweep strips every one and lifts the lines that
carried weight into the packages' PACKAGE.md files, where the English
lives; the test names carry the invariants they explained.

The exemptions hold: generated code keeps its headers, and the metadata
packages keep their comments, because they are generation input whose
doc comments the generator lifts. Everything else in the corpus is
clean — the only `//` lines left in Go are compiler directives.

Beside the strip, a quality pass that changes no refusal and no wire
byte: dead helpers out (`assertSequential`, `endedCount`,
`recordFrontend.Input` from the delegate package), duplicated shapes
collapsed (the decision fixture's risk criteria, the status tests'
fleet subscription, the TUI's pluralization and its scripted-input
helpers), one dead nil-guard out of the loop transport, one inverted
branch flattened in the runner's model resolution. The candidates that
would have moved a refusal or an error voice were skipped and named in
the PR.

The frozen surface took a comment-only pass, which is all it ever
takes: the gate compares comment-stripped trees, so a strip is a pure
addition by its own arithmetic, and the branch carries `-refactor` —
the escape hatch doing the one job it documented, named here in the
PR. `make test`, `fmt-check` and the wire job are green.

The README's measured block rode along: 174 lines of Go and 100 of
tests came off the tree — the strip, the dead helpers, the collapsed
shapes — and the stores behind the cache line moved the live numbers
as they always do between releases. The loop is still 409 and the
swarm still 650; neither was touched.

## [2.14.5]: the docs read like a human wrote them

A docs release: no Go moves but `Version`. The README was restructured —
install, first run, the in-session tour, and configuration now lead, with
real `settings.json` and `models.json` examples carried over from the
site — and *what's different* was rewritten in prose: the engine's single
priority queue and why it needs no locks and no timers, the batching and
broadcast that ride it, the decision sidecar with its data curation and
built-in training pipeline, the byte-stable menu, and the workers and
swarm behind their narrowed verb set.

- **the measured block is computed, not typed**: `scripts/readme-measured`
  renders it from the session stores (aggregates only, committed as
  `docs/measured.json`) and from the tree; CI and the release job refuse
  drift. Recomputed: 2,204 sessions, 45,845 turns, 98.8% of 5.0B prompt
  tokens served from cache, and a new era row for 2.10.x — the fleet's
  own traffic, named as the reason its cache ratio gives up points.
- **`docs/DESIGN.md` and the reading set** (`SETUP`, `USAGE`, `PLUGINS`,
  `EMBED`, `TUI_DESIGN`) got the same human/succinct pass; specs and the
  per-package `PACKAGE.md` files are untouched.
- **the GitHub page** leads with the concurrency model and the decision
  pipeline, and its measured strip matches the README's.
- **why rig, in one breath**: the README intro and the page's lede answer
  why this harness and not another — a tree you can read before you
  trust, refusals that name their rule instead of retrying blind, every
  decision a row in a store you own, a session that follows you.

## [2.14.4]: input ends the reviewer's fire

The reviewer's bite ran at the lowest priority and fired in a goroutine
whose context no gesture could reach: the operator's input closed the
`reviewing` row on the screen while the fire's worker kept running
headless behind it — spending its tokens, streaming its reasoning
nowhere, and settling its rows after the operator had already moved on.
The bite now ends when the operator speaks.

- **the fire rides a context of its own** (`decision/review.go`): each
  bite mints a child of the session's context for its fire, and
  `Reviewer.Halt` cancels the one in flight. A fire the halt kills —
  its context dead and its return an error — settles nothing, says
  nothing loud, closes its `reviewing` phase naming `interrupted`, and
  leaves the rows pending for the next landing — the rule a fire that
  answers nothing already kept. The fire's context is released when the
  bite ends, whatever the ending, the release the delegate's worker
  learned in 2.14.0.
- **the gestures carry the halt** (`decision/review.go`,
  `cmd/rig/main.go`): the frontend wrap's `Input` return is the
  operator's line, and it halts the bite before the turn takes the
  slot; Esc on an empty prompt with no turn live ends it beside the
  delegate workers' stop, and a session with neither a fleet nor a
  decision keeps the prompt clear it always had.
- **named cases** (`decision/review_test.go`): the fire's context is
  dead and the phase closes `interrupted` with nothing settled, by halt
  and by input, each test red before its half of the wiring existed; a
  fire that returns on its own releases its context, red before the
  release existed.

## [2.14.3]: the docs say what rig is

The audit of 2026-10-08: README and `docs/` described the engine, the
pipeline, and the wire discipline in cells and table rows and stopped
short of the sentences that own them — while a year of amendments left
the retired gate, the retired clock, and the retired `concurrency`
token still described as live, and the story grew four thousand words
of its own history inside the reading set. This is a docs release: no
Go moves but `Version`. What rig is now goes in prose, quoted from the
spec that owns it, each fact with one home and the others pointing.

- **the README leads with what is different** (`README.md`): four
  short paragraphs in order — the engine (the event loop's sentence,
  completions in call order, nothing locked but the queue, the room
  below the turn), the decision pipeline (proposals an LLM reviews,
  never actions; trained from rig's own rows; served only on a
  measured win), words are the budget (the 15,000 aim, the 15,500
  wall, the wire diff), and the worker rule (a worker does, the
  session decides; the doing set; the return on the next turn) — each
  naming its spec. The layout tree becomes ten names, one line each;
  install, hosted mode, configuration, and the dashboard say one line
  and point, and SETUP owns every knob.
- **DESIGN gains the three sections** (`docs/DESIGN.md`): *the turn*
  grows the engine prose beside the diagram, *the decision pipeline*
  and *the fleet* are new; the middleware and policy lists carry
  `operator` (the delegated worker's verb gate), the seam table fixes
  `Stream(ctx, req Request)`, and the closed-world sentence names the
  two discovery zones instead of denying discovery.
- **the stale lines are fixed where they stand**: SETUP's workers
  paragraph (no slot gate, no delegate clock) and dashboard section
  (the live session, not stores with a few writes); EMBED's worked
  example parses `added tN` and its kernel table carries the operator
  link; USAGE names the `plugin` door and the `finished` verb;
  TUI_DESIGN's event map gains the ten-row phase preview and
  `WorkerDone`, the band gains the delegate's two rows, the key table
  gains the esc ladder's stop rung, and the palette count says five
  and an alias. SETUP's input section gains the esc gesture.
  `site/index.html` matches the README: the documented install URL,
  the async delegate (`started` one line, `returned` on the next
  turn), the hosted row with its required numerics, the resident
  fleet, the current refusal, `swarm start 2`, the real tool list;
  the generated lines stay generated.
- **duplicates collapse to one home** (`docs/`): SETUP's plugin and
  training sections become pointers to PLUGINS.md; the retry bound and
  the allow-list are explained once, in USAGE, and pointed at from
  SETUP and DESIGN; hosted mode keeps its knobs in SETUP; USAGE's
  `/swarm` bullet becomes a list. ROADMAP and CONSOLIDATION move under
  `docs/history/` with a header naming them dated snapshots, and the
  docs table names them history.
- **the measured numbers were re-run, not retyped** (`README.md`):
  45,941 lines of Go and 72,411 of tests, the swarm board at 650, the
  loop pair at 409 (the same commands the pages workflow runs); the
  store-derived rows keep their receipt. Word count of the working
  docs (README + `docs/`, the new `docs/history/` archive excluded):
  18,016 before, 16,876 after — and counting what the archive took
  out of the read set (CONSOLIDATION), 19,147 down to 16,876.

The docs-citation gate ran for this release: every `TestXxx` named in
docs, specs, and the CHANGELOG exists in the suite; `scripts/wire-check`
posts no drift (no registry word moved), and the freeze gate accepts
the diff (`docs/` carries the history move).

## [2.14.2]: the cheap calls ask for the cheap effort

A model row's `effort` field did two jobs: the live turn's default, and
— until now — the effort of the calls nobody reads the thinking of: the
compaction summary, and the review fire's worker. On a row dialed up
for the brain (xhigh on the operator's Qwen row), every summary and
every review thought at xhigh to fold a transcript or accept one row.
The cheap calls now ask for the cheap effort, and the field keeps the
one job it was named for.

- **the summary** (`policy/compact`): `summarize` sends the row's
  lowest `efforts` level — the operator's order is lowest first — and
  `""` when the row names none, the server default riding as before.
  The row's `effort` is unread there; lowering the summary's cost never
  lowers the brain.
- **the review fire** (`cmd/rig`): `rig -p` gains `-effort <level>`
  (precedence flag > `RIG_EFFORT` > the row, refused loud by the
  `effort` command's own check when the level is not in the row's
  `efforts`), and the fire passes the row's lowest level to the worker
  it spawns. Delegate workers and scheduled jobs pass nothing: the
  row's default rides, unchanged.
- **the delegate's spawn opts** (`store/scheduler`): `toRunOpts` never
  carried the swap URL, so a jailed or landlock delegate spawn — the
  default profile — refused at the socket proxy ("unsupported scheme")
  since the delegate tool landed. It rides now; the fire's argv tests
  cover the landlock path.
## [2.14.1]: a worker does, the session decides

A headless worker ran with the session's allow list, which is per tool
name: as long as a tool was allowed, every verb inside it was the
worker's. A delegate worker could `todo create` on the board the session
plans, `scheduler remove` a job, `plugin delete` a live tool, prune the
queue it was handed a slice of. The rule the fleet has been converging
on since the swarm is one sentence — a worker does, the session decides
— and this version writes it down in the two places a worker meets the
world: what it may hold, and what it may be told.

- **the delegate's doing set** (`tool/delegate`): the spawn passes the
  session's allow list intersected with `bash read write edit view
  python web rem` — one named constant, the only place the set is
  written. Todo creation is centralized in the session and the board is
  the session's to judge; observation is distributed, so `rem` rides
  along and a worker may learn, recall, pack and prune memory — clearing
  a stale fact it just disproved is its job too. An intersection that
  keeps nothing runs the worker allow-none (`-allow none`), never the
  embedded default an absent flag would have resolved to: a session
  allowing only `delegate` used to spawn a worker holding everything.
  The swarm's workers keep today's list (draining the board is their
  job) and scheduled jobs keep the operator's job allow. The result's
  trailer is unchanged.
- **the registry marks the operator's verbs** (`tool`): an entry gains
  `operator` — one list per tool, written once. `todo` prune, accept,
  reject and move (the architect's, SPEC_SWARM's rule now enforced);
  `scheduler` remove; `plugin` delete. `rem` has no key.
- **a delegated worker's menu does not offer them** (`policy/operator`,
  `cmd/rig`): the gate is `RIG_DELEGATE` — the marker the delegate spawn
  already sets on the delegate's and the swarm's workers — not the
  frontend kind. A delegated worker's tool specs drop the operator verbs
  from the `action` enum and the verb words from the description and
  schema — `scheduler`'s id field still says `pause/resume/runs/show`,
  only the session's verb is gone. A scheduled fire is a session of its
  own, no marker set: it keeps todo's full verb set, because the hedge
  optimizer prunes and accepts on the board it was given. The wire is
  byte-identical everywhere the marker is absent.
- **a delegated worker cannot run them** (`policy/operator`): a
  middleware in the delegated worker's wire only refuses a call whose
  tool and verb are in the registry's operator list, before the tool
  runs, with one named refusal — `todo prune: the session's verb — a
  worker does not judge or delete the board; leave it for the session`
  — returned as the tool result, never a fault. The tools' switches are
  untouched; every other verb passes, `rem prune` included.

## [2.14.0]: the delegate lets go

A delegated worker used to own the turn that spawned it: `Run` blocked on
`spawn` for as long as the worker lived, so ten delegates in one turn cost
ten waits, and the only way out was to interrupt — which killed the worker
that was doing the work. The blocking was never the point of the tool; the
answer was. So the tool lets go of the answer. The turn starts a worker and
hears back one line — `delegate: worker #2 started · session <id> · log
<path>` — and the worker's message comes back on a later turn, as the head
of whatever the operator would have typed next. That shape is not new to the
session: the steer slot is a line that waits for the boundary, and a return
is the same mechanism with the session as its source. Every refusal still
lands in the turn that asked — the seams, the recursion guard, the residency
gate, the workspace rule — because a refusal is an answer.

- **a return is an event, and the inbox is a queue** (`core`, `tool/delegate`):
  `core.WorkerDone{N, Task, Content, Exit, Duration, Session, Log}` carries
  the text the synchronous result always was, with `Head` and `WorkerBlock`
  beside it so the block the model reads is one shape written once, not three
  frontends agreeing by hand. It implements no `Snapshot`, so nothing
  coalesces it: two workers that finish during one turn arrive as two blocks
  in the order they finished. `DelegateStart` does everything
  that can refuse and returns a `Delegation`; `Wait` collects the outcome
  once the run log and the record are on disk; `Delegate` is the two of them,
  so the swarm and the review fire keep the blocking path untouched.
- **the next turn, in all three frontends** (`frontend/tui`, `frontend/cli`,
  `frontend/web`): an inbox appends returns and wakes `Input`, which drains
  it at the top of the loop — ahead of the steer slot, so the model reads the
  returns first and then what the operator typed. A drain with no live turn
  starts a turn of its own; a live turn is never interrupted. The dashboard
  shows the same head line in its feed as it arrives, and the CLI prints it:
  the terminal sees what the model was told.
- **a worker belongs to the session, not the turn** (`tool/delegate`,
  `cmd/rig`): the spawn's context derives from a root-supplied session
  context, so the turn ending no longer kills the worker. The interrupt
  gesture with no turn live — esc, the dashboard's stop button — reaches
  `StopAll`, which cancels every running worker and, under the jail, its
  process tree. The gesture is the bare esc on an empty prompt — the one with
  nothing left to clear — and a session with no delegate wired keeps the
  prompt-clearing it always had.
  The CLI has no such gesture, named: SIGINT there ends the session, and the
  workers end with it.
- **the delegate's two rows** (`frontend/tui`, `swarm`, `core`): the band
  distinguishes the two publishers of `core.SwarmStatus` by the one thing the
  message carries, the role string (`delegate`), and renders `delegating · 3
  workers · 1m12s` over the most recent call across the batch — `#2 edit
  tool/file/edit.go · 12s`, `—` until a worker calls — the same two rows for
  ten workers as for one, under the cache row. Between turns nothing else
  repaints, so a running batch keeps the frame ticker alive; the ages are
  computed at paint from stamps the snapshot already carries, and no new
  clock joins the ones that exist.
- **a call crosses the pipe; a body does not** (`broadcast`,
  `frontend/oneshot`, `core`): the worker publishes each call as a `tool_start`
  frame carrying its name and one short argument — the first line of `path`,
  `command`, `pattern`, `query`, `url`, `task`, `id` or `name`, cut at 80
  characters. `core.ToolStart.BoundedCall()` is the reader that knows the
  difference, and is the reason `tool_start` is a kind and not a `Notice`
  shaped like one. The bounding lives in the worker, the only process that
  has the body: a 10 KB `write` crosses as its path.
- **the pipe is the fleet's, not the verdict's** (`cmd/rig`): a delegated
  worker has always had a fleet pipe whether or not its allow list carries
  `verdict`, and the old gate withheld it — so a worker that could not rule
  could not heartbeat or speak, and a silent worker looked hung. The gate now
  decides the verdict tool alone.
- **a piped session keeps its synchronous delegate** (`tool/delegate`,
  `cmd/rig`): `rig -p` ends when its turn ends, so there is no next turn to
  carry a return; `Opts.Await` keeps the tool waiting, which is what lets a
  cron fire fan out and read the answers.

Named, not changed: the recorder has no case for `WorkerDone`, so a return
lives in neither the transcript nor the store — the worker's run log is where
its result is kept, and `scheduler runs` names the path. `SwarmWorker.Tool`
is bounded by the sender, not the reader: a caller that hands over a raw
`ToolStart` gets its whole argument in the band, and only the oneshot
publishes the bounded form. And the 2.12.7 clause "a worker lives until it
exits or the turn is interrupted" is reversed on purpose, with the tests that
pinned it; what survives is the no-clock rule — nothing waits longer because
nothing waits at all.

## [2.13.1]: the summarizing phase gives the row back, and the docs catch up

A reactive compaction runs inside the turn — the provider faults with a
context-length error and the decorator summarizes before retrying — and
in that position `core.Compacting` cannot take the indicator row: the
turn owns it, so `compacting` stays false and the phase begin falls
through to `beginPhaseLocked` and sets `aside`. The compaction line is
summarizing's end (2.12.1 pinned it, test included), and the `Compacted`
handler cleared `compacting` without ending the phase. `aside` is written
in exactly two places, and nothing else clears it: after the first
in-turn compaction the indicator painted `summarizing · <age>` for the
rest of the session, the frame ticker restarted itself on every tick, and
the 2.11.7 notice breath starved on its own gate — a phase owns the row,
so no notice ever could. The fault path leaked the same way. A box with a
decision server hid it: the next review bite's `Phase{Done}` cleared the
stray phase by accident. Compaction had five tests and all five drove the
idle path, where `aside` is never set.

- **the phase ends when its end event arrives** (`frontend/tui`):
  `Compacted` and `Fault` close the open phase through `endPhaseLocked`,
  the way a reviewer's settle does. No check line joins summarizing:
  the compaction line stays its end, and the pinned ordering tests are
  untouched. Three tests name the invariant, one of them the breath.
- **the specs stopped contradicting themselves** (`docs`, `specs`): the
  audit this came out of found ~90 claims at odds with HEAD, and the
  dominant shape was not a stale file but an amendment that landed
  beside the clause it replaced. `todo project` was still "the binding
  door" five releases after 2.12.0 deleted binding; SPEC_WORKERS' body
  still ran the slot gate its own 2.6.0 amendment retired;
  SPEC_PLUGINS' intro still put plugins on the wire that 2.8.2 folded
  behind one door while decision 8 said otherwise in the same file.
- **the retired concepts leave** (`specs`, `docs`): `workers.json`'s
  presence rule (2.4.0) in SPEC_CONFIG's `Config` struct, the goals, the
  layout and seven tests that no longer exist; the `plugins` tool and the
  `allow` rule that now drops the name with a notice; `ls`/`find`/`grep`
  in SPEC_CORE's layout and SPEC_MODES' approval sets; the `golden_020`
  fixtures and the `-update` flag 2.12.3 moved to the wire job; `bind`
  in SPEC_STATE's "verbatim" tool surface.
- **the entry points count what the code counts** (`README.md`,
  `AGENTS.md`, `docs/USAGE.md`): the menu's arithmetic (12 without
  vision, 13 with, `decide` conditional, `verdict` fleet-only and missing
  from the table), two `swarm start` examples the command refuses, a
  layout tree missing six top-level packages, counts three releases
  stale, 13 of 16 commands in the file an agent reads first, and no
  build/test/gate workflow in AGENTS.md at all — nothing to warn a
  contributor that the freeze job would refuse their PR.
- **the embedder's example compiles** (`docs/EMBED.md`): `todo.Create`
  takes one `CreateItem` since 2.12.4; the freeze paragraph states the
  gate as `specs/FREEZE.txt` states it (`core/provider.go` reopened
  2.8.3), the kernel surface is its nine options, and the retired row
  `concurrency` token is gone.
- **the config spec owns settings.json** (`specs/SPEC_CONFIG.md`,
  `docs/SETUP.md`): the knobs table is `config.knownSettings`, with
  `decisionUnit` and `trainPython` in the spec that governs them for the
  first time; `RIG_MODEL_*_CONCURRENCY` left SETUP's overlay list (it is
  ignored with no line), the example `allow` lost the name config drops,
  and the knob table gained `reviewBatch`, `plugins.max` and `workers`.

Named, not changed: the recorder has no case for `Notice` or `Phase`, so
neither survives a resume — SPEC_TUI said the opposite and now says
this; whether they should be persisted is the operator's call. The
dashboard still ships the pre-2.4.0 `no workers configured` branch that
the server can no longer trigger; that is a JS change for another PR.
## [2.13.0]: the decision model is trained from rig's own rows

The rows the reviewer settles were gold labels that trained nothing:
the fine-tune pipeline lived outside rig, read the store through a
read-only sqlite handle, and its output was whatever that script's last
run left. 2.13.0 owns the pipeline. The evidence that made it worth
owning: the base Laya scores 38% on held-out bash against a constant
59% and carries no pack signal at all (mean p(yes) 0.11 on relevant
against 0.13 on not); the 2026-10-06 full fine-tune on 167 settled rows
scores 81% bash and 82% pack with p(yes) 0.33 against 0.03 — and
head-only training equals the base model, so the gradient step must
move the encoder, which is to say it is Python, never Go.

- **`train/` is a kernel-loaded zone beside `plugins/`** (`plugins`):
  one trainer is one Python file, the name the filename stem, its
  contract two callables — `train(rows_path, out_dir) -> report` and
  `evaluate(checkpoint, rows_path) -> report`, a report per-question
  accuracy and, for noul, the mean predicted p(true) by gold class.
  The zone machinery took a contract: `Contract` names what one file
  of a zone must expose (`PluginContract`, `TrainerContract`), `Zone`,
  `List`, `WritePending` and `DiscoverChecked` name the home directory
  they read, the discovery cell generates its own checks, and one
  `Invoke` cell calls a loaded file's method. The zone reads through
  `trainPython` (settings, beside `decisionUrl`): torch is two
  gigabytes and the session kernel's venv is not where it belongs.
- **`decision train <trainer>` never runs inside a turn** (`command`,
  `cmd/rig`): the slash command enqueues a scheduler command job
  (`rig decision train <trainer>`, once, a couple of minutes out); the
  job runs headless, exports the settled rows (approved takes the
  proposer's answer, denied the reviewer's parsed correction — the
  regex `~/laya/retrain_rig.py` carried, ported; unparseable rows
  count and skip), splits 80/20 stratified by question and label with
  no seed and no shuffle, writes `decision/train/<run>/rig-train.jsonl`
  and `rig-heldout.jsonl` in laya's shape, and scores the candidate,
  the incumbent and the constant baseline — computed from the train
  rows, never assumed — on the same held-out rows.
- **Promotion is a measured win** (`cmd/rig`): the candidate must beat
  the constant AND the incumbent — the checkpoint the unit file
  `decisionUnit` names serves — strictly, on every question; a tie is
  not a beat and a missing question is not a beat. The run is recorded
  in the decision store's `trainings` table (the three held-out
  reports, promoted or not), one `core.Notice` carries the held-out
  table, and promotion rewrites the one `Environment=` line in the
  unit file and prints what the operator runs —
  `systemctl --user daemon-reload && systemctl --user restart laya`.
  rig restarts no service it does not own, and no `decisionUnit`
  means no incumbent and no promotion. The nightly is the operator's
  one line (docs/SETUP.md).
- **`store/decision` gains `trainings`** (`store/decision`): the
  metadata, the generated projections, the settled-rows read the
  export holds, and the run record. Schema version stays 2: a new
  table applies on open, nothing migrates.

## [2.12.11]: the models table is the operator's

`/models` lists `local` beside the operator's five rows, and the
operator's `~/.rig/models.json` has no such row: `config/models.json` is
embedded in the binary with one row in it, and `config/modelsfile.go`
folds it under the operator's file at every start. 0.25.6 removed the
model *default* (`model: local` left the embedded settings.json, and a
run naming no model refuses) and kept the row, on the reasoning that a
model table is not a default. The table is not the binary's either: a
row the operator never wrote has no business in the picker.

- **the embedded table is empty** (`config`): `config/models.json` is
  `[]`. The file and its `go:embed` stay — the parse, the row-by-row
  merge, and the merge's error voice remain one path, and `mergeRows`
  over an empty embedded table is the operator's table verbatim — but
  nothing ships a row, so `Load` with no `models.json` yields no rows,
  `/models` lists only what the operator wrote, and the only way a row
  exists is that someone wrote it. No filtering, no hidden-row rule: the
  row is gone from the data the fold reads.
- **a named row nobody wrote refuses like a name nobody set**
  (`cmd/rig`, unchanged code): the root resolves the active id through
  `models.Resolve` at the point 0.25.6 moved to the front of the start,
  so a fresh install naming `local` refuses before any store opens or
  request is made, naming the missing id and the table's known ids — the
  existing voice, not a new one (`TestNamedModelWithNoRowsRefusesBeforeAnyRequest`
  counts zero requests and no rig home created). `RIG_MODEL_*` still
  mints the active row for an id the table does not know: the env
  surface is the escape hatch it always was.
- **the tests own their rows** (`cmd/rig`, `config`): `rigEnv` writes a
  `models.json` into the scratch rig home with the row its
  `RIG_MODEL=local` names (the values the embedded row had, so every
  wire body is unchanged; a test that writes its own table keeps it), and
  the fixtures that used to lean on the embedded row for a base — the
  vision row, the file-row switch, the wire's `run-job` home, the
  `RIG_HOME` overrides, the cold-shell fire, the REPL command run — write
  theirs whole. `defaultsTable` builds from the test's own row instead of
  `config.Load` of an empty dir. The config merge tests that need an
  embedded row build it from a literal (`config/modelsmerge_test.go`, the
  internal package): overlay per field, the hosted fields, the unlisted
  row kept, a new id's defaults, `vision`'s presence-aware descent, and
  the violation voices, none of them reading the binary's table. The wire
  job shows no drift: the row's values did not move, only who writes them.
- **the docs say so** (`specs/SPEC_CONFIG.md`'s goals, layout, 4, and 5,
  `config/PACKAGE.md`, `docs/SETUP.md`, plus the one-clause corrections
  in `specs/SPEC_COMPACT.md`, `specs/SPEC_COMMANDS.md`, and
  `specs/SPEC_CORE.md`): the embedded table is empty, the operator's file
  *is* the table, the examples carry `local` only as a row the operator
  wrote, SETUP's config section tells you to write the row for the model
  you mean to name before the first run, and 0.25.6's "a model table is
  not a default, and `model: local` still works when the operator names
  it" gets its amendment: still true — when the operator names it, and
  wrote it.

## [2.12.10]: a fired once-job is done, and a job can be read by id

Every delegate fire registers the job it fires — `adHocCreate` mints a
row with `cron: once` and `at: now`, no crontab line — and records its
run, but the state was a flag on the write, not a rule of the fold:
`run-job` passed `Done: job.At != nil` and `RecordRun` appended a `done`
event for it, while the delegate's record passed nothing. The live store
kept the receipts: 89 once-jobs, 71 of them still `active`, 65 with a
fire already recorded, nearly all named `delegate:Review these recorded
decisions…` — one row per reviewer bite, dozens a day, and `list` printed
all of them while the registry promised `at` "self-deletes after
firing".

The lifecycle moved into the fold, where one rule covers every writer:
`jobState.consumeFiredOnce` settles a job whose `at` is set, whose state
is still `active`, and whose `last_status` is `ok` or `fail`, and the
fold calls it from the `run` verb and from the compact snapshot — a store
compacted before the rule carries the fire as `lastStatus` with no `run`
event left to fold, which is exactly what those 63 rows are. Whatever
writes the run, the row settles: `RecordRun` applies its own run event
through `fold.apply` instead of hand-mutating the state it just appended,
so `RunRecordInput.Done` and the extra `done` event went away (the op
stays in the fold for the logs written before this, and the fire now
writes one event). No migration, no sweep, no direct write to the
projection — the lingering rows move to `done` at the next fold and
`list` treats them as it treats `done` today. A skip is deliberately not
a fire: the runner records skips for the drift it wants the list to keep
naming (a paused row behind a live line, a held lock), and a once-job
that never ran stays live for its re-fire.

`show` reads one job. An agent holding a `jN` from `list` or a `runs`
reply could not read that row — `runs` is the audit trail, not the
definition, and `list` is the whole board — so `Show(ctx, id)` joined the
tool's `Scheduler` interface and answers with the `jobLines` block `list`
prints for that job (crontab line, drift, running lock) plus one
`last <run>` line off the runs container: one renderer draws a job row
wherever it appears. An unknown id names itself and points at `list`, a
removed one says so, and `/scheduler show jN` rides `schedulerVerbs`. The
registry's words follow the code: `at` is "done after its one fire", and
the guideline says `show` reads one job by id.

---
## [2.12.9]: a phase streams a preview, not a transcript

SPEC_TUI 3 said a phase's deltas "stream dim under the row" and the TUI
read that as *flow them through the pending region* — and the pending
region commits every line a newline closes. A reviewer's bite or a
compaction's summary call therefore wrote its whole reasoning into the
scrollback: hundreds of dim lines above the one check line, the thinking
worth a glance while it runs kept as a page in the record
(the operator's read, 2026-10-05).

A phase's deltas now buffer in their own text and the live region draws
them under the phase's indicator row as a preview: a rolling tail of at
most `phasePreviewRows` (10) screen rows measured by `screenRows` at the
terminal's width, dim in the reasoning slot, headed by one dim
`· n rows above ·` line once the tail has scrolled — the tail-and-marker
shape the tool bodies learned in 2.11.11, in the unit that is honest:
forty lines of reasoning is ten rows and a count, whatever the width.
The preview redraws with the live region on every delta and re-measures
on resize; the viewport shrink loop gained its cap and gives it up
first, because a peek is the cheapest row on the screen. The reasoning
toggle gates it as it gated the flow. The end commits what it always
committed, the one check line, and for `summarizing` the `⧉ compact`
line after it: 2.12.1's ordering holds by construction now — the preview
is gone with the phase's last frame, so nothing can land out of order.
The recorder, the web feed and the headless frontends still receive
every delta: this is the TUI's rendering choice, not the event's. The
live turn's own thinking stream is untouched, and a phase that waits
during a live turn still shows nothing. The loop, the policy and the
events did not move.

---

## [2.12.8]: create echoes the task

Every transition verb answered with `echoTask` — the note, the affected
row with its links, the summary line. `Create` alone answered with the
whole present: every open task and the ten most recent finished, one
dim hint line for what was hidden. The exception was written for the
array (`store/todo/PACKAGE.md`: "after a merge the whole queue is the
news"), and 2.12.4 took the array away: a create is one task, so a plan
of five steps is five creates, and five creates printed the queue five
times. The reply shape was taxing the planning it exists to support —
Qwen planning 2.12.6 on 2026-10-05 counted it out loud ("each returns
the full queue though. That's ~7 × 1500 tokens = 10k tokens. Hmm,
painful but tolerable") and then weighed batching its own plan to dodge
the cost, trading the one-task atomicity 2.12.4 had just bought.

`Create` replies through `echoTask` like every other verb: `→ added t4`
or `→ t4 already there`, then the row with its `requires`/`blocks`/
`waits for` suffixes, then the summary with its `next:`, so the first
claim of a plan is visible without a read. The id the next call links
by is on the line the create just printed. The refused create keeps the
queue (2.11.10): that reply is where the ids a bad link was guessing
at are listed, and nothing lands. `read` is unchanged and is where the
queue lives; `read all:true` is still the history.

One shape for every write, at every door: the tool's `create`, the
`/todo create x` line, and the dashboard's POST all print the same
echo, and the TUI needed no new renderer — `todoBlock`'s `Echo` path
already draws a note, one row and the head, and a create block now
renders exactly that with no finished rows under it. Three store tests,
a tool test and a TUI block test pin the shape, one of the store tests
by name (`TestCreateEchoesTheTaskAndTheSummaryNotTheQueue`); the create
tests that had been reading the queue out of the create reply read it
through `read`, which is where it was all along.

---

## [2.12.7]: a delegate has no clock

A delegate carried a clock: ten minutes by default, `timeoutMs` to
stretch it, thirty minutes as the ceiling it clamped to. The clock
started at spawn, so it measured the queue and not the work — a ten-way
fan-out of test-pass reads on 2026-10-05, all ten on GLM's one slot,
lost the tail worker at 1500.3s to `delegate: the worker timed out
after 25m0.003s (process tree killed)` with nine spawns ahead of it in
the server's queue and its own first token still unwritten.
SPEC_DELEGATE says extras wait for a slot rather than failing; they
waited, and the clock failed the tenth of them on a schedule. A timeout
on a concurrency seam guesses how long the work should take, and under a
shared slot the guess is always wrong for someone.

The tool has no clock. `timeoutMs` is off the schema and off the
registry words, `defaultTimeout`, `delegateTimeoutCap` and the
timed-out error voice are gone, and `Run(ctx, task, workspace, model)`
is the verb. The spawn context is the turn's context (`SpawnCtx: ctx`
beside `Timeout: -1`), so a worker lives until it exits or the turn is
interrupted, and the interrupt — which the operator already had — ends
the turn and takes the worker's process tree down through `RealSpawn`'s
Setpgid cancel, the same kill the timeout used. What bounds an induced
worker is the turn that asked for it, the fleet's slots, and the
operator's hand; none of the three needs a guessed duration. The
scheduler's own callers keep `DelegateInput.Timeout` untouched: a
`run-job` fire carries its per-job timeout in minutes, the review fire
and the swarm pass -1, and the seam's 24h ceiling bounds a positive
caller.

Silence is shown, never acted on: the fleet pipe already stamps a
heartbeat per worker and the indicator's swarm row already prints its
age, so a worker that has written nothing for ten minutes reads as one
and the operator ends the turn. The 1.3.8 stall kill stays exactly
where it was — the runner's per-job `stall` window, an opt-in operator
setting about a process that has written nothing, not a ceiling on how
long work may take — and the interactive delegate sets no window. Two
tests that pinned the tool's clock are gone with it; four pin the rule
in their place: no deadline on the spawn, so a worker outliving the old
default returns its message; an interrupted turn cancels the spawn
context; an interrupted turn over the real `RealSpawn` kills both the
worker and the child it backgrounded; and a call carrying `timeoutMs`
refuses as the unknown field it is. The menu drops to 13,487
characters.

---

## [2.12.6]: every native tool is its interface

A native tool was a struct with one `Exec`: it decoded JSON, held every
check, and answered with a string. A Go caller that wanted what the tool
does had to speak the wire to get the checks — marshal arguments it
already held, call `Exec`, and match the refusal's words hoping they were
the words the model sees. 2.12.4 gave `tool/todo` the other shape, a
`Todo` interface embedding `tool.Definition`, declaring `Exec` as the one
JSON door and one typed method per verb with the checks inside it, and
this release gives it to every native: `Bash`, `Read`, `Write`, `Edit`,
`View`, `Verdict`, `Delegate`, `Web`, `Sessions`, `Python`, `Rem`,
`Scheduler`, `Plugin`, `Decide`. The implementing struct is unexported,
the constructor returns the interface, `Exec` decodes and routes and does
nothing else, and the verb's required fields, mode gates and bounds sit
in the method the model's call lands in: `Edit(ctx, path, old, new)`
refuses `old matched 0 times` from Go exactly as from JSON,
`Scheduler.Create` holds the command-job model gate and the workspace
containment, `Web.Fetch` holds the `maxChars` and `timeoutMs` bounds,
`Rem.Learn` holds the scope rule that mints no memory under a path that
is not there. A wire shape Go cannot name — a todo `link`, an id or a
list of ids; a scheduler `model`, absent, null or a name — is decoded at
the door and reaches the method decoded, or as a pointer where absent and
zero differ. A verb's target stays positional — the workspace, the path,
the job id — with at most three of its own fields beside it, and the
fields go in one input struct named for the verb — `Create(ctx, in
CreateInput)`, `Learn(ctx, scope, in LearnInput)` — as soon as they are a
bag of options rather than an order, because a door whose ten arguments
are six strings in a row is the wire again with type names on it. The bytes the model sees do not move: not one registry word,
not one refusal sentence, and `TestFrontendMenuBytesStayWithinTheBudget`
holds the menu at its 1099.

Where the template did not fit it is named, not forced. The plugin door's
schema verb is a method called `Contract`, because `Schema()` is the
tool's own argument schema — the one the door overrides to carry the live
plugin names; its four forge verbs now call the ecosystem's typed
methods, so the ecosystem lost the JSON door that had no caller left. The
decide tool routes on `kind` and its three methods are its three question
kinds, `Choice`, `Binary` and `Score`, sharing the item refusals and the
read ceiling in one run. `Python` carries beside its three verbs the
kernel seams the root and plugin discovery already held: `Run`, `Host`,
`Close`. Each interface gained one invariant test that a typed call and a
JSON call refuse and reply the same way, fourteen in all.

---

## [2.12.5]: edit takes one change

The chunk list was a second way to use the tool, with its own grammar:
`{old, new}` pairs applied in order, chunk indices in the refusals
(`chunk 2 of 3: old matched 0 times (absent from the file as the earlier
chunks leave it)`), a 32-chunk bound, a total-bytes bound, one reply line
per chunk, and a TUI preview that walked the list. 2.12.4 took the same
shape out of `todo` for the same reason: one way to use a tool is one
thing to reason about. Edit takes `path`, `old` and `new`; `old` matches
exactly once or the call refuses by name (`old matched 0 times` /
`matched 3 times, want exactly 1`); the reply is the path and the bytes
replaced. The bounds ahead of any I/O are the two about the call: `old`
is not empty, and `old` plus `new` sit under read's ceiling.

What the list bought, the loop already gives: `edit` is mutating, so
several calls in one turn run one after another in call order (SPEC_EVT
2a) and each landing records the file's new state, so the next call is
drift-checked against what the one before left — a test in
`loop/batch_test` names that order, because the order is the mechanism.
2.5.0's evidence (412 edits, 22% of them to the file the previous call
had just edited) is answered by the turn. The unread-file teaching reply
and the drift refusal are untouched, and old transcripts keep their
`edits` arrays as the history they are: nothing replays them, so there
is no shim and a legacy call refuses as an unknown field. The menu drops
to 13,594 characters.
## [2.12.4]: one task per create, the tool as its interface

GLM numbered its five-step plan and wrote `requires: "t1"`, `"t3"`,
`"t4"`, meaning its own steps; the ids resolved first, those tasks exist
from August, and the chain it meant was never recorded. The array was
the bug: a link in a batch could be an id, a sibling's text or a
position, and every spelling was a guess at intent. `create` now takes
one task, and a link is the id a reply gave: the relation exists when
it is written, one call is one id and one event, and independent tasks
go out as parallel calls in one turn. A number or a text as a link
refuses at the door by name; the refusal teaches the one form and shows
the queue. The empty create that cleared a queue went with the array.
Old array events fold at replay as written.

The tool is now its interface, the template for the other native
tools: `Todo` embeds `tool.Definition`, declares `Exec` as the one JSON
door, and has one typed method per verb (`Create`, `Claim`, `Start`,
`Complete`, …) taking the scope and the verb's fields; `Exec` only
routes, so a Go caller and the model hit the same checks. The menu
drops to 13,736 characters.
## [2.12.3]: the gates move to CI

The freeze gate and the wire pins left the test suite for CI jobs: a gate
is a job the PR sees, not a test the tree carries. The freeze allowlist
left the Go boolean for `specs/FREEZE.txt`, a file the repo owns — one
path per line, a `#` line for why, the reopenings named with the version
that reopened them — and `cmd/freeze` is the small program the freeze job
runs: diff `origin/main...HEAD`, every touched path must match a line,
else the refusal names the path and the file to edit. A reopening is a
one-line diff to that file, reviewed in the PR; the set is the old set
plus the gate's own homes, `cmd/freeze` and `scripts`, and nothing else.

The wire pins became a diff too. `scripts/wire-check` renders the three
request bodies and the tools array at the merge-base and at the head from
the same fixture the goldens used — the dump's bodies came out
byte-identical to `golden_020` the day it moved — and posts the unified
diff as the job's summary, so a words pass is reviewed as the diff it is
instead of a regolden commit. The stored goldens, the `-update` flag, and
the sha constant went with the job.

- **The menu budget is a guideline now**: aim 15,000 characters, fail
  past 15,500, the two numbers living in the wire job. The wall at
  15,000 trimmed a sentence three times in 2.11.9 to fit; a sentence
  that carries meaning is never cut to save forty characters. Today's
  menu is 13,846.
- **What stays pinned**: `TestSystemPromptIsByteStableAcrossBuilds`
  (determinism is an invariant, not a golden), the registry's shape
  tests, and the words' vocabulary — the no-other-harness's-voice check
  moved to `tool/registry_test.go`, where the words live.
- `frontend/tui/freeze_test.go` is gone; its equivalence to the file was
  proven on the branch that moved it, and the corpus test in
  `cmd/freeze` carries the file's behavior from there.

## [2.12.2]: the reviewer reads the code

A review fire on pack rows reasoned "this is genuinely ambiguous without
the code … the pack question is whether the symbol matters for the task"
and guessed, because it had to: the fire ran with the verdict tool alone
and a pack row's state is one line, the task and the symbol's file:line.
The fire now has `read` and `rem` beside `verdict`, still bare and still
jailed, and the contract says to look before judging a row about code and
to name what was read in the reason. Because a row's scope is a key and
not a path, a fire reviews only the rows of its own project or `global`;
rows of other projects wait for a session opened there, and the drain's
report counts them. No new setting: the tool list is the fire's.

## [2.12.1]: the compaction line lands last

The summarizing phase streams the summary call's thinking, and the
`⧉ compact` line committed before that stream had closed: the handler
flushed the finished lines of the flow but left the open tail in the
pending region, so the tail painted after the compaction line and the
transcript read as compact, then more thinking, then the indicator. The
compaction end now closes the pending flow the way a turn end does, so
the line lands after the last word of the summary's thinking. Its words
shorten too: `· up 111k down 4.3k`, since the row above just said
`summarizing` for the whole call and the arrows name the direction.

## [2.12.0]: scope is a parameter, never a guess

The evidence was one live session: rig started in `~`, the work lived in
`~/Projects/rig`, and three of five `rem` calls plus a `todo create`
failed — `pack` found no map, `index` refused the home as a project —
because both tools resolved the project from the process cwd or from an
optional `project` argument the model never passes, and todo carried a
`bind` action and a session binding to make the guess sticky. Scope is a
required parameter on both tools now: `"global"` is the one reserved
word, anything else is a project directory (`~` expands at the paths
boundary, a worktree shares its repo's key), and a call without it
refuses naming the rule. No cwd fallback is left in either tool, todo's
`bind` and the `session_project` binding are gone, rem's old
`project`/`scope: project|global|all` pair is one `scope`, and the
global scope is a fixed key in `store/scope`, never a hash of a path.
The `start` and `claim` replies carry `· scope <path>` on the row so the
model knows where to run and what to pass next; the swarm brief and the
run-job report-back name the scope path the worker must pass. The
operator moves the session, never a tool: `/project <path>` closes the
current session and opens a fresh one in that workspace through the same
seam `/new` uses, with the path canonicalized, a non-directory refused
by name, and the workspace's AGENTS.md riding the new session's system
prompt. The move is the process's own: `os.Chdir` runs before the
recorder opens, so every tool's exec-time cwd is the workspace too —
one truth rather than two. On the wire the menu moved within budget: 13,998 chars before,
13,846 after, of 15,000.

## [2.11.12]: a yes/no question is a binary

The tool menu's budget moves from 14,000 to 15,000 characters. The wall
at 14,000 trimmed a sentence that taught the model something three times
in one week, twice this release, to save a handful of characters; the
budget exists to make the cost visible, not to cut meaning at a round
number. 2.12.1 turns it into a guideline with the delta shown on every
PR. The menu stands at 13,998.

The question kind `yesno` is `binary`: the constant, the constructor,
the decide tool's enum and the docs say one word for one shape. The
answer values stay `yes` and `no`, and the decision server's wire never
carried the kind (a binary rides as `noul`), so nothing changes between
rig and Laya. The store holds nine hundred rows written as `yesno`; the
two reads that decode a stored question fold the old word to the new,
so a settled binary still answers its twin.

## [2.11.11]: a tool body hides rows, not lines

The committed tool block kept the first six and the last two lines of a
result and hid the rest under `· k lines hidden ·`. The unit was the
logical line, so a bash result of a thousand short lines was eight rows
and a marker while a result of one three-thousand-character line, a
minified blob or a `go test` failure on one line, was a wall of wrapped
text with nothing hidden; read looked tidy and bash did not. The unit is
the screen row at the terminal's width now, for the body and for an
edit's two sides alike, so every result has the same height. The piped
frontends have no width and print the whole body as before.

## [2.11.10]: the refusal shows the queue that exists

2.11.9 made a refused create show the queue, and showed the wrong one:
the planner adds the planned tasks to the folded state before the links
are checked, so the rendered queue carried the four tasks that were
about to not land, and the transaction then rolled them back. A model
read `4 open`, took the create as done, and found `0 open` on the next
read. The queue in the refusal is rendered before the plan now, so it is
the one that exists; a test pins that a refused task's text never
appears in it.

## [2.11.9]: a number is a link, and a refused create shows the queue

A model planning five tasks in one create wrote `"requires": 2` the way
the words invited, "its number in this list", and the tool refused it,
since a link had to be a string; GLM then mangled the retry into
`"}, 2"` three times and hit the retry guard. A JSON number is a link
now, the sibling's position, with a fraction or a zero refused by name,
and the schema says so first. A create that cannot resolve a link still
lands nothing, but its refusal names the three forms with an example
each and shows the queue as `read` would, open rows and the finished
tail, so the next call links to the ids it names instead of guessing.

## [2.11.8]: the project's contract follows the session

A project's `AGENTS.md` loaded only when it sat exactly in the process's
working directory, read once at start. Open rig from home and work in
`~/Projects/rig`, or open it in `store/graph`, and the repo's contract
never reached the model. Now the project's file is the nearest
`AGENTS.md` walking up from the workspace to the repository root, the
directory holding `.git`, and no further: a subdirectory reads the
repo's contract, a file above the repo is nobody's, a workspace that is
no repository reads only its own, and the operator's file is never read
twice when the workspace is the rig home. It is read when the session
wires rather than once at start, so a session that opens in another
workspace carries that workspace's contract; `Config.Agents` is the
operator's file alone. A scheduled worker still inherits its job's cwd's
file. `Load` still refuses loud at start when the file cannot be read.

## [2.11.7]: notices breathe once

A notice was a dim line committed to the transcript, and the transcript
is what the operator is reading. One fact that held for a thousand rows
scrolled it a thousand times; even one decision bite per turn scrolled
it. Now a notice is a state the operator sees for a moment, not a line
they scroll past forever. The TUI queues notices (identical ones
collapse, no size) and, when the action indicator's row is idle, the
oldest takes that exact row in the indicator's shape, breathes in once
on the ember's curve and is gone; the next follows. While the model
thinks or a tool runs the indicator owns the row and notices wait. No
new row, no height change, and no second clock: the breath rides the
frame ticker that already drives the indicator.

The color says what the notice means. `core.Notice` gains a `Level`
(`LevelInfo` the zero value, `LevelSuccess`, `LevelError`; the same kind
of reopening as `Snapshot` and `Verdict`), `broadcast.Say` carries it,
and the three places that make the text choose it by one rule: error
when something failed or was refused, success when something the
operator asked for completed, info otherwise. The breath paints in the
level's existing slot, red, green or white, the slots the tool rows have
trained the eye on all session; no new theme vocabulary. The web
frontend receives the level in its `notice` frame for free; the
recorder stores it. `RenderNotice` and the transcript line are gone.

The second commit lets the operator watch the two things that used to
happen behind the indicator. The decision review's bite and
compaction's summary call were both a model thinking out of sight: the
bite in a child whose reasoning went to its run log, the summary in a
stream the policy read and dropped. Both are now a `core.Phase`, one
type for begin, delta and end (`reviewing`, `summarizing`), the same
kind of reopening as `Snapshot`, `Verdict` and `Level`. The one-shot
worker sends its reasoning deltas over the fleet pipe as themselves;
the reviewer, which minted the voice and knows what the fire is for,
turns them into phase deltas and closes the phase at settle with the
count; the compaction policy opens `summarizing` before the summary
call and streams its reasoning, with `Compacted` as the end. The TUI
shows a phase in the indicator's row as `reviewing · 14s`, streams the
thinking dim under it through the reasoning toggle, and commits one
checked line when it ends, then returns the row to idle and lets any
waiting notice breathe; during a live turn a phase waits, except
summarizing, which runs inside the turn. The frontend member now
delivers only what a frontend renders, `Notice`, `SwarmStatus` and
`Phase`, so a worker's raw thinking never reads as the session's own.
Named, not built: no percentage for compaction (the call has none, so
the elapsed time is the fact), and the swarm's workers' thinking, which
already crosses the pipe, stays off the screen until there is a screen
for three of them.

The same release makes every slash reply one of two shapes: a one-line
ack, or the list shape of SPEC_COMMANDS 13. Six replies were prose that
the TUI painted as plain text. The bare `effort`, `role` and `approve`
are choice sets now, a head with the count and the active one and a row
per choice with the active marked `[~]`. `sessions show`, `sessions
summary` and `rem show` are details: a head naming the thing and a row
per fact with the key in the id slot, a transcript's messages numbered
as rows with their further lines, thinking and calls as continuation
lines, a memory's content as plain lines after its facts. One painter
draws all of it and still knows no command's name; `choices` and
`detail` join the list helpers. The piped frontends print the same text,
the model reads the same summary, and a row with no text no longer
carries trailing spaces. The sessions tool's reply words name the new
shape, which moves the wire prefix; the prefix golden is updated
deliberately and the menu sits at 13,921 of 14,000 characters.
## [2.11.6]: the store answers before the model

The decision store had become the busiest reader in the tree: 1,110
pending bash rows waiting on a model whose confidence carries no signal
(0.48 average on the rows the reviewer approved, 0.56 on the ones it
denied), 132 of them pending beside an identical settled twin, and 998
of 1,151 commands beginning `cd <path> &&`, so one command arrived at a
hundred spellings and matched nothing. A question whose answer was
already on file was asked again, because nothing looked before the
model did.

Two rules close it. The proposed state carries the normalized command —
one leading `cd <path> &&` (or `;`) stripped, once, and only when the
path it names is the workspace the call named or under it, runs of
whitespace collapsed, ends trimmed — so twins of one command share a
state while a `cd` that leaves the workspace stays in the state: the
risk answer is workspace-relative by its own words, and `cd
~/Projects/rig && rm -rf build` must not twin with `cd /etc && rm -rf
build`. And before the decider is called, the queue asks the store what
is settled: the most recent approved or denied row for the same site,
question id and state answers a twin, which lands a final row (the
store's answer, confidence 1, decider `reviewed`) and proposes nothing
— the model is never asked a question the store has already answered,
and a denied row's answer is its correction. A store error on the read
falls through to the proposer: the read fails open and the queue never
blocks.

## [2.11.5]: the map forgets a vanished file, and says a sentence once

The 2.11.4 walk left a 1.3 GB graph store behind for the home directory
as a project, with eighteen thousand symbol rows whose files lived
under some other root. Every pack-by-task walked those rows, failed to
open each file, and said each one, on every call. Two rules close it. A
symbol whose file is gone is a stale row, not news: the live refresh
and the task pack drop the file's rows the moment they find it missing,
and say nothing, since the write path already did that for an index of
a deleted file. And the queue says any one sentence once for its life,
so a fact that holds for a thousand rows is one line in the transcript.
The home store itself was removed by hand; nothing recreates it, since
`index` refuses a directory that is not a project.

## [2.11.4]: the map stays inside the project and says a thing once

Two walks of the wrong size, found the same evening. `rem index` from a
directory that is not a project walked whatever `RootOf` fell back to,
which from the home directory is the home directory: every Go toolchain
under `go/pkg/mod`, every file of every project, and on quit one notice
per remaining file saying `context canceled`, because the walk treated
a cancelled context as a per-file error to say and continue. The
session could not exit until the walk ran out of disk. `ProjectRoot` is
the one rule for what a project is (a `go.mod` above the directory, or
a git worktree); `index` refuses anything else by name, and the walk
returns the context's error at the first cancelled file, so a quit is
one line. `RootOf` keeps its fallback for the per-file touch, which is
bounded by the file it was given.

The other walk was the transcript. Opening a JavaScript project with no
`typescript-language-server` on `PATH` put one notice per file the code
map tried, a few thousand lines of the same sentence. A missing server
is a fact about the session, not about the file: the queue says it once,
naming the language and that its files stay unmapped until the next
start, and skips that language for the rest of the queue's life. The
doubled `graph: graph:` prefix on the queue's notices goes with it,
since the source rides the event.

## [2.11.3]: the edit shows its diff again

The TUI's edit block previews the arguments as a diff: the old side as
`- ` lines in the error color, the new side as `+ ` lines in the success
color, each side eliding by the head/tail rule. When the edit tool
became a batch of chunks (`edits`, 2.9.x) the preview kept reading the
top-level `old` and `new` that no longer existed and quietly showed
nothing, so an edit landed as a receipt with no red or green. The
preview walks the chunks now, in order, each one its red then its green,
and a four-chunk edit reads as four small diffs above the receipt. The
operator asked for it back after watching GLM land them all day.
## [2.11.2]: the setters leave

The quality pass every few versions, this one on what 2.11.0 and
2.11.1 grew. Nothing changed behavior; the suite is the receipt, 54
packages with the race on.

The setters died. AGENTS.md has said from the start that a value is
whole when it is constructed, and five survivors disagreed:
`graph.Queue.SetScorer` and `SetPackCaps`, `decision.Decide.SetParallel`
and `PackScorer.SetParallel`, `tool/python`'s `SetCwd`. Each was called
exactly once, at wiring; each was a constructor argument that was never
given one. The queue takes its pack caps and its scorer as options now,
and the decision stack stands beside the frontend it reads: the
decider, the decide tool, both queues, the reviewer and the pack scorer
are built together, and the queue and the rem tool are constructed
after them, so the scorer exists at the queue's construction site and
the reviewer's land is the reviewer's own method, not a closure over a
variable that filled in later. The kernel's `WithParallel` still bounds the tool batch; it no
longer restamps the decision fan-out on the way through, because
nothing in the tree ever set it and the stamp was always the default.
An embedder who set it saw the decision tools follow; in-tree nobody
could. Named, not changed.

`middleware/toolset`'s table had two doors where its contract is one:
`Set` for the tools and `SetPlugins` for the provenance, called as a
pair by the wire and the plugin reload, so a reader between them saw
new tools with stale names. `Swap(tools, names...)` is the one atomic
door; the wire, the reload and the model switch state the whole truth
in one lock. `store/graph`'s 692-line queue.go split by responsibility:
the pipeline stays in queue.go, the pack reads are pack.go, the live
refresh is live.go.

And the one duplication worth a package: `store/rem` and `store/graph`
carried byte-identical `tokenize`, `gramsOf` and `ftsQuery` — the two
stores' fuzzy arms spoke two copies of one contract. `store/fts` is the
leaf now; a token, a gram or a reserved operator means the same thing
in both stores, and the tests that pinned the shape moved with it.

The tests got the same pass where the code was touched. The reviewer's
bite tests waited on 200 ms sleeps for fires the fake had already
signaled; `waitFires` receives the signal instead. The queue's
unbounded-caps refusal is pinned at construction. The stamped-parallel,
born-cwd and swap-atomic invariants carry their names. One e2e builds
the binary, points `RIG_DECISION_URL` at a stub and asserts the run
reaches the model server — the first cut of this pass shadowed the
decider above the root and refused every start with a decision URL,
and no test wired main, so the review caught what the suite could not.

Named, not changed: `r.pluginTools` still holds the discovery-time
plugins after a reload, so a model switch after a reload rebuilds the
table from the stale slice. One field away from a fix, and a behavior
change — the operator's call.

## [2.11.1]: three rows a bite

Three things after the 2.11.0 tag. `reviewBatch` defaults to 3, down
from 10: a bite is a glance at the queue mid-work, three rows at most,
and the backlog converges across turn ends instead of in one sitting;
the setting is unchanged for an operator who names it. The delegate
tool's `Fill` goes: 2.10.1 reworded its text without the
`{default_model}` slot, so the wrap replaced nothing; `scheduler` is the
one tool whose schema still names the default, and the docs say so.

And the fix the first live bite found. It ran eleven minutes and settled
nothing.
The worker read the contract, said "find the verdict mechanism", tried
todo, bash, plugin and rem, was refused each time by its allow list,
and never called `verdict`, because the tool was registered but never
on the table the model reads: the table walks the native names, a
conditional native is appended by hand the way `decide` is, and
`verdict` was not. The table carries it now, with a test that wires a
fleet and reads the menu. It is offered only to a worker whose allow
list names it, a reviewer or a bite, so a run-job worker on the same
pipe sees the menu it always did; the run-job golden is what caught the
first attempt, which offered it to every piped worker.

Named, not changed: a bare fire's menu still shows every native tool
and refuses all but one. The allow list enforces; it does not shape the
menu. Whether it should is the operator's call.

## [2.11.0]: the fleet posts

SPEC_EVT opened with "an operator on the phone steering while a
delegated worker reports back" and its rule is that everything that
waits on the world posts. The fleet did not: the swarm kept a roster,
a heartbeat field, a throttled status emitter and its own goroutines;
the delegate streamed bytes to a file; the reviewer scraped verdict
lines from stdout; `Notice` and `SwarmNotice` were two events to the
frontend. Five dialects for one sentence: a member tells the room
something and whoever cares subscribes.

The operator's `broadcast` module is lifted in as `broadcast/`, its
comments with it, and placed on the event loop. `Room`, `Member`,
`Transport`, `Message`, `Encoder`; the transaction, the state machine,
the WAL, the queue, the clock and the client stay behind, because rig's
durable truth is the todo log and the decision store and one slot needs
no quorum. A message carries a `core.Event`, so the kinds are the types
rig already has; a message with none is a heartbeat. The loop transport
posts a send as one closure at the room's priority and acks on the
post: the queue is the durability, an event stays until the consumer
runs it, no buffer is sized, and a second heartbeat before the first
ran is not posted. `Broadcast` fans out with one ack per member and
names the members that missed it. Members thread the caller's context;
the room takes its transport in the constructor; `lo` goes, the package
is stdlib over `core` and `evt`.

The kernel owns the engine (SPEC_EVT 8, the named reopening of
`loop/`): `rig.WithEngine`, the loop running on the kernel's engine
when it has one and minting its own otherwise, and the priorities named
in the kernel: `PriorityInput` 90, `PriorityStream` 50, `PriorityTool`
50, `PriorityFleet` 30 for the room, `PriorityReview` 10 for the
decision review, so a worker's message runs in the gaps of a turn and a
review bite starts only when nothing else is queued. The root assigns,
the loop reads.

The swarm moves onto the room (the third commit). The controller is
the supervisor member, each worker a member by its id; every state
change is a closure posted at the fleet's priority, so the mutex is
gone; the router is a posted dispatch, deduplicated while one waits;
the worker's heartbeat line becomes a heartbeat message to the
supervisor; what the supervisor said to the frontend it now publishes
(the notices, the status snapshots, and the stderr lines as `Notice`
with source `swarm`). `core` gains `Snapshot` (the same reopening as
`Notice`): an event whose latest value is the whole truth, and the loop
transport keeps one pending per sender for a heartbeat or a snapshot,
which is the status throttle without its 250 ms clock. The root is the
frontend member: it subscribes once and hands each event to the current
recorder, with the panic recovery that lived in the swarm. Gone: the
`Frontend` seam and its resolver, the `status` emitter and its clock in
the swarm, the stream files under the scheduler home, the controller's
mutex and `set`/`bump`/`countOf`. The delegate tool still carries the
emitter until it moves onto the room.

The reviewer bites on the loop and the delegate tool speaks in the room
(the fourth commit). A turn end posts the bite at `PriorityReview`, the
lowest rung, so it starts only when nothing else is queued; the bite
takes its rows on the loop, fires in a goroutine, and posts the settle
back, so a four-minute review never holds the operator's input; one
bite is posted at a time; the reviewer's `Run` goroutine and its wake
channel are gone, the engine and the context are constructor
arguments. The delegate tool is a room member and publishes its status
snapshots there; the `Notify` closure, the `swarm/status` emitter and
the last 250 ms clock in the tree are gone.

The heartbeat crosses a pipe (the fifth commit). A spawned worker gets
the write end of a pipe as fd 3 and its member id in `RIG_FLEET`; the
one-shot frontend heartbeats through a `broadcast` pipe transport on
it, one JSON frame per line through the encoder, and without a fleet
it has no heartbeat and no ticker. The parent reads the frames through
the same transport: `Delegate` publishes each as the worker's member
(`DelegateInput.Member`, the swarm's worker and the delegate tool's
worker rows are members now), so the supervisor and the delegate tool
stamp the heartbeat from the room; the run-job runner touches its
stall watch per frame. The parent closes its end after the spawn and
waits for the reader's end-of-file, so no frame is lost behind the
result; the child marks the fd close-on-exec, so no tool's subprocess
holds the pipe open. Gone: the `rig: heartbeat` stderr line, the
`Observe` byte observer on `DelegateInput`, and both greps for the
line. The stall watch still reads stdout bytes too: a worker's text is
still progress.

One notice, one voice (the sixth commit). `core.SwarmNotice` was a
second type for the idea `Notice` already is, so it folds in: the
swarm says everything as `Notice` with source `swarm`, through one
`say`, and the TUI, the web and the CLI render it as they render every
notice, `source: text`, which is the same line the swarm's texts
already began with. The `loud func(string)` closures the root built for
the graph queue, the decision queue, the reviewer, the pack scorer and
the decision recorder, and `root.notice` behind them with its three
branches, go: each holds a `broadcast.Member` from the kernel's named
ids (`rig.MemberGraph`, `rig.MemberDecision`; the frontend, the
delegate tool and the minted worker range beside them) and
`broadcast.Say`s, which is one `Notice` published to the room. The
frontend member hands it to the current recorder, or prints it to
stderr while there is none yet, and a headless worker's stderr line is
what it was. The doubled `decision: decision:` and `graph: graph:`
prefixes in the operator's notices go with the closures, since the
source is on the event now. The run-job process opens the same room
for its recorder's one rare line. The web client renders every
`notice` in its feed, where before it showed only the swarm's.

The verdict is a message (the seventh commit). A reviewer used to end
its reply with a line the parent scraped, `verdict: accept` for the
swarm, `verdict: <id> approve|deny` for the decision bite: two parsers,
two vocabularies, and a line written slightly wrong counted as a dead
worker. Now the worker calls a tool. `tool/verdict` is registered only
in a process that holds a fleet pipe; its call crosses as `core.Verdict`
published as the worker's member, the swarm supervisor stamps it on the
worker and the decision reviewer keys it by row, both on the loop, and
a reject without a reason is refused before it crosses, so the model
fixes it instead of dying. The decision bite still runs bare (no
report-back) with `verdict` as its only tool, and its reply ceiling is
derived from the call's arguments at their worst instead of the old
contract's lines. The room mints an id for a member that needs no name
(`Room.Mint`, below every id it has seen), which the delegate tool's
workers and the bite's fire use, so `MemberMinted` goes. Gone: both
parsers, the `verdict:` sentences in the briefs, `DelegateInput.NoTools`
(now `Bare`: no brief, and only the named tools), and the reviewer's
`Fire` returning stdout.

The first commit is the package and its tests; the second the engine;
the third the swarm; the fourth the reviewer and the delegate tool; the
fifth the pipe; the sixth the one notice; the seventh the verdict. The
stdout scrape is gone from the tree.

## [2.10.2]: claim says what claim does

The todo description's claim clause read "the next available task" — a
word the store never implemented. Claim walks the board in order and
takes the first pending task with no unfinished blocker, where the
blockers are the unfinished tasks on either end of its
`requires`/`blocks` links; a done peer never blocks. The description
carries that rule now, and the drift that let it wander from the
source is named where the words live: `tool`'s PACKAGE.md gotcha says
a behavior change to a wired tool diffs its registry entry in the
same change.

- **tool**: the todo entry's claim clause is the store's rule; the
  PACKAGE.md gains the drift gotcha.
- **cmd/rig**: the golden_020 request bodies regoldened.

## [2.10.1]: the words the model reads

The seven wordiest tool descriptions were rewritten trigger-first: the
first sentence says when to reach for the tool, rules follow one to a
sentence, and edge cases and refusal semantics come last. The decide
trigger left the system prompt — the tool's own description is the one
source of truth, so the GuidelineContributor link carrying it is gone.
Scheduler's pinned-model default is named where it lands on the wire,
in the schema's model description; the description speaks of the
resident default in words.

- **tool**: seven registry entries reworded (read, edit, todo, rem,
  scheduler, delegate, decide); schemas untouched, except none —
  scheduler's keeps the `{default_model}` slot Fill replaces, now the
  only place the slot lives.
- **decision**: the guideline seam link is gone; a description test
  replaces the seam test.
- **cmd/rig**: the wire tools prefix and the golden_020 request bodies
  regoldened; the scheduler test asserts the schema now.

## [2.10.0]: the pack takes a task

`rem pack` needed a symbol or a file, and the question a model arrives
with is about a task: "who builds the decision queue", "where does the
pack budget live". The target now also takes a sentence: a target with
a space that names no file is a task. The candidates come from the map
through the two lexical arms recall uses — an FTS5 virtual table and a
trigram shadow over each symbol's name, kind, package and file, fused
as recall fuses them (reciprocal rank) — and the tables land in the
graph store's extra.sql, the generated ddl and domain untouched.

With `decisionUrl` set, candidates are scored with one yes/no —
"Does this symbol matter for the task?" — through the fan-out decide
uses (now exported as `decision.FanOut`), bounded by the kernel's
Parallel; the candidate's item is its live signature and file. No more
candidates go out than the pack could load: the blocks (definition,
callers, callees — what actually spends the cap) are built down the
rank until the result cap the root passes in is spent, and only that
prefix is scored — the pack loads the blocks it built, so the menu
task's minute of fan-out becomes about ten seconds. The lexical rank
is the pack's spine: the
yes set loads live in rank order, the server's judgment promotes
within the rank and never re-orders it; an answer whose confidence is
under one half is unsure and the unsure are listed by name at the end
so the model can pack one by hand, a confident no is not listed, and
the lexical top fills the rest of the budget, so a scored pack is
never worse than an unscored one.
Every answered candidate is one pending row in the decision store (the
new site `pack`, the server as decider, the task and the item as state)
and the reviewer settles them at turn end as it settles bash rows, a
deny naming the right answer. Unset, pack by task uses the lexical
candidates alone, in rank order, and records nothing. No automatic
pack: the model asks, and one guideline joins the system prompt only
when decisionUrl is set. The lexical containers are schema version 2:
the migration rebuilds them from `symbols` on open, so a store mapped
before this release searches without a re-index.

The target description grows by the clause "the symbol, the file, or
the task as a sentence"; the menu budget holds.

## [2.9.6]: one word for an edit's piece

The registry calls the unit of an `edit` a chunk ("one line per chunk";
"as the earlier chunks leave it"); the tool's own replies and refusals
still said hunk (`hunk 1: replaced 3 byte(s)`, `hunk 2 of 2: old matched
0 times`), so the model read one word in the menu and another in the
result. The edit tool says chunk everywhere now, in its replies, its
refusals and its names (`editChunk`, `applyChunks`), and the docs that
describe it follow. The diff engine keeps its hunks: a unified diff's
`@@` hunk is a different thing with its own name.

## [2.9.5]: the definition is an interface

2.8.1 lifted the model's words into `tool/registry.json` and gave every
tool a `tool.Definition` to embed. It was a struct with methods, and the
struct leaked at once: the schema had to be a string to keep it
comparable, and `plugin` overrode `Schema()` by shadowing a method on an
embedded value. The operator asked for an interface then; this is it.

`Definition` is now the interface (`Name`, `Enabled`, `Description`,
`Schema`); the registry's entry is its one concrete, unexported, with
the JSON struct as the type the way lift's `JSONConfig` is; `Fill` is a
Definition wrapping another with a slot replaced, delegation by
embedding, instead of a method copying a struct. A tool embeds the
abstraction, the door implements `Schema()` because that is its
contract, and a test can hand a tool any Definition without the
registry. The wire does not move: the goldens, the prefix sha and the
menu budget are untouched.
## [2.9.4]: the review takes a bite

Run j31 woke the reviewer at a turn end with 264 pending bash rows and
fired them as one 177 KB prompt: 1,344 s of reasoning, `killed by signal
13` on 131 KB of streamed stderr, stdout empty, nothing settled, and the
same batch re-fired at the next turn end while the operator typed. The
notice said only `the review fire ended exit -1 (timed out false)`.

The wake was right and the bite was wrong. The reviewer still wakes at
the turn end — a landing marks it dirty, a turn end with nothing landed
costs nothing — and one fire now takes the oldest rows up to
`reviewBatch` (settings.json, rows per fire, the default 10; 0 leaves
the reviewer off while proposals keep landing; a negative or
non-integer refuses at start, naming the key), settles what the fire
answers, and leaves the rest pending for the next turn end. Two
ceilings stay underneath the batch, never above it: the reply (one
verdict line per row, bounded by the reviewer row's max output tokens
over a verdict line's token cost, derived from the review contract's
own verdict templates, never a constant) and the window minus its
reserve. A bite that answers rows and leaves the backlog marks the
reviewer dirty, so a quiet turn end with a backlog still takes the
next bite; a fire that answers nothing waits for the next landing, so
a garbage fire cannot spin.

Two fixes ride the fire. The headless worker (`-p`) ignores SIGPIPE,
so a broken stderr costs the reasoning stream and never the run. And
the fire's error carries the delegate's reason — the spawnReason, the
signal — and the run log path joined onto the scheduler home, so
`killed by signal 13` is readable instead of a bare `exit -1`.

## [2.9.3]: the prompt rides stdin

`decision: review: fire: delegate: spawn: fork/exec rig: argument list
too long`, at every turn end. The reviewer sizes one fire by the model's
window (about 1.5 MB on GLM) and the delegate handed the prompt to the
child as the single `-p` argument; Linux caps one argument at 128 KiB,
and 262 pending bash rows made a 176 KB prompt. Nothing settled, and the
same fire failed again at the next turn end.

A prompt never rides argv now. `rig -p -` reads the prompt from stdin;
the plain, bwrap and landlock spawns all say `-p -`, the spawn context
carries the text (`WithPrompt`, `PromptFrom`) and `RealSpawn` pipes it.
The `Spawn` seam and the fakes are unchanged; the argv tests read the
prompt from the context. `-p -` with nothing on stdin refuses by name.

`rem pack` refuses two more targets with the words that teach the
shape: an import path (`github.com/…/v2.Kernel`) names the package tail,
and a qualified name whose package has no such symbol names where the
map has it.

## [2.9.2]: the menu in the command's own color, and its words for the operator

The completion menu painted command and verb names in the accent while
the committed `/name` opening paints them in the ember; on `warm` that
was blue beside orange. The names are the ember now, in every theme.

The descriptions the menu shows are the operator's, not the spec's: a
readability pass over every `/command` and verb hint, the way 2.8.1
rewrote the tool words for the model. Each says what the verb does in
plain words first and the shape of the line last (`approve <name>`),
and the house terms that only the specs know (the drain pair, the
soak's vitals, zones, re-deriving a crontab line) are gone from the
menu. The piped frontends and the model see none of this: the
registry, the goldens and the tools' replies are untouched.

## [2.9.1]: every command speaks under its opening line

2.8.3 gave the listing commands the todo block's shape; the one-line
commands still printed bare text. Now every slash command in the TUI
commits the same frame: the ember `/name · args` opening, then its
reply. A one-line ack (`/theme cool`, `/role architect`, `/effort high`,
`/approve manual`, `/new`, `/steer`, `/rem forget`, a `/models` switch,
the plugin verbs, `/swarm start` and `stop`) drops its own `name: `
prefix and reads dim under the opening; a note that rides an ack stays
text; a transcript or the summary vitals keep every line in text; a
refusal paints in the error slot under the same opening. The piped
frontends print the reply as before.

## [2.9.0]: the code map — pack before grepping

The evidence sat in the transcript: to answer "who calls gateOnce" a
session greps, then reads two or three files into context. This release
gives rig a map: `store/graph`, one sqlite file per worktree under the
rig home (`<home>/graph/<repo scope>/<worktree>.sqlite`; the repo scope
minted like todo's — the repo's identity — and the worktree from
`git rev-parse --git-dir`, the `worktrees/<name>` leaf for a linked
worktree, the checkout directory's name otherwise; branches never share
a map), generated through lift, metadata first. The containers are
`symbols` (package or module, name, kind: func, method, type, var,
const; file relative to the project root, line, end_line), `edges`
(from symbol, to symbol, file, line of the use), and `files` (path,
sha256, language, the sha the file's outgoing edge rows were last
rebuilt at). No source text is stored anywhere: the map holds
addresses, and every quote in a reply is read from disk at reply time.
Paths are project-relative; each worktree's store maps its own tree.

Extraction is a seam — `Extract(file) -> symbols, edges` — with two
implementations behind it. Go runs in-process: `go/parser` plus
`go/types`, the package as the unit, a small module importer that checks
module-internal imports from source (one cache per project) and hands
everything else to `importer.Default()`. Symbols always come from the
package-scope AST; edges come from `Uses` (package-scope references,
in-module only), `Selections` (method calls), and the name fallback when
the type-check fails — a selector resolved through the file's own
imports, a plain identifier that names a package-scope symbol. A file
declared `package x_test` is checked and keyed as `<importpath>_test`,
so its symbols never overwrite the package's while its edges still name
it as the caller. A type-check failure keeps what resolved and falls
back to names. `vendor` and `testdata` are skipped. Every other language runs through a
language-server client: a child process over stdio speaking JSON-RPC
(`initialize`, `documentSymbol`, `references`), started on the first
read of a file in that language, one server at a time — touching another
language replaces it — living as long as the session and never on the
loop's thread. The operator installs the servers; rig spawns them
(TypeScript first). For these languages symbols are stored on extraction
and edges are resolved when pack asks, through references, cached by
file sha.

Indexing is the 2.7.0 queue shape: after a read, write or edit of a
mapped file (the `index` middleware, innermost in the canonical chain,
hands the path over once the call has returned without error), a bounded
channel and one goroutine extract that file (its package for Go) and
replace its symbols and outgoing edges in place; an unchanged sha is a
no-op; the call never waits. The queue's drops and errors are
`core.Notice` lines with the source `graph` (2.8.3), never stderr over
the frame. Edges are natural keys,
never synthetic ids, so one file's replace never orphans the edges
another file's extraction wrote, and a dangling edge is a row whose join
finds nothing until the file that owns it is touched again.

rem gains two actions. `index` does not ride the hook's channel: it
walks the project root and extracts every mapped file on the call's own
thread, nothing dropped, and replies with the count mapped when done.
`pack` takes a symbol (package-qualified or bare) or a file path and
replies from the live files: the definition (the lines line..end_line,
bounded by the read ceiling), each caller with its call line, the
callees' live signature lines to the opening brace, then one coverage
line (packages or modules mapped of those present). A cited file whose
sha moved is re-extracted first; a bare name defined in two places
refuses, naming both; the pack is bounded by the read ceiling and
registers no observation, so an edit's drift check still demands a real
read. The description carries the contract sentence: pack before
grepping for who calls what.

The menu stays at 14,000: the description grew by the map's sentences,
the room came from a words pass over todo's and rem's parameter
descriptions, and rem's importance field now says in one line that
strength starts at it and decays. The golden_020 request fixtures move
with the description; the wire sha moves deliberately.
## [2.8.3]: the reviewer fires on the resident model, and notices stay off stderr

Two bugs that showed as one symptom: a torn TUI frame at every turn end,
worse on the phone, and again on ESC mid-turn.

The decision reviewer fired on the settings default model (`dsv4`) while
the session ran on `ox-alpha` and GLM was resident, so every fire was
refused with `a different model is resident`. The fire now names no
model: it resolves to the resident model's row as a scheduled fire does
(2.5.3), with the session's active model as the fallback, and the
delegate's result carries the model it ran on, so the reviewer's name on
the row is the model that actually reviewed.

The refusal was printed with `Fprintln(os.Stderr)` straight over the
TUI's frame, as were the decision queue's drops and errors and the
recorder's store errors. Core gains one event, `Notice{Source, Text}`
(a named reopening of `core/provider.go`, the same door `SwarmNotice`
came through), and every frontend renders it: one dim line in the TUI, a
`notice` frame on the dashboard, a `rig:` line on stderr in the piped CLI
and in a headless run. The root's background writers go through
`notice(source, text)`; stderr only when the session is headless.

The slash commands share one design language in the TUI. `/sessions`,
`/models`, `/plugins` (and its zones), `/rem` and `/swarm` render the
shape `todo read` taught: a head with the count and the one fact that
matters, one row per item with its id, a marker (`[ ]` `[~]` `[x]`
`[!]`), the text and ` · ` details, and a `· n more · <verb>` footer;
the TUI paints the marker as the todo glyph under the command's
opening line, the piped frontends print the text. Long lists fit the
screen instead of scrolling it: the TUI hands the commands its row
budget, and `sessions list all|<n>`, `rem list all|<n>`, `rem project
<path> all|<n>` show the rest. The `plugin` tool's `list` and `reload`
replies carry the same shape.

## [2.8.2]: plugin and plugins are one tool

Two tools covered one concept: `plugin` was the door to a live plugin
(run it, fetch its contract) and `plugins` was the ecosystem (list,
create, delete, reload). Together they cost 1,445 characters of the
14,000 menu, and the second tool's description repeated the first's
vocabulary.

They are one tool now, `plugin`, with six actions: `run` and `schema`
as before, and `list`, `create`, `delete`, `reload` as the ecosystem
had them. The `Ecosystem` stays as the dispatcher behind the door; it
is no longer a tool of its own. Approval stays the operator's act
through `/plugins approve`; the model has no arm for it. `decide` is
unchanged. The embedded allow default, the README tool table, SETUP.md
and the plugins notes lose the second name; the `/plugins` command
keeps its verbs. A `settings.json` whose `allow` still names `plugins`
starts with one notice and the name is dropped, never a refusal.

The menu loses one tool; the goldens and the tools-prefix sha move
deliberately and only there.

## [2.8.1]: the words are data

The text the model reads was scattered: `read` and `edit` returned their
descriptions inline, `rem` kept a const beside its schema, `scheduler`
built its from a function, and `todo`, `python`, `web`, `delegate`,
`sessions`, `plugin`, `plugins`, `view` and `decide` each kept their
own. A words pass meant grepping twelve packages, and the house shape
(what it is, `Guidelines:`, `Reply:`) was a convention nothing checked.

`tool/registry.json`, embedded, is now the one file holding every
native tool's words: an entry per tool with `name`, `enabled`, `what`,
`guidelines`, `reply` and the `schema` object as it goes on the wire.
`tool.Def(name)` hands a tool its `Definition`, which it embeds to
satisfy `Name()`, `Description()` and `Schema()`; the tool writes only
`Exec`. The description is composed from the three parts, so the shape
is the type, not a habit. `scheduler` and `delegate` fill the default
model into a `{default_model}` slot at construction; `plugin` adds the
live names to the registry's schema. The root derives its native tool
list from the registry's enabled entries, so flipping `enabled` to
false removes a tool from the build's menu without deleting its words.

With the words in one file, the operator took a pass over all of them
in one voice: every word lowercase, the labels (`guidelines:`, `reply:`)
and the system prompt with them, the house shape kept, the facts kept.
Formal names and symbols keep their case: `JSON`, `API`, `URL`, `CLI`,
`HEAD`, `[TRUNCATED]`, the id patterns `tN`/`jN`/`mN`, the cron fields. `python` and `scheduler` had put a blank line before
`Guidelines:` where every other tool put a space; `decide` carried no
`Guidelines:` or `Reply:` clause; `bash` and `web` described their cap
as "a [TRUNCATED] marker that names the full size", now "when capped,
shows a [TRUNCATED] line with the total size". A schema field that
restates a rule from its tool's description now uses the description's
own words (scheduler's `model`, delegate's `workspace`, rem's `query`,
python's `action`, edit's `old`, read's `diff`, web's `target`,
plugin's `action`, decide's `labels`). The three golden_020 fixtures
and the tools-prefix sha move with the words; the menu is 13,973 of
the 14,000 budget.

Tests: every entry composes into the shape and carries an object
schema; every wired tool has an entry and every entry is wired; the
native list follows the registry's enabled order; no `.go` file outside
`tool/registry.go` carries tool words (a file scan). AGENTS.md names
the rule: the model's words are in `tool/registry.json`.

## [2.8.0]: decide — the model hands the sorting to the decision server

The evidence sat in the transcript: 27,035 bash calls and 4,935 reads,
many of them steps like "which of these items match X", where the whole
output fed the context only so the model could sort it. With
`decisionUrl` set — the same setting that already proposes a risk row
per bash call — the live tool table now carries one built-in entry
named `decide` (SPEC_DECISION, the delegate section). The arguments are
one typed question, a choice with a description per label, a yes/no, or
a score, and a list of items. The tool sends one request per item
through the 2.7.0 proposer, up to the kernel's Parallel at once, and
replies grouped: for each label, the items that got it, numbered from 1
with the first line; then the unsure items in full for the model to
judge itself. A choice is unsure when its top probability is under one
half; a yes/no is never unsure, so a question that needs a middle uses
a choice.

The bound holds ahead of any I/O: the items' total stays under the read
ceiling, the read tool's own, and a list at it refuses before a request
goes out. A server error is a refusal the model reads, never a dead
turn — nothing was sorted and no row was written, so the retry starts
clean. Every answered item lands in the decision store as a final row:
site `decide`, the server as decider, its confidence, unsure marked;
none are queued for review. The mark is a column — `unsure`, schema
version 2 — and the migration adds it to a version 1 file with the old
rows reading 0, the same one-time move the rem store made.

When `decisionUrl` is set, one guideline joins the system prompt
through the GuidelineContributor seam: when a step is sorting or
filtering many items against a question you can state, hand the items
to decide instead of reading them. Unset, nothing moves: the fixed tool
menu, the wire sha, and the system prompt are byte-identical, the same
invariant the wire goldens pin. The decide name is native to the
plugins — a plugin file named `decide` collides — and the allowlist
carries it in the embedded default.

## [2.7.0]: the decisions are kept, the answers are reviewed

rig decides all day and kept none of it. The stores held thousands of
bash calls and a couple of dozen gate refusals, and nothing a decision
model could learn from: no question, no answer, no confidence, no
verdict, no outcome. This release gives rig's decisions a place to land
and the one rule that shapes it: an answer is a proposal an LLM reviews,
never an action.

The seam is `decision` (SPEC_DECISION): a question is typed — choice,
score, yes/no — and an answer carries a confidence and its decider. The
store is one sqlite file under the rig home, scoped like todo: a row per
decision with site, state, question, answer, confidence, decider,
session, status, reviewer, the reviewer's answer, and the outcome once
known. The gates record their final rows — approve the operator's
verdict (both of them: the yes is the gold label), perm a denial, paths
an expansion it applied, guard a bound or round-cap refusal, the
scheduler's skip — and recording never changes a decision, a store error
never fails a call.

The proposer is opt-in: settings `decisionUrl` points at a local
decision server, and every bash call then gets a pending risk proposal
(safe, changes, dangerous) written after the call returns — the call
never waits on it. A landing wakes a reviewer, as the swarm's wake does:
when the slot is next free it takes every pending row in one headless
fire and replies one verdict line per row, parsed like the swarm
reviewer's. Partial replies settle what they name and leave the rest
pending; a fire that settles nothing waits for the next landing. No
timer, no poll.

## [2.6.0]: the router reads the queue, the workers run the tasks

The swarm's drain loop was a poll. Each worker claimed on its own
two-second tick, counted empty claims, and exited after the third; the
supervisor grew the roster from the swap's slot read and shrank it as
workers exited. The shape made the swarm a busy-waiter on its own
queue and made worker lifetimes a function of queue luck: a review in
flight kept a worker alive, a fast drain emptied the board and ended
it. This release replaces the loop with a router: one goroutine
attached to the session's todo queue is the only queue reader, and it
runs on events — the start, a task created or completed (the todo tool
calls a wake callback wired at the root), a worker finishing. Each
pass claims a ready task for each idle worker and hands it over;
workers never read the queue and wait on the handoff. `/swarm start
<n>` names the count again (the 2.4.0 per-slot growth is gone), the
claim poll is gone, the empty-claim exit is gone, and the "board
emptied" notice with it — idle workers wait, they do not exit.

The spawn side lost its clock. A worker queued at the llama-server
writes nothing until the server hands it the slot, so the 10-minute
stall kill would have shot healthy workers, and the 2h timeout was a
spend guess on top of a queue of unknown depth. The swarm's spawn
carries neither: the caller's context is the only bound, and
`DelegateInput.Timeout` negative now means no wrapper deadline (zero
keeps the runner default). The same evidence retires the slot gate
everywhere: a second request queues at the server, so the delegate
and the scheduler fire send and wait instead of counting free slots,
`this turn holds the only one` is gone, and one slot hosts the
drain pair — the fleet wiring is now `workers on and the swap
readable`, with an unreadable swap failing closed. With the slot gate
goes the stall kill: the delegate's `Stall` input, the delegate
tool's `stallMs`, and the scheduler tool's `busy` and `stall` keys
are gone (the store keeps the columns and replays historical rows as
written); the timeouts stay, because they are spend ceilings, not
liveness guesses.

The menu paid for the delegate's return to the one-slot wire out of
the scheduler tool: the description and schema lost their wait and
stall text, the `n` knob, and the `workspace` override (a job runs in
the session's workspace; the store keeps the column and the guard).
The tool menu holds under its 14,000-character budget with the
delegate wired on a one-slot box, and the recorded golden says so.

## [2.5.5]: the bare read names what paints

The one-dial rule landed in 2.3.2 with a read that still spoke the
older world. The bare `/theme` echoed the settings key, so a home with
a theme.json and no key painted the file while the read said
`theme: warm (default)` — the dial and the file disagreed in the one
place the operator is told which is active. The root now mints the
read's name from the same facts the paint resolves: the key when one
is set, `custom` when the key is unset and the file stands, empty when
neither, and the command's own words are unchanged (`theme: warm
(default)`, `theme: custom (theme.json)`, `theme: <preset>`). After
`/theme cool` with a file on disk the read says cool, naming the dial
that beat the file. `switchTheme` already wrote the dial's value into
the root; only the construction-time copy of the raw key was wrong.
`ResolveTheme` keeps returning the palette alone and the command
package is untouched; a consumer that ports the root takes the same
mint at its construction.

## [2.5.4]: the docs read the code back

The docs had drifted from the code they describe. Three tool counts
lived side by side — SETUP said "13 non-worker" in one table and "15"
in another, USAGE said "17, 19 with a fleet", the README said 13 — and
the `--version` examples still printed 1.5.8. The 2.1.x consolidation
shrank the toolset; none of the counts followed it.

The code is the one source now, and the docs say what it says:
thirteen native tools register on a model row without vision, fourteen
with `vision: true`; the embedded default allow-list carries twelve
base names, and `scheduler` and `delegate` join it where a fleet
stands and no operator allow does — a menu that never names what is
absent, refusals that do. The README's measured numbers name their
population (the fleet: every workspace's store, not one), the SETUP
examples print a placeholder instead of a release that has long moved
on, and the pages site carries the same thirteen. The measured section
stopped bragging in point-in-time: the 2,925-turn day and the 208M line
gave way to the whole curve — 4.1B prompt tokens over every recorded
turn (1,529 sessions, the earliest on 0.2.0), 99.0% served from cache,
and the era table where the consolidation shows as the kink: 1,499 new
tokens a turn in 0.x, 711 in 2.x. The site's rating strip carries the
same number; under it, the table.

## [2.5.3]: the resident resolves to a row

Every unnamed fire under a resident the models table names differently
died in 3ms with `models: no row for "glm5.3-flash"`: `ResidentModel`
returns llama-swap's canonical id, the models table names that model
by its alias (`ox-alpha`), and both the runner and the delegate
passed the canonical id straight to `-model` — a run row and a fire
log for a spawn that never lived, repeated every interval the fleet
sat on that model (j8 burned 39 fires in a day, and the daily
optimizer skipped with it).

One resolver serves both call sites now: the resident id resolves to
the models-table row whose id is the resident id or one of its
llama-swap aliases (`canonicalModels` already carried the alias map).
The gate still takes the canonical id; `-model`, the run record, and
the fire log take the row id. A resident with no row never spawns:
the fire records a skip naming it and the known rows, a delegate
refuses the same way. An unreachable swap is not that case — the
delegate falls to the session's default as it always did (a
remote-row worker never needed the swap), the fire skips as a failed
check.

## [2.5.2]: the line spells the unnamed job

The 2.5.1 unnamed job had no spelling from the user command:
`/scheduler update` marshals every value as a string, so
`update j8 model null` stored the literal id `null` and
`update j8 model none` stored `none` — both then fail at fire time as
a model the resident server does not hold, while the job's own intent
was to stop pinning one. The tool's update already treats `null` and
`""` as the clear (the unnamed job); the line could produce neither.

`model none` now marshals `"model": ""`, the unnamed job the fire
resolves (resident first, else the settings' model); any other value
stays a string. No other key changes: `name none` is a job called
none. The shape strings name it: `[model <m>|none]`.

## [2.5.1]: the scheduler's unnamed job

A scheduler job named its model at create: `Create` refused an empty
model, the tool filled the session's model when the call named none,
and the dashboard's create did the same. The cost showed every GLM
session: jobs pinned to the worker model skipped with "the GPU is held
by glm5.3-flash" while the machine sat idle, because a stored id never
re-reads the room. The delegate already resolved an unnamed model
resident-first; the runner did not.

Create and update now accept no model and store the empty string: the
unnamed job. Update takes `model` as a pointer, absent means unchanged
and `model: null` clears a named model back to unnamed. At fire time
the runner resolves an empty model — the resident model first, else
the settings' model (`RIG_MODEL` overlays as everywhere else) — and
passes the resolved id to the busy gate and to `-model`; nothing
resident and no default is a skip that says so. A named model keeps
the rule it had: the gate refuses a model the resident server does
not hold, naming the holder, so a named job never evicts. The run
record and the fire log name the resolved model (schema 7 adds
`runs.model`, the same presence-keyed column add; `runs` shows it
beside the exit and the duration). The tool's schema says the
contract: omit the model to run on whatever is resident, default
`<model>` when nothing is. The dashboard no longer fills the fleet's
model on create, and an empty model field on its job form clears to
unnamed. The request fixtures move with the description; the wire sha
does not (its root wires a fake scheduler surface).

## [2.5.0]: edit takes a list of changes, all or none

Edit's arguments were `path`, `old`, `new`: one replacement per call.
Since 2.1.0 the operator's sessions carried 412 edits; 22% of them were
another edit to the file the previous call had just edited (65 runs of
two or more, one of eight) — each run an extra call, and each call an
extra point where the drift check can fire between hunks. The model
already groups its changes; the tool made it do them one at a time.

Edit now takes `path` and `edits`, a list of `{old, new}` applied in
order; a single change is a list of one, and the top-level `old`/`new`
are gone from the schema. Every hunk is validated against the content
as the earlier hunks leave it, each matching exactly once, before
anything writes: all or none, so a refused call lands nothing and names
the hunk, its match count, and what it found. A later hunk may match
text an earlier one created — the validation walks the content the
earlier hunks leave, not the file as it was. The bounds stand ahead of
any I/O: at most 32 hunks, total old plus new under read's ceiling, no
zero-width old, an empty list refused.

The observation contract is 2.3.3's, unchanged: an unread file with
every hunk matching once applies; a miss on an unread file teaches once
with the whole file, read's bytes and cap; a read file refuses by name.

The reply is one line per hunk, then the path and total bytes replaced,
and the observation is refreshed by the result. The description's
guideline says it: put enough of the file in each `old` to match
exactly once; several changes to one file go in one call, applied in
order, all or none. The wire sha and the golden_020 request fixtures
move deliberately: the edit tool's schema and description are the only
bytes that moved.

## [2.4.0]: the fleet is the resident model

Every slot count rig kept — `workers.json`'s `slots`, `models.json`'s
`concurrency`, the delegate's `Slots`, the swarm's `count`, `busy`'s
`force` — restated one fact that was never rig's to store: the resident
server's `-np`, which llama-swap owns and exposes live at
`GET /upstream/<model>/slots` (per slot, `is_processing`). A slot is
held per request, so an idle session holds none. And the stores said
what that drift costs: every delegate refusal read `the GPU is held by
<model>` — a worker asked for the fleet model while another was
resident.

The counts go. A worker's model resolves at claim time: the named one,
else the resident model, else the session's own default — and the gate
is the live free-slot read, never a stored number. A fire waits for a
free slot up to its timeout, then skips naming the holder; a delegate
inside a turn reads once and refuses (`no free slot; this turn holds
the only one`); the swarm starts one drain worker per free slot and
grows as slots free. Nothing evicts: `busy: force` is retired,
create and update refuse it. `workers.json` and `RIG_MODEL_CONCURRENCY`
are read, ignored, and named once at start — deleting the file silences
the line. `delegate` and the swarm are wired only where a second
request can run — the session's model row is remote, or the resident
server reports more than one slot, one live read of the same `/slots`
at wire time — and settings `"workers": false` turns the pair off on a
capable machine; the scheduler is wired everywhere, and the menu says
nothing about what is absent.

Timeout, stall, and budget stay: they bound a fire, not the fleet.
Remote rows keep skipping the gate entirely; their `concurrency` token
flock goes with the count — the endpoint's own 429 retry is the
backpressure. SPEC_WORKERS is the one page; SPEC_DELEGATE,
SPEC_HOSTED, SPEC_SWARM, and SPEC_CONFIG 12 point at it.

Migration: none — no schema moved. Delete `~/.rig/workers.json` when
the start line nags; move `settings.json`'s legacy `defaultJobModel`
to `model` by hand.

## [2.3.4]: usage carries the model that made the call

The store said one thing and the truth said another: `sessions.model`
held the id the session opened with, never moved, and the usage rows
named no model at all. The last three PR sessions ran on ox-alpha while
every turn in their stores read dsv4. The vitals were unreadable at the
one place they mattered — which model spent the tokens.

`usage` gains a nullable `model` column (schema v5, an ALTER on open):
the id that produced the call, stamped by the recorder from `Done`'s
echo. The discarded attempts of an empty turn stamp their own id — the
`EmptyTurn` event now carries the swallowed `Done`'s model — and the
compaction row stamps the summary call's, which `Compacted` carries in
the same way. A row left modelless by an older build reads, at every
read path, as its session's start model; no backfill, the session row
remains the fallback.

The `/models` switch now moves the open session's row with it:
`Recorder.UpdateModel` writes the new id to `sessions.model` before the
loop applies the switch, so the row names the current id rather than the
open-time one, and a refused write refuses the switch instead of
diverging silently.

The `sessions` tool's `summary` acts on the new column: when a session
in the slice ran on more than one model, a `tokens:` line splits the
slice's prompt+completion totals by the producing id, sorted by name.
A slice of single-model sessions prints nothing extra.

Migration dry-run against a copy of this rig home's 19 stores
(233 MB, 34k usage rows): 19/19 landed on v5, zero failures, the
oldest through the full v2→v5 chain.

## [2.3.3]: edit applies on a match, teaches with bytes on a miss

The read-first refusal is retired. Since 2.1.0 the operator's sessions
carried 332 edits; 57 were refused with "was never read this session:
read it first", and 114 reads existed only to satisfy that rule — an
edit of the same file followed at once. The rule spent 171 calls per
332 edits to catch 2 drifts, and the read-first sentences in bash,
read, and edit never changed how the model explores: it looks with
bash and edits what it saw there. The refusal asked whether the model
had seen the file, but the tool's own contract already proves it: an
`old` that matches exactly once cannot come from a model that never
saw the bytes.

An edit of a file the session has not observed now applies when old
matches exactly once. On a miss — never, or more than once — the call
does not refuse: the reply is the file's text exactly as a read would
return it, the same 1 MiB cap and truncation marker, ending with
`[edit: <path> was not read this session; its text is above, now edit
it]`. The reply records the observation — the digest and the
remembered bytes — so the edit that follows is drift-checked like any
other read: an external change between the teaching reply and the next
edit still refuses, naming the diff against what the model was shown.
The 2 drifts the old rule caught, it caught through the drift check,
which is untouched. A file the session has read keeps today's
refusals whole: a mismatch names the occurrence count, a change since
the read names the drift, and the 3 old-mismatch refusals now teach
with bytes instead of dead-ending the turn.

The refusals about the call, not the observation, stand ahead of the
teaching reply: a zero-width `old` and a missing file error loudly as
before, and a standalone exec (no threaded session) behaves as it
always has, because there is no session to record an observation into.

The tool descriptions move with the behavior: read is "the way to look
at a file", and edit describes the teaching reply — one refusal
trigger fewer on the menu, and the explore-refuse-read-edit dance
becomes edit, edit. The wire sha and the 0.2.0 request goldens move
deliberately: the fixtures' schemas, messages, and tool count are
byte-identical, only the two descriptions moved.

Tests pin the new contract at every edge: the exact-once apply without
a prior read, the mismatch and the ambiguous old handing back the
bytes, the reply's byte-parity with read (capped and uncapped), the
drift check on the edit that follows the teaching reply, the follow-up
edit applying without a separate read, and the two call-shaped
refusals surviving. The description pins name the new phrases.

## [2.3.2]: /theme, the operator's dial over the three presets

The theme was a config-file affair: the `theme` key in settings.json
naming a shipped palette, theme.json overriding it when present, and
no way to move between them without editing a file and restarting. The
web dashboard has had the two-preset picker since its first release —
warm (the default) and cool — persisted in the browser's localStorage;
the terminal, the frontend the operator actually types into, had
neither a picker nor a persistent choice.

`/theme` is the operator's verb over the same vocabulary, one command
in the standard set: bare shows the active preset (`theme: warm
(default)`, `theme: custom (theme.json)`), and `theme warm|cool|
custom` sets it. Warm is the default palette — yesterday's `oled`
table under its preset name, `oled` kept as a legacy alias for
settings keys and theme.json bases already in the wild. Cool is the
dashboard's cool palette ported to the slot table, hue for hue. Custom
is theme.json in the rig home, read at selection time: a missing file
refuses by name (`theme: no theme.json in the rig home (~/.rig/
theme.json)`) and nothing is persisted; a malformed one refuses in
config's voice.

The choice is persistent where the theme's key already lived:
`config.SetTheme` writes the `theme` key of settings.json in place —
the file's other keys preserved, the write atomic (temp then rename),
the value one of the three presets. Resolution is now one dial: a set
`theme` key names the theme alone (`custom` names the file), and the
file is the theme only when the key is unset — the old "the file wins"
rule is replaced, because a dial that loses to the file it is supposed
to override would lie at the next start. An operator with both a
theme.json and a settings key keeps their file verbatim; `/theme
custom` is how they return to it.

The TUI repaints on the spot: the root's seam resolves the new theme
through `tui.ResolveTheme` and pushes it over an optional
`RepaintTheme` interface assertion on the frontend — the loop, the
commands, and the other frontends never see it. New output paints in
the new theme; committed scrollback keeps its bytes, the
scrollback-native rule. The command set is fourteen: `theme` rides
`All()` with its three presets as the TUI menu's argument hints, and
the piped CLI's unknown-command line names it in the known set.

Tests pin the three voices (the bare read, the set reply, the named
refusals), `config.SetTheme`'s preserve-and-atomic-write contract, the
warm/cool tables slot for slot, the one-dial resolution (the dial
beats the file; `custom` requires the file; no dial and no file is
warm; no dial with a file is the file), and the root's switchTheme
persisting, refusing, and repainting. The golden fixtures that typed a
named settings key to carry a glyph override now resolve the file
alone, where the file is the theme.

## [2.3.1]: the separating blank belongs to the tool block

The blank line that separates tool blocks was flowed on `ToolStart`. In
a parallel turn every start arrives before any result, so the gaps
landed together at the top of the wave and the results committed back
to back — a read block's closing `read ✓ 0.0s` standing directly under
the next `● read ·` opening. Sequential turns never saw it, because
start and result alternate.

The separating line is the block's now: on `ToolResult`, when the last
committed thing was a tool block (`lastSlot == slotAfterTool`, read
before the result reassigns it), one newline flows before the block
commits, and the `ToolStart` newline is kept only when the last slot
was text or reasoning — where it closes the open line or lands as the
single gap before the first block. Every rendered turn, sequential or
parallel, carries exactly one blank row between consecutive tool
blocks, prose keeps its gap before the first block, and no boundary
gets two. The golden streams are byte-identical: a sequential pair
renders exactly as it did. The TUI's session harness pins the wave:
two parallel results one blank apart, a sequential pair unchanged, and
prose then a block keeping its single gap.

## [2.3.0]: recall degrades by relevance, corruption refuses by name

The full-text arm of recall joined its tokens with AND, so a natural
query ("how does the scheduler handle drift") matched only a memory
containing every word — and in the operator's stores, where 141 of 144
recall queries run four words or longer, five queries returned nothing
and the trigram arm carried retrieval alone. The fusion that outranks a
hit both arms reach almost never saw two arms, and nothing in a reply
named which arm had found a hit, so none of this was measurable from a
session. The arm now ORs its tokens and orders by FTS5's own rank
(bm25): a long query degrades by relevance instead of to nothing.
Reserved words stay quoted, and the arm's cap and the scope and kind
filters are unchanged. Each hit line names how it was found — fts,
fuzzy, or both — one word after the strength, and the rem tool's Reply
sentence says so: `the hits with their ids, strength and arm`.

`daysSince` read an unparseable or future `last_consolidated_at` as
fresh, no decay — the one silent error path in the package, over
timestamps the store writes itself. Recall and consolidation now refuse
the memory by name, `rem: mN has an unreadable last_consolidated_at;
prune it by id`, instead of ranking a corrupt row as the freshest; an
empty timestamp is still "never", not corruption, and a refused recall
does not reinforce. A property test walks query lengths 1 to 20 over a
stored memory plus noise words and holds the full-text arm on the
memory; the refusals are pinned in prune and in recall, query and
browse.

The store's PACKAGE.md carries the two named trades: recall is a write,
since it reinforces its hits inside its own transaction (recalls
serialize with learns; a swarm contending on it records the access
after the read rather than dropping it), and the mN id is a local
handle minted from a counter — a mesh converges rows on content_sha256,
which learn is already idempotent on, and each node mints its own mN.
The golden_020 fixtures carry the one Reply-sentence line.

## [2.2.0]: the retry guard named by its rule

The guard list in the embedded default system prompt named the retry
guard without its rule: `an allowlist, a retry guard, an approval gate,
a plugin landing zone`. Read cold, a model could take the guard for a
per-tool mute — three failures of a tool and the tool stops — when the
bound is narrower and kinder than that: the streak keys on identical
args, a corrected call always executes, and the strike note appends to
the real error rather than replacing it. The default prompt now says so
in the one clause: `a retry guard (three identical failing calls to one
tool in a turn exhaust the bound; a corrected call always executes)`,
the guard's PACKAGE.md words. The pinned default string in
`config/settings_test.go` carries the same words; no behavior changed.

The failure etiquette moved in beside the refusal rule. After `never
reach the same effect through another tool.`, the default prompt now
carries the two sentences the operator's agent contract kept for
itself: `When a tool fails, read the error and work out why before
calling again. Do not retry blindly, and stop when the environment or
the plan is wrong.` The guard bounds the blind retry; the prompt asks
for the diagnosis first. A rule in the system prompt is read every
turn.

## [2.1.11]: the plain-words contract and the plan rule

The todo tool's contract had grown by append: 2.1.6 through 2.1.9 each
added a clause to `tool/todo/schema.go`, and the description and the
link fields read like a contract — `Task id (tN), exact text, or
position in this create that this task cannot start until done; null
clears the link; omit when none` — while `tasks` and `status` still
carried the older plain-words pass. The words are now one set. The
description decides: the one line of what the tool is, its verbs, the
argument shapes, the reply's shape. The fields explain: each field's
description says what that field is, in plain words — `action` is
`What to do.`, `tasks` is `The tasks to add, in order. Required for
create. An empty list clears the queue.`, and the two links each carry
their own sentence (`The task this one waits for: its id (tN), its
exact text, or its number in this list, where 1 is the first. Omit when
none; null removes a link.`). `blocks` is a field, not a sentence: its
meaning is no longer folded into the description's prose, and the
description's link line is the one plain sentence `A task can wait for
another: set requires on the one that waits.` The unknown-link refusal
names the forms in the same words: `(a link is tN from a reply, a
sibling's exact text, or its number in this list)`.

The planning rule moved too. On 2026-09-29 the operator's AGENTS.md
lost its todo line as redundant with the todo description, and sessions
that used todo fell from 55 of 60 to 4 of 7. A rule inside a tool
description is read only once the model has reached for the tool; a
rule in the system prompt is read every turn. The system prompt now
carries the one sentence, after `A capability you build twice belongs
in a plugin.`: `For any job of three or more steps, or one that touches
several files, plan it in todo before the first edit: create the tasks,
start one before working on it, complete or fail it when done, and
leave the queue empty at the end.` The description keeps its Guidelines
sentence as the arrival text: the system prompt decides when, the
description is there when the model arrives. No behavior changed: the
verbs, the store, and the refusal rules are untouched. The three pinned
request bodies (`golden_020`) carry the new words, deliberately, and
the tool menu's byte count goes down with them.

## [2.1.10]: the read truncation facts

The read tool capped a reply at 1 MiB and appended `[output truncated]`
with no numbers, so a model reading a big file in ranges could not know
how much had come back or what the next offset was. The cap now cuts at
a line boundary when one exists inside it — the reply ends at a complete
line — and the marker carries the next step: `[output truncated: N of M
lines; continue at offset X]`, X the offset the next read needs
(offset+N, N the complete lines in the reply and M the file's line
count, the same "lines" the offset and the past-the-end refusal use). A
single line longer than the cap still falls back to a rune boundary, so
no rune is ever split, and gets its own marker — `[output truncated:
line N is longer than the 1 MiB cap; slice it with bash]` — because a
read can never make progress on it: bash owns that line. The read
description names the reply for what it is — the file's text, exactly as
edit will match it — and the cap sentence left it: the marker carries
the cap, so the description carries only the rule. The guideline names
the tool the model picks between — use read, not bash (cat or sed) —
and says why: edit checks the file against what you read, and a bash
read leaves no observation for it to check against. No behavior changed
elsewhere: the suite is the gate at every commit.

## [2.1.9]: positional links

A 4B model planned five steps as one create with `requires: "1"`,
`requires: "2"`, the numbering it wrote in its own plan. The store
refused (`requires '1' not found`) without saying what a link is, and
the model fell back to thirteen single-task creates. Within one
create, a bare number N is now the task at 1-based position N of that
call's `tasks`, tried after the id and the exact text: ids are always
`tN`, so no link that resolved before changes meaning. A position that
lands on its own task refuses as a self-link, the replay path resolves
positions from the logged payload and skips a self-link the same way,
and an unknown link's refusal now ends, once, with the forms a link
takes: `(a link is tN from a reply, a sibling's exact text, or its
position in this create)`.

The same session also called `start` and `complete` without an id; the
verbs keep requiring one. The big models copy the id from the reply
every time, and a second way to name the same act is the kind of menu
growth the words pass removed, so that half was left out.

The link fields' descriptions and the tool's one-line link sentence name
the new form; the three pinned request bodies (`golden_020`) carry them.
SPEC_TODO_EDGES 1 is amended, named 2.1.9.

## [2.1.8]: the workspace vocabulary and the cwd bucket

The todo tool refused every write when the session's cwd was not in a
git repo (`no project: … is not a repo, so its queue is shared by every
session started there`), though the store beneath it already keys a
non-repo directory by its path and serves it — the same queue a
`project: "~"` write reaches — and every repo queue is shared by every
session in it too, with `claim` as the door. The refusal guarded
nothing. A non-repo cwd is now its own workspace keyed by its path, and
writes land there; the `(not a repo)` decoration is gone from the queue
head, the empty reply, and the unknown-id refusal; and the bind report
says `queue: <label> (this workspace; not bound)` when unbound and
`queue: <label> (bound)` when bound. The vocabulary the model reads is
now one set: a workspace is the directory the session runs in, resolved
to the repo root when inside one (worktrees keep sharing), and project
is the field that binds the session to another workspace, given as a
path. The todo and rem descriptions, the project field descriptions in
both schemas, and the session line (`The session's workspace is <cwd>`)
say so; the 2.1.7 name-project sentence and its two tests are gone, and
`OutsideRepo` stays in the store and the binding table with nothing
rendering it.

The same vocabulary now covers every model-facing string: the place is
`workspace` everywhere, and the place field is `workspace` on all three
tools — bash, scheduler, and delegate — described `the workspace the
job runs in`. bash's failure line and refusal, python's kernel
sentence, the embedded system prompt, the session line, the read
`diff` field (`a non-git workspace refuses`), the diff refusal, the
shared `pathguard` voice (the scheduler and delegate refuse `outside
the session's workspace`), the scheduler's list sentence, and its
create refusal (`create requires a workspace`) all say workspace, and
todo's no-cwd refusal does too (`todo: no workspace`). The home is the
`rig home` everywhere, the session line included. The person is the
`operator` (the scheduler's two `user`s), the delegate's worker model
says `the worker model defaults to`, and the todo and rem `project`
fields both read `another workspace, as a path; later calls act there
until you name a different one. ~ expands.` with rem's recall sentence
`this workspace first, then global` and its project clause `when the
fact belongs to a different workspace than the one you started in`,
matching todo.

## [2.1.7]: the session line and the idempotent todo

The session line named the cwd and the home but never said whether the
cwd is a repo, so a session started in the home made its first todo
create with no `project` and learned the rule from the refusal
(`no project: /home/ng is not a repo...`), then retried with the
project set. Outside a repo the session line now adds one sentence —
`It is not a repo: name project on todo and rem calls, as a path to
the repo the work is in.` — using the same repo test `store/scope`
already uses (`InRepo`), fail-closed: an unprobeable cwd reads as not a
repo and the sentence stands. `complete` on a task that is already done
refused with `'tN' is done; read-only`, and the model read the refusal
as "I did something wrong" and spent a thought on it, though there was
nothing to fix. Complete on a done task and start on a task already in
progress by the same session are no-ops that look like success: they
answer with the echo a fresh call would give — the row (`[x]`/`[~]`)
and the queue summary — write no event and run no compaction. A foreign
start of an owned task still refuses naming the claimer; done stays
read-only for every other verb.

## [2.1.6]: the link fill and the API reply

The todo tool treated any string in `requires` or `blocks` as a link
target, so a create carrying `requires: ""` or `blocks: ""` (a model
filling every field) refused with `'' not found`, and the model then
believed a link needed a second call — though `requires` takes the exact
text of a sibling task in the same create. An empty string is now the
same as omitting the field: no link, no refusal. The schema's two link
descriptions end with "omit when none", and the todo description's
links sentence names the one-create sibling rule. `web fetch` returned
a JSON body as raw text up to the cap, so a 77 KB API reply was
unreadable and the model fell back to curl and jq. A JSON response (the
content type, or a body that parses) now comes back as one shape line
first — the top-level type, its keys, each array's length and its
first element's shape — then the compacted JSON, both under the same
cap and [TRUNCATED] marker; the fetch sentence says an API reply comes
back as its shape and a head.

## [2.1.5]: the notch, for real

2.1.3 put the top safe-area inset on the main column in the phone
layout, and two rules further down the same block reset the padding
shorthand and took it back, so the installed app still started under
the notch. The inset now lives in the rules that apply, and a test reads
the last main rules in the phone block and refuses a version that drops
it.

## [2.1.4]: the approve update

An approve of a name that is already installed was a refusal (the
command door) or a 409 needing an explicit replace flag (the dashboard
forge). It is an update now: both doors rename the pending file over
the installed one, one atomic rename, no refusal, no flag, and the
reply names the replacement. The page's "approve with replace" button
is gone with the 409.
## [2.1.3]: the notch

The top safe-area inset lived on the sidebar, and the sidebar is hidden
on the phone, so an installed app started under the notch. The main
column takes the inset in the phone layout now, for the chat and the
store views alike.

## [2.1.2]: the dashboard on the phone

Three phone fixes. The token cookie now lives 90 days instead of the
browser session, and the web manifest is served per request with the
token in its start_url, so a home-screen install on iOS, which opens in
its own cookie jar, signs itself in on first launch instead of landing
on 401. The composer's status row shows the model and the counters, not
the session id, and the input has no placeholder. The sessions list is
two lines per row, the id and date with resume on the first, turns and
exit and label on the second, so nothing overlaps at phone width.

## [2.1.1]: the tools speak in the system prompt's voice

A second pass over every tool description, in the voice the 2.1.0 system
prompt set: an opening sentence that says what the tool does, Guidelines
that say when and how, a Reply that names only what a good call returns,
with the refusals moved up to where the model decides. plugin and plugins
get their first rewrite. The pinned wire and view hashes and the recorded
goldens move with the words. No code path changed.

## [2.1.0]: plain words

Every tool description and the system prompt rewritten in plain sentences,
one tool per commit. The house shape stays: what the tool is, a Guidelines
sentence, a Reply sentence. What left was restatement and internal voice:
todo's description explained sixteen verbs to a model that uses seven and
now says the three-verb life; scheduler's guidelines decide while its fields
explain; delegate says what to do when the GPU is held; web joins the house
shape and loses its exemption; bash, read, edit, write, python, view, rem
and sessions trade arrows and parentheses for sentences. The system prompt
says only what the tools cannot: the harness, its guards, the refusal rule,
the plugin rule, the shape of a finished answer. The menu test now reads
the real menu, since its recorded wire was stale since 2.0.0; the goldens
are regenerated. No behavior changed.

## [2.0.2]: the comment-free refactor pass

The repository read its own comments as one corpus, so every `//` line
is gone from the Go — implementation and tests alike, the only
exceptions the generated projections and the metadata packages — and
each load-bearing rationale now lives in its package's `PACKAGE.md`.
The pass also split the monoliths into one-responsibility files: the
composition root (`cmd/rig`) into `root`/`env`/`names`/`reap`/`status`/
`models`/`plugins`, `store/todo`'s 2197-line store into
`types`/`fold`/`create`/`render`/`verbs`/`read`/`tx`, `frontend/tui`'s
shell into `shell`/`keys`/`prompt`/`notify`/`commands`/`menu`/`paint`/
`frame`, `store/scheduler`'s three files into one per responsibility,
`store/rem` into `base`/`learn`/`prune`/`render`/`read`/`migrate`,
`frontend/web`'s routes into `router`/`reads`/`writes`/`static`/
`plumbing`/`shape`, `tool/python` into `tool`/`proc`/`kernel`/`render`/
`host`/`boot`, `tool/file` into `read`/`write`/`edit`/`state`/`remember`,
`tool/todo` into `tool`/`exec`/`dispatch`/`resolve`/`items`,
`provider/openai` into `provider`/`stream`/`wire`/`messages`,
`swarm` into `controller`/`drain`/`emit`/`verdict`, `config` into the
settings shape and the parser, `tool/web` into the fetch seam, the
guard, and the extractor, `frontend/cli` into the input seam, dispatch,
and notify, and `tool/rem` into `schema`/`tool`/`args`. No behavior
changed: the suite is the gate at every commit, and the docs
(`PACKAGE.md` inventories, the README's package list) now name the new
layouts.

## [2.0.1]: the repair verb

Drift between the scheduler store and the crontab was surfaced in list
but not fixable from inside rig: a line lost to a crash between the
crontab write and the store commit, an edited cron, a paused job whose
line went live were all manual crontab edits. `scheduler repair`
re-derives a drifting job's crontab line — `UpsertLine` with the job's
cron and the wired runner command, then `SetPaused` to match the state.
It is a crontab write only: no event, no state change (the state is
what it is being repaired toward). One id repairs that job — a removed
or done job refuses `nothing to repair` in the store's voice, and a job
with no drift replies `'jN' is in sync` and installs nothing; no id
walks every job and repairs each drifting one, one reply line per
repair, `nothing drifted` when none. The reply lists the drift it
fixed, verbatim from `driftOf`. The scheduler tool carries the action
in its enum and one description line; the `/scheduler` command verb
carries it too (`repair [id]`); the dashboard gets
`POST /api/scheduler/repair` (id optional) beside the other four doors
and a repair row control shown only when the row carries a drift line.
The module path is now `github.com/mrsirg97-rgb/rig/v2`, so dependants
can pin 2.x.

## [2.0.0]: the dashboard is the third frontend

`rig serve` is rig with the page as its terminal. The web server
satisfies the loop's frontend seam (`Input`, `Notify`, and the TUI's
optional doors: ask, steer, interrupt), `serve` composes the whole root
exactly as the terminal does and hands the server to the loop, so a
session started from the phone is an ordinary session in the same
store, resumable from the TUI, on the same `settings.json` model,
workers, and commands. The loop's events ride a server-sent stream as
sequenced frames (deltas coalesced, results capped by name, a ring for
reconnects); a prompt posts to `/api/chat`, a `/command` dispatches at
once, a line during a live turn steers, and manual approvals are
answered in place. The page is rewritten in the TUI's grammar: the home
view is the live session (the `❯` prompt, the folded reasoning, the
tool blocks, the status line, the breathing activity label), the
sidebar folds into a bottom tab bar on the phone (chat, sessions, todo,
jobs, swarm, more), sessions resume into the chat, models switch from
the table, swarm is a live view, and the todo render parses the scoped
reply. Two palettes ship, `warm` (the terminal's) and `cool` (orbit's).
A web manifest, the Apple meta tags, and the icons make "add to home
screen" install it as an app; a native wrapper is one allowed origin
away. The tests cover the seam end to end through the routes.

## [1.7.4]: the todo default read is the present

The default queue read was the ledger: every finished row rendered in
front of the open work, on every read, for the model and the operator
alike. The default is now the present — open work first in queue order
(pending, active, review, failed: failed is open work, keeps its ✕
marker and counts toward open, so retry stays reachable), then the
done work related to it (every done task reachable from an open task
over requires/blocks, nearest hop first), then the recent done, newest
terminal event first, until ten done rows show in total
(`DefaultFinishedShown`, one phone screen: SPEC_CORE). When the chains
alone exceed ten, the ten nearest hops win; anything hidden gets one
dim hint line naming the largest window that would show it —
`· 480 more finished · todo list finished 100` — and the head becomes
`[rig] 6 open · 10 of 490 finished shown · next: t497`, with the
finished clause omitted when nothing is done. Retirement is a view,
not a state: a hidden done task still resolves by id, its links
resolve, and notes and show work on any id.

The new read is `todo list finished <n>`: the n most recent done,
newest first, default 10, capped at 100 (`FinishedListCap`, one tool
result: SPEC_CORE) — the same in the terminal as `/todo list finished
<n>`; an over-cap count refuses naming the range. Read all stays for
the operator only.

The TUI's todo block parses the new head and the hint line: the one-row
transition echo (`→ 't1' auto-started and completed` plus the affected
row and the summary) renders the note dim and the row through the same
task-line painter the queue block uses, and a reply the parser does not
recognize keeps the opening and prints dim beneath it — never the bare
string, so rows wrap by the TUI's own rules, never the terminal's. The
fixture drives every shape from store/todo's real renderer through
tool/todo. The wire goldens re-pin once with this release; version
moves to 1.7.4.

## [1.7.3]: the two web tools fold into one `web`

web_search and web_fetch were two native tools, two allow entries, and
two wire slots for one capability family: search a local SearXNG, fetch
a public URL. They are now one `web` tool with `action` `search|fetch`
and one `target` field (the query, or the URL); the per-action
optionals (`maxResults`, `maxChars`, `timeoutMs`) ride the same object,
the description says search "<query>" and fetch <url> one line each,
and the reply shapes are unchanged — search's compact JSON and fetch's
capped text with the named TRUNCATED markers.

The fold touches every name-keyed surface: `nativeToolNames`, the
embedded allow default (`config/settings.json`, now keying on `web`),
and `concurrentNatives` (`web` takes the slot; neither tool was on
orbit's fire wire). Approval is per tool name, so fetch is allowed with
search — one allow entry, one gate, no way to split the pair.
`tool/web` keeps its engines (the SearXNG call and the guarded fetch,
its seams and every voice), and `commit.go`'s detail line now shows the
query or the URL for `web` by action. The wire goldens re-pin once with
this commit. Version moves to 1.7.3.

## [1.7.2]: the TUI speaks ember and the todo block parses its own queue

The TUI's committed openings were still the accent blue: the tool rows
(`● bash`), the compact glyph, the `/command` opening, and the todo
progress bar. Rig's colour is the ember — the status rows, the footer,
the prompt glyph, the greeting — so the openings and the bar now paint
the ember, and the accent stays for highlights only (the effort
ladder, the menu's candidates, markdown headings).

The todo store's summary now prefixes every queue with its scope tag
(`[rig] 2/5 done · next: t3`), counts tasks in review in the head,
marks a review row `[r]`, and says `claimed for review by`; the TUI's
parser was anchored on the bare count and accepted only `[x!~ ]`, so
the todo block fell back to raw text on every reply. The head regex
now carries the scope tag and the review count, a review row renders
its `[r]` as the review glyph in warn, the review claim splits like a
plain claim, and the scope tag renders dim before the ember bar. A
bare `queue: rig (bound)` report prints as one dim line, never a
block.

The todo render tests no longer hand-write their reply text: one
fixture drives store/todo's real renderer (and tool/todo's bare
report) through a real store, so the parser and the store cannot
drift again. The activity line's spinner is gone: the label alone
(thinking, bash, edit, compacting) breathes within the ember — a
twelve-stop sine table from the ember down to a darker step of the
same hue and back, one stop per animPeriod, one colour per frame,
never per character; where the stops collapse under the 256-color
downconvert the activity toggles two stops at the same cadence. The
version moves to 1.7.2.

## [1.7.1]: the crontab tag is home-scoped

The crontab tag was `# pane-scheduler:<key>`, a leftover from the port:
two rig homes (~/.rig and an embedder's ~/.orbit) sharing one crontab
emitted indistinguishable lines, and a key like `j1` meant something
different in each home's store. The tag is now
`# rig-scheduler:<home>:<key>` with `<home>` the 12-hex short sha1 of
the rig home path: the homes own disjoint lines and a key only means
something inside its home, hashed over the cleaned path. The reader and
the writer see only this home's new-tag lines; an old-tag line belongs
to nobody until the one-time migration on the scheduler store's next
open claims it, and it claims a line only when the key is a job in this
store's event log and the runner command on the line is this binary's,
so two homes sharing a key never take each other's lines. The migration
runs through the runtime's own crontab seam: the web frontend's store
cache now takes the server's crontab instead of opening a real one, the
hole through which a test suite with a fake crontab could rewrite the
operator's. The version moves to 1.7.1.

## [1.7.0]: the tool menu reduction

The menu shrinks to the observation path. `ls`, `find`, and `grep` are
gone from the wire and `tool/fs` is deleted: bash `ls`/`find`/`grep` is
the shell's, and read is the observation path — the drift-checked read
(with offset/limit for a range), not cat or sed, for any file you may
edit, because a bash read leaves no observation. `diff` folds into read
and edit: read's `diff: true` appends the file's git diff against HEAD
(or `no changes` when clean), edit's drift refusal keeps its capped
diff, and the `diff` native tool's schema and registration are deleted
(the `last` verb, a call's newest result against its previous
observation, dies with it) — `tool/diff` keeps the engine (`Diff`,
`Files`) as the package read and edit use. The wire goldens re-pin once
per commit that moved the wire, never before.

## [1.6.0]: the session listing seeks

`state.ListSessions` aggregated per session through correlated
subqueries over tables with no seek path, and computed them for every
session before the sort and the limit: 10.5s on a 170-session,
24k-message store, the same with a limit of five. Every rig start in that
repo paid it (the claim reap lists sessions), and the dashboard paid it
once per store for its workspace list, past its 5s read timeout.

The state store gains `metadata.ExtraStatements()` beside its generated
DDL, the todo store's pattern: indexes on `messages(session_id, role,
seq)` and `faults(session_id)`, IF NOT EXISTS, so an existing store gains
them on its next open with no schema bump. The listing selects the n
newest sessions first and aggregates those n only. The dashboard's
workspace list reads `state.Cwds` (distinct cwd per store) instead of the
full listing. Measured on a copy of the same store: 13ms with the
indexes, 3ms with the limit-first shape.

## [1.5.9]: the region resets after a capped aim

A pane that shrank under a tall live region (the phone's keyboard
opening with the command picker up) capped the repaint's cursor-up at
the viewport, and the terminal clamped the cursor at the top; the rows
of the taller paint stood above, out of view. When the pane grew again
(the keyboard closing) a phone terminal brought them back into view,
and the next repaint aimed only as far as the trimmed region it had
painted while short — the picker and the status rows stood twice.
`live` now marks a capped aim and makes the next repaint a viewport
reset: cursor-up by a full pane, clear below, the region painted from
row one. The transcript rows that pane showed are the price (the
scrollback keeps them); tmux, which returns no history on a grow, sees
the same reset and nothing else changes. `TestLiveRegionSurvivesPhoneKeyboardShrinkAndGrow`
replays the shrink and the grow through the test terminal.

## [1.5.8]: the status rows tick while idle

`frontend/tui` gains `WithStatusTick(d time.Duration)`: while the TUI
waits for input (no turn streaming, no compaction), the status function
is re-read every d on the Input loop and the region redraws only when
the rows changed. The embedder's rows used to change at command time
only — a background fire wrote a snapshot the next command would show;
now a write the status function sees lands on the next idle tick. Zero
is off (the default; rig's own main never sets it), and the recapture
rides the Input loop, which a turn's start leaves, so the tick never
fires mid-turn; a buffered tick lands immediately when the next Input
starts. The Used reset stays at the session boundaries (`/new`,
`sessions resume`).

Tests: a status callback that flips a row after two 10 ms ticks
redraws with the new row under a 10 ms tick without any command, the
unchanged rows never repaint, and a streaming turn blocks the tick
until it ends.

## [1.5.7]: the empty 5xx before the first token is retried for every row

`provider/openai` retried 429/5xx with backoff only for hosted rows; a
local llama-swap proxy could answer 502 with an empty body before the
first token (a keep-alive race with llama-server — nothing reached the
model), and the local row faulted the turn. A 5xx whose body is empty
now retries for every row under the hosted policy: the identical request
body, exponential backoff from `RetryBase` (500ms, 1s, 2s by default)
with the row's jitter, under the hosted 3-retry bound. The retry lives
at the status gate before the stream: once any streamed byte has
arrived a failure is never retried, and a 5xx carrying a body is still
a real error — it faults immediately on a local row.

## [1.5.6]: the embedder's footer rows

`tui.StatusIn` gains `Rows []string`: the embedder's footer band. The
rows render under the status line behind the same four-cell dim rule the
swarm band uses (nothing when empty, so rig's own main is unchanged).
The status recaptures after every successful command — the embedder's
commands included — with the Used reset staying at the session
boundaries (`/new`, `sessions resume`); `docs/EMBED.md` lists the field
beside `WithCommands` and `WithStatus`. Rows recapture at command time
only: a mid-turn tool effect shows stale until the next command, while
the swarm band stays live because it rides a notify event. A turn-end
hook is a later version.

## [1.5.5]: the welcome title is an embedder door

`frontend/tui` gains `WithTitle(name string, rows []string, tagline
string)`: an embedder replaces the welcome block's block-letter rows
with their own and adds a line under them (default: the rig rows, no
tagline). The ascii glyph fallback prints the plain name (`name`,
"rig" for the default), never the first art row, and `docs/EMBED.md`
lists the option beside `WithCommands` and `WithStatus`.

## [1.5.4]: the swarm band densifies below the footer

The TUI band moves below the existing footer rows behind a short dim
rule and densifies each row (`workers 1 · +7 ✓0 ✗0 · w1 t423 12s` /
`reviewer 1 · ⧗0 ✓0 ✗0 · w2 — —`); `/swarm`'s replies say `added N
agents` and `stopped N agents`; and every abnormal spawn end records a
reason on the run.

- **The band** (`frontend/tui`, SPEC_TUI 3a): one row per role below
  the status rows, separated by a four-cell dim rule (no rule and no
  rows when nothing runs). No new colors: labels, markers and
  separators dim, counts text, ✓ the success slot, ✕ the fault slot;
  the glyph switch carries the ascii fallback (`....`, `~` for the
  review clock).
- **The replies** (`swarm/`, SPEC_SWARM 4): `swarm: added N agents
  (role X · model M)` — one phrasing whether the swarm was empty or
  running (`agent` for one, `agents` for more, never `started`) — and
  `swarm: stopped N agents`.
- **The heartbeat resets on each spawn**: a restarted task shows a
  fresh age (`—` until the new run's first beat) instead of the dead
  run's last heartbeat.
- **Run reasons** (`store/scheduler`, the j23 investigation): the
  runner captured no signal and recorded no reason when a spawn's
  process group died — the only proof of a stall or timeout was a log
  marker, and a cancel or external signal was a bare exit -1 with
  reason NULL. `RealSpawn` now captures the signal from the wait
  status, `Delegate` and `RunJob` record the reason on the run
  (`killed after timeout`, `killed after stall`, `canceled`, `killed by
  signal N`), the spawn's process group carries `Pdeathsig` (a hard
  runner death does not orphan the worker), the swarm controller's
  context derives from the session context, and `cmd/rig` stops the
  swarm on session teardown so those deaths are recorded before the
  process ends.
- **README.md**: the measured stats refresh (720 swarm lines, 34,336 of
  Go, 53,815 of tests).

## [1.5.3]: the docs catch up to the thesis

The README, the docs, and the landing page now say what rig is: a small
operating system for agents.

- **README** (`README.md`): the top screen becomes the thesis, "A small
  operating system for agents. The kernel is a few hundred lines.", with
  `## measured` (each number names its mechanism: 99.1% cache hit over
  2,925 turns, 297M of 299M prompt tokens, 208M on 2026-09-22, 7k
  byte-stable preamble, swarm 723 lines, 34,301 lines of Go, 53,626 of
  tests), `## what's different`, and an OS mapping table. A new
  `a day with rig` walkthrough covers first prompt, tools, the queue,
  memory, schedules, the swarm, and resume. The tools section says the
  default menu is 17 built-in tools (`view` vision-gated, `scheduler`
  and `delegate` fleet-gated), and the dashboard bullets match the real
  web surface (plain-text create, start/complete/retry, transcript open).
- **docs/EMBED.md** (new): rig as a Go module, the core seams,
  `loop.Run`, the five seams the root wires, a worked HTTP-service
  example (kernel, board-backed job, in-process worker, one process),
  the freeze, and local vs hosted. The example compiles.
- **docs/SETUP.md**: the models.json block gains a hosted row example
  (`remote`, `provider`, `baseUrl`, `apiKey`, `concurrency`,
  `reasoning`, `providerPin`, `cacheControl`, `retries`).
- **docs/USAGE.md**: the /swarm section names the hosted-row path (a
  remote worker skips the local swap and the busy probe, riding the
  row's `concurrency` tokens) and gains a short hosted-rows paragraph
  (auth, retry, cost in the usage line, reasoning field names).
- **site/index.html**: the title, og:title, and hero become "a small
  operating system for agents"; the stats refresh (417 loop lines,
  34k/53k code/tests, commit count) and the freeze line names the real
  story (core frozen at 1.5.0's bytes, loop open to pure addition with
  named reopenings).
- **core/PACKAGE.md**: the 1.5.0 hosted extension is the named reopening,
  closed when the gate re-froze core at 1.5.0's bytes, not "the named
  reopening of the frozen surface" (the re-freeze, 8441fd8).
- **The freeze gate** (`frontend/tui/freeze_test.go`): the frozen-surface
  loop honors the allowlist's PACKAGE.md entry, a PACKAGE.md change in
  core/ or loop/ is a docs change, not a reopening.

## [1.5.2]: the todo board grows two edges and a notes door

The task queue's one dependency became two named links, `requires` and
`blocks`, and `read` stopped inlining note text.

- **Two edges per task** (`store/todo`, `tool/todo`, SPEC_TODO_EDGES): a
  task carries `requires` and `blocks`, one each, each an id (`tN`) or
  exact text, null clears. `requires tN`: I cannot start until tN is
  done; `blocks tN`: tN cannot complete until I am done. Blocked
  means a task's requires target is unfinished or any task whose blocks
  names it is unfinished (pending, in_progress, review, failed), so
  `claim` takes only unblocked pending tasks and `complete`/`accept` on
  a blocked task refuse naming what it waits for. `create` refuses
  loudly naming the tasks: an unknown link, a self-link, and a cycle
  through either relation (`cyclePath` walks both). The waits-for graph
  is `requires` t -> required and `blocks` target -> blocker; the read
  line shows `· requires tN`, `· blocks tN`, and `· waits for k` on a
  target. Old `dependsOn` payloads (create events and compact
  snapshots) fold as `requires` at replay; the snapshot carries both
  links and note times.
- **The notes door** (`store/todo`, `tool/todo`, `/todo`): `read` no
  longer inlines note text — a task with notes shows `· N notes` under
  it, and the new `notes <id>` action lists them in order with their
  session and time, headed by the task's link lines; a task with none
  replies `no notes on tN`. `read <id>` renders one task, summary-only,
  and points at `notes`. The swarm brief's `TaskInfo` still carries the
  full notes; workers need them.
- **Schema 4** (`store/todo`): `task_deps` gains the `kind` column
  (requires|blocks), the disposable projection rebuilt from the log in
  every transaction. `EdgeMigration` (3->4) drops and recreates the
  projection table; the log carries the edges, so replay is total.
- **The TUI's todo render** (`frontend/tui`): the parser dims the new
  suffixes (`requires`/`blocks`/`waits for`/`claimed by`) and keeps the
  note-count line.
- **Tests**: seven leaves blocking a root (claim and complete of the
  root refused until the last leaf is done), a requires-chain, both
  links on one task, cycles refused with the task names, old
  `dependsOn` payloads folding, replay across compaction, note counts
  and the notes action (order, session, time, the no-notes reply), and
  read-one's summary-only pointer.

## [1.5.1]: the swarm survives its first status frame

The controller captured the frontend at wiring time (`Frontend: r.rec`),
but `r.rec` is assigned later and `swapIn` replaces it on `/new` and
`/resume` — the first `SwarmStatus` frame hit a typed-nil recorder and
panicked in `Recorder.ensure`, crashing the session.

- **The swarm's frontend is a resolver** (`swarm`): `Opts.Frontend` is
  `func() core.Frontend` (the `Models` idiom), resolved on every notify,
  so a controller wired before the recorder exists emits safely once it
  does and a session swap routes its frames to the new recorder. A
  panicking frontend is recovered into a stderr line — the drain worker
  keeps draining.
- **The recorder tolerates a nil receiver** (`store/state`): `Notify` on
  a nil `*Recorder` is a no-op, so a seam wired before the recorder
  exists cannot crash on `ensure`.
- **The delegate's notice seam resolves at call time** (`cmd/rig`): the
  delegate's `Notify` no longer captures the recorder's method value at
  wiring time.
- **Tests**: a controller wired with no recorder drains a task and
  routes the next frames once the recorder appears; a session swap
  routes notices to the new recorder; a panicking frontend leaves the
  drain loop running with the panic loud on stderr; `Recorder.Notify` on
  a nil receiver is a no-op.

## [1.5.0]: hosted mode — remote OpenAI-compatible endpoints

The provider already spoke the OpenAI wire; hosted endpoints (OpenRouter,
DeepSeek's API, any remote OpenAI-compatible server) needed what a local
llama-server does not: auth, retry, cost, per-provider reasoning field
names, and a remote spawn path that never touches the local swap. Model
rows now carry where they run, and swarms and scheduled jobs take dollar
budgets.

- **Model rows gain their run site** (`models`, `config`): `remote` (bool),
  `provider` (a name implies remote), `baseUrl` (required for a remote
  row), `apiKey` (never logged or rendered), `concurrency` (the row's
  token bound, default 1), `reasoning` (`reasoning_content` default,
  `reasoning` for OpenRouter), `providerPin` and `cacheControl`
  (openrouter-only), and `retries` (default 3 for remote rows). The env
  overlay gains `RIG_MODEL_BASE_URL`, `RIG_MODEL_API_KEY`,
  `RIG_MODEL_REMOTE`, `RIG_MODEL_CONCURRENCY`, `RIG_MODEL_REASONING`, and
  `RIG_MODEL_RETRIES`, so a key can live in the environment and never in
  a file.
- **The provider's hosted behavior** (`provider/openai`): `NewWithConfig`
  sends `Authorization: Bearer <key>`, retries 429 and 5xx with
  exponential backoff and jitter under the row's bound (the loop sees no
  event until the bound is exhausted, then the existing loud fault),
  parses `usage.cost` into `core.Usage.Cost`, reads reasoning under the
  row's field names (`delta.reasoning` / `delta.reasoning_details` for
  OpenRouter, `delta.reasoning_content` otherwise), echoes both back on
  later turns, omits `chat_template_kwargs` for remote rows, and sends
  the OpenRouter `provider.order` pin and `cache_control` switch when
  the row asks for them.
- **Cost lands in the usage column** (`store/state`, `core`, `loop`):
  `usage.cost` (schema v4), `RecordUsage`/`AddUsage` take it,
  `SessionUsage`/`SessionCost` read it, the sessions list carries it,
  and the TUI footer shows the session's dollars beside the token
  totals.
- **Remote spawns** (`store/scheduler`, `tool/delegate`, `swarm`): a
  remote row's delegate and job fire skip the llama-swap busy probe and
  `WaitBusy` entirely; parallelism is the row's `concurrency` token
  flock beside the per-session slot bound. The worker resolves the row
  from the shared `models.json`, so a remote row's `baseUrl` and key
  ride the worker without the parent passing them.
- **Budgets** (`swarm`, `store/scheduler`): `swarm <n> budget=<dollars>`
  stops claiming at the cap with a notice; scheduled jobs take `budget`
  on create/update and record a skip at the cap. The delegate and the
  runner record each run's cost (read from the cost column), so
  `scheduler runs` shows the spend and the job's sum is auditable.
- **Tests**: a fake OpenAI-compatible server asserts the bearer, the
  bounded 429/5xx backoff (deterministic base and jitter), cost parsing,
  reasoning echoed under the row's field names, and the remote wire
  (no `chat_template_kwargs`, pin, cache switch); the cost column is
  recorded and summed; a remote delegate and a remote swarm fire never
  consult the swap; a swarm budget and a scheduled job budget stop at
  the cap.

## [1.4.4]: the swarm's progress in the TUI

The swarm ran silently beside the session: the transcript showed nothing
when a task failed or a reviewer bounced the work, and the only progress
read was the bare `/swarm`. The TUI now gets two surfaces, both through
the frontend's existing `Notify` door.

- **The transcript notices** (`core.SwarmNotice`, `swarm`): the
  controller emits one line per decision-worthy event and nothing else —
  a task failed with its note (`swarm: t1 failed — the worker died
  twice`), a reviewer rejected with the reason (`swarm: t1 rejected —
  tests are missing`), a worker died and was restarted or exited
  (`swarm: w1 died — t1 restarted` / `swarm: w1 died — t1 exited`), and
  the board emptied or the swarm exited (`swarm: the board emptied — all
  workers exited` / `swarm: /swarm exited — N workers stopped`). The
  worker failure now notes the reason on the task too.
- **The status band** (`core.SwarmStatus`, `swarm`, `frontend/tui`): the
  controller emits a snapshot on claim, stream bytes, complete, verdict,
  and exit — throttled to a few per second, the exit's last frame always
  landing — carrying the roster and the bound queue's fold counts
  (`store/todo.Counts`, the new structured read). The TUI folds the
  latest into the footer: two rows above the existing status line while a
  swarm runs (`workers 2 · todo 3 · done 5 · failed 1 · w2 t388 12s`,
  `reviewer 1 · review 1 · done 1 · failed 0 · w3 t386 4m`), zero rows
  when nothing runs, one row for a delegate.
- **The delegate's row** (`tool/delegate`): the optional `Notify` seam —
  an interactive delegate emits the same snapshot shape (one worker row,
  zero queue counts), so the band shows the worker row only. CLI and
  oneshot ignore both events (the compat rule).
- **The band is a status-string extension**: no `live.go` line, no loop
  line — the region's existing height-changing machinery covers the two
  extra rows, and the mid-swarm resize test replays the stream under the
  freeze harness.
- **Tests**: each notice with the fake spawn (failed, rejected, died,
  board/stop), the throttled status emission, `todo.Counts`, the band's
  exact rows, the footer growing/updating/returning, the resize under the
  freeze harness, the delegate's snapshot.

## [1.4.3]: the suite is walled off from the operator's machine

One fixture run once wrote a `local` session into the real `~/.rig`
(`sessions/cb051fd0065f.sqlite`, cwd `cmd/rig`), and a test could still
dial the embedded default swap (`127.0.0.1:8090`) — or detect the
operator's live server by binding that port and skipping when it was
busy. The suite now runs walled: every package that opens config or
stores isolates `HOME`/`RIG_HOME` to a throwaway directory, and the
scheduler's dial seam rides a test-only transport that refuses any host
that is not an httptest server before it dials.

- **TestMain isolation** (`testenv`, every package that opens config or
  stores): `HOME`, `XDG_CONFIG_HOME`, and `RIG_HOME` point at one
  throwaway directory for the whole package run, so a fixture that
  forgets its own scratch cannot write into the operator's `~/.rig`.
  The Go toolchain keeps the operator's caches (`GOPATH`/`GOMODCACHE`/
  `GOCACHE`), so the fixtures' `go build` calls stay warm and offline.
  The operator-home fixture probes (the lift checkout, the kernel venv,
  the `.bashrc` landlock probe) read `testenv.OperatorHome`, captured at
  package init, and never write.
- **The refusing dial transport** (`testenv`, `store/scheduler`): the
  `Transport` seam carries `testenv.Transport` in the suite; nil stays
  the production default (`http.DefaultTransport`). `RealFetch` and the
  socket proxy ride it, and any host that is not an httptest server
  created by `testenv.Server` is refused before the dial, so a test
  reaching for the embedded default swap fails loud instead of touching
  the operator's live server.
- **The embedded-default test no longer detects the live server**
  (`cmd/rig`): the swap chain's "neither takes the embedded" case used
  to bind `127.0.0.1:8090` and skip when the port was busy — detecting
  the operator's server — then fired the real worker at it. It now runs
  `RunJob` in-process with a recording fetch seam and a recording spawn,
  asserting the busy check and the worker argv carry the embedded
  default, with nothing bound and nothing dialed.
- **Tests**: the isolation invariant per package (the suite never sees
  the operator home), the refusal (a non-server host refused, an
  httptest host dialed), and `RealFetch` riding the seam.

## [1.4.2]: the spawned worker stops touching the board, and the plugin door pauses

The manual gate and the swarm's worker door both had a gap the model could
walk through: the `plugin` tool ran any live plugin ungated, and a spawned
`rig -p` worker could claim a board entry and complete it — or accept or
reject a review — despite the brief. The gate now pauses the door, and
Worker mode is read/note-only.

- **The plugin door pauses** (`cmd/rig`): `plugin` joins the mutating set,
  so a `plugin run` asks the operator in manual mode exactly like calling
  the plugin by name does. Both entries are pinned in the mutating
  predicate, and a chain test pins that the ask fires.
- **Worker mode is read/note-only** (`tool/todo`): claim, start, complete,
  fail, accept, and reject refuse with one message naming the supervisor —
  findings go in the task's note and in rem. The store's own arms stay as
  they are: the swarm controller and the delegate parent use the store
  directly and are unaffected.
- **Tests**: each refused verb, the door's gate, and the board never
  moving; the golden fixtures regenerated for the new todo description.

## [1.4.1]: the delegate worker dies on the silence, not the clock

The scheduler already kills a fire that writes nothing (1.3.8), but the
delegate path kept one wall-clock bound for both jobs: a swarm worker
still producing output — a long suite, a deep report — was killed at
the 30-minute ceiling while it worked. The liveness bound and the spend
bound are now separate here too.

- **`Stall` on `DelegateInput`** (`store/scheduler`): the silence
  window, 0 = today (no stall kill, the plain timeout). The delegate
  wires it to the same `stallWatch` the runner uses, touched by the
  `Observe` stream: a worker that writes nothing for longer than the
  window is killed as hung, its stderr naming
  `[runner: killed after stall]` and the result marked `Stalled`. The
  timeout stays the spend ceiling; a producing worker is never killed
  for the clock.
- **The spend ceiling is the caller's** (`store/scheduler`,
  `tool/delegate`): the seam's cap moves from the runner's 30-minute
  default to the scheduler's 24h bound, and the interactive tool keeps
  its own 30-minute `timeoutMs` cap at the tool boundary, so a model's
  induced spend is unchanged. The swarm sets `Stall 10m` and `Timeout
  2h`: a worker keeps its slot while it writes, and a silent one is
  gone in ten minutes.
- **`stallMs` on the tool** (`tool/delegate`): the optional silence
  window beside `timeoutMs`; unset is today's behavior. A stalled
  worker errors naming the stall (`delegate: the worker stalled after
  … (process tree killed)`), the trailer unchanged.

## [1.4.0]: the swarm: a fleet of drain workers, supervisor-side

`/swarm` turns the session's queue into a shared work board a fleet
drains. The supervisor is the session's own process: each drain worker
claims a task and spawns a one-shot `rig -p` through the delegate path
(the jail, the socket proxy, the recorded run), then finishes the task
itself — workers submit for review, reviewers parse the worker's
`verdict:` line and accept or reject. The parallelism is the GPU slots,
not the worker count.

- **`/swarm`** (thirteenth command): bare lists the supervisor's
  workers (`w1 worker qwen3.8-workers · task t3 · heartbeat 2s ago ·
  done 1 failed 0`); `swarm <n> [role=worker|reviewer] [model=<id>]`
  starts n drain workers on the session's bound queue — against a
  running swarm a start adds workers, so a worker swarm gains a
  reviewer mid-drain; `swarm stop` cancels the swarm, releases the
  in-flight claims, clears the rows.
- **The drain loop is supervisor-side Go**: `todo claim` → brief (task
  text and notes, with their sessions) → delegate spawn → `complete`
  (worker mode, submits for review) or the reviewer's verdict protocol
  (`accept` / `reject <reason>`, the reason riding the reject note).
  Three consecutive empty claims end a worker; an empty claim while
  another worker is mid-task does not count.
- **The dead claim**: a worker that dies mid-task has its claim released
  through the todo store's Reap door and is retried once; a second death
  fails the task (workers) or rejects it with the reason (reviewers).
  No task is ever left held by a dead identity.
- **The delegate path, three amendments** (defaulted to today's
  behavior): `WaitBusy` — a swarm spawn waits at llama-swap for a GPU
  slot instead of refusing; `Observe` — the worker's stderr streams
  into `<scheduler home>/swarm/wN.stream`, the run stream the
  supervisor's heartbeat reads; `SpawnCtx` — the base context the spawn
  timeout wraps, so `swarm stop` kills the in-flight worker.
- **The one todo read**: `todo.Task(ctx, db, p, id, session)` returns
  the task's text and notes for the brief — the rendered queue is the
  model's surface, not a parser contract. The swarm's review release
  also fixed a store replay bug: the `release` event now folds a
  review claim (the holder clears, the status stays), with a replay
  test.
- **`workers.json`** gains an optional `reviewer` key (same row
  contract as `model`): the swarm reviewer's default model.

## [1.3.9]: the todo store becomes the swarm's shared board

The board is now the place sessions talk about shared work. Three
changes, all events on the existing per-scope log: a claim verb that
takes the next task atomically, notes that any session may attach to any
task, and a review gate between active and done.

- **`claim`**: takes the first pending task whose dependsOn is done (the
  order `next` shows) and marks it active for this session; the reply is
  the task's echo, or `nothing to do`. With `status=review` a reviewer
  takes the first task in review that no reviewer holds. The take is
  atomic: two sessions claiming one task, exactly one wins (the
  serializable transaction plus the FSM check against the freshly
  rebuilt projection).
- **`note <id> "…"`**: appends a note to any task — no hold needed,
  notes are how agents talk about shared work — and `read` renders them
  in order with their session, one indented line each. A note must name
  a task and is bounded (`MaxNoteLen`, 1000 chars) because it rides the
  log and the compact snapshot.
- **The review gate keys on who completes**: an interactive session
  completing its own task lands it done in one call (the complete/accept
  pair is still written, so the log is uniform and replay is unchanged);
  a worker (`rig -p`: delegate or swarm) submitting it lands it in
  review (`[r]`), and `accept <id>` / `reject <id> "…"` decide. Accept
  and reject auto-claim an unowned review task — the same idiom as
  complete auto-starting a pending one — so a delegate's parent reviews
  its workers by read then accept/reject, with no claim step; a foreign
  holder still refuses. `blockedBy` still clears only on done, so a
  dependency in review keeps its dependents blocked; prune still drops
  done only; the summary counts review rows (`· N in review`).
- **Replay and migration**: `claim`/`note`/`accept`/`reject` are events
  on the log; notes ride the compact snapshot; a stale review claim
  releases back to unclaimed review, keeping the status. Schema 3
  (`ReviewMigration`) pairs every historical `complete` with an `accept`
  in event order, so a pre-review log replays exactly — the live board's
  109 finished tasks stay done instead of flipping to review.
- **The `todo` tool and `/todo`**: the new verbs ride the store's shapes
  (`todo claim [review]`, `todo note <id> <text…>`, `todo accept <id>`,
  `todo reject <id> <reason…>`), and `done` now submits for review.

## [1.3.8]: the scheduler kills a stalled worker, not a busy one

The daily optimizer kept getting murdered at an arbitrary minute: it
finished reports in 4-20 minutes and then, when the run drifted into
implementation work, it always outlived its budget. The wall clock is
the right bound for spend but the wrong bound for liveness, so the
scheduler now tells them apart.

- **Per-job stall kill**: `stall` (nullable minutes, same 1..1440 range
  and `-1` reset as `timeout`) is the silence window on one fire. A
  worker that writes nothing for longer than the window is killed as
  hung — the log names it (`[runner: killed after stall]`), a fail run
  like the timeout's note. `timeout` keeps its meaning: the hard
  wall-clock ceiling, the spend bound. NULL stall is "ceiling only", so
  a command job that is silent by nature never surprises.
- **Output is the liveness signal**: every byte the worker writes
  touches the window, so a long silent backtest with no stdout is never
  confused with a hung provider. The `Spawn` seam carries the observer;
  a delegate passes none (interactive sessions keep the plain timeout).
  And the one-shot worker now lives on the contract: stdout is the
  answer only, stderr carries the liveness — reasoning deltas as they
  stream, one line at tool start and end, and a heartbeat while any tool
  runs (30s cadence) — so a worker deep in a silent tool is never killed
  for not printing.
- **Live run tail**: a scheduled fire streams its output to
  `runs/<id>/<run>.stream` while it runs — `tail -f` a long job — and
  the canonical log is written whole at the end; the stream is removed,
  and a run the runner never got to finish leaves it behind. Log names
  now key on the run's start time, so the stream and the log share one
  base name.
- **Schema 5**: `jobs.stall` rides the same presence-keyed idempotent
  migration as `timeout` and `command`, and the field survives
  compaction like they do.
- **The hedge fund's j9** (the run that found this) now sets
  `stall=30` beside its `timeout=120`: silent for half an hour = hung,
  but a working session gets the full two hours.

## [1.3.7]: the docs and the landing page catch up

The 1.3.x surface landed in the code and the specs, but the README, the
setup and usage docs, and the landing page never learned the new tool
count: `view` joined the default allow list at 1.3.0 while the docs still
said 16 built-in tools, and the landing page had no image tool at all.
The package index also missed the leaves the 1.3.x surface added, and
the landing page's numbers were the 1.2.9-era snapshot. The docs now say
what the code does.

- **README**: the tool count names `view`'s vision gate beside the fleet
  condition, and `view` joins the layout's tool list.
- **docs/USAGE.md**: the default allow count is 17 tools, 19 with a fleet.
- **docs/SETUP.md**: the version examples move to 1.3.7; the knob table
  and the allow-list section name the 17 non-worker tools and the
  19-tool fleet default.
- **AGENTS.md**: the package index gains `tool/view`, `tool/sessions`,
  `tool/execwrap`, and `imagemarker` (the one image-marker contract).
- **docs/DESIGN.md**: `tool/view` joins the leaf diagram, and the
  middleware chain names `cutoff` and the `~` expansion at the outer
  edge.
- **site/index.html**: the landing page gains the `view` tool row and the
  seventeen/nineteen count, and its numbers move: the loop is 396 lines
  (loop.go + batch.go), 31k/47k lines of code/tests, 602 commits, and
  1.36B prompt tokens across 120 sessions at 99% served from cache (the
  session store's current totals, same store and metric as the 1.0.0
  receipts).

## [1.3.6]: the sessions tool migrates older project stores

The `sessions` tool opened a project's state file with the build's schema
version but no migration, so any store an older build left behind refused
with "schema version mismatch: the file carries 2, this build wants 3 and
carries no migration". The root migrates on session start; the read tool
never did, and the file stayed unreadable until a v3 session happened to
run in that workspace.

- **the sessions tool migrates on open** (`tool/sessions`): the store open
  carries `state.Migration()` — the same idempotent, transactional schema
  step the root runs — so a read of an older workspace upgrades the file
  first and lists the sessions instead of refusing. The tool stays out of
  `mutatingNatives`: the only write a read can do is the store's own
  bounded, versioned migration, and no session rows are touched.

## [1.3.5]: the suite never dials the real swap

One test could still reach the operator's swap. `TestDefaultJobModelMintsTheFleetAtStart`
ran `rig -p hello` with no `-base-url` and no `RIG_BASE_URL`, so the
embedded default (`config/settings.json`: `http://127.0.0.1:8090/v1`) was
the endpoint; a guard (a canary base URL children inherit, plus a dialer
that fails on 127.0.0.1:8090) caught exactly one `POST /v1/chat/completions`
for the fixture model `local`. The test ignored the child's exit, so the
suite stayed green while the live swap was touched. The leak is closed.

- **the fixture run is hermetic** (`cmd/rig`): the minted-fleet test now
  dials an httptest fixture and asserts the run exits 0 with exactly one
  model call, so a return of the embedded-default path fails loudly
  instead of touching the operator's swap.
- **the vision wire no longer reads the operator home** (`cmd/rig`): the
  view-registration tests pin `rigHome` to a temp dir, so `wire` never
  resolves the real `~/.rig` — the read was safe, but it was the last
  path from a unit test into the operator home, and `rigHome` carries a
  rename of the old config home.

## [1.3.4]: an empty turn is asked again

The evidence, verified in a session store: messages.seq 13171, model
huihui3.8-flash, the assistant turn had content "", zero tool calls, 268
completion tokens, finish_reason "stop". Its reasoning ended with a fully
formed tool call (`<invoke name="edit"> ... </invoke>`) that the model wrote
inside its thinking block without closing it. llama.cpp filed it as
reasoning, so rig got an assistant turn with nothing to say and nothing to
run, and the turn ended; the operator had to type "you good?" to restart
it. Server side was clean: HTTP 200, truncated = 0, nowhere near maxTokens.

- **the empty-turn guard** (`policy/empty`, `core`): a provider decorator
  at the same seam as the overflow retry. A stream that finishes normally
  (`finish_reason "stop"`) with no content and no tool calls is discarded
  and resampled with the identical request, at most twice, then a fault
  with plain words: `model returned an empty turn 3 times (no content, no
  tool call)`, plus `last reasoning ended with what looks like a tool call
  written inside its thinking` only when the last reasoning carries a
  tool-call marker (`<invoke name=`, `<tool_call>`, `<function=`). A
  `length` cut, a fault, and a content turn pass through untouched; the
  loop is byte-identical.
- **thinking still streams live** (`policy/empty`): the deltas of the
  discarded attempt are shown as they arrive; the `EmptyTurn` notice marks
  the discard, and the recorder drops its partial on it, so the store
  never persists the discarded reasoning. The one residue, named in the
  spec: the loop appends one message per stream, so the in-memory session
  message carries the shown reasoning beside the kept turn's; the store
  and the resample's request stay clean.
- **usage still counts** (`store/state`, `core`): the discarded attempts'
  usage rides `core.EmptyTurn` and is added to the session totals (one
  row per message; two discards in one turn merge), while no empty message
  row is written.
- **cancellation is unchanged** (`policy/empty`): a ctx cancel during an
  attempt or a resample closes the stream cleanly, the loop reads its
  normal interrupt path, no fault, no extra call.

The spec is `specs/SPEC_EMPTY.md`; `loop/loop.go` was not touched.

## [1.3.3]: a queue knows whose it is

The scope law says a queue belongs to its project, and the lazy re-scope
could re-key one once a repo was discovered — but the operator's shape
defeated it: `rig` launched in `~`, working several repos by absolute path
(or none at all: a ledger, an inbox). The measured cost was 593 finished
tasks from six projects in one cwd bucket while the repo's own scope held
nothing, and a live claim that could name a session from another project.
Which queue a session works in is now said out loud, not guessed from the
paths a call happens to name.

- **the binding** (`store/todo`, `tool/todo`, `command`): `session_project`
  records a session's queue. Resolution is one order everywhere: the
  `project` a call names, else the session's binding, else the launch
  directory when it is a repo, else its bucket — where a write refuses with
  the rule and a read answers labelled. Naming a project binds by what the
  call did: a write records it once the action succeeded (`→ bound to
  <label>`), a read is a peek that leaves the session where it was, and
  `bind` — `/todo project <path>` — is the declaration itself. A failed
  write changes nothing, the binding included, so a glance at a neighbour's
  queue or a mistyped id cannot relocate a session's later bare verbs.
  `todo <path> <verb…>` is the same door in one line. Resume re-reads the
  binding, so a queue cannot move because a process started elsewhere.
- **every reply names its queue** (`store/todo`): the summary leads with
  `[rig] 3/7 done · next: t4`, and a bucket minted from a non-repo says
  `[ng (not a repo)]`. Inside a repo the name is the repo's, so a
  subdirectory or a second worktree does not rename the project, and a bare
  repository is a repo of its own rather than the cwd bucket its common dir
  would suggest (SPEC_CORE's naming rule, reaching past the empty reply).
- **`prune`** (`store/todo`, `tool/todo`, `command`): the door for the done
  rows a long-lived summary keeps counting. It is itself an event, so a
  replay drops the same rows and the history stays reconstructable; failed
  rows stay (they still ask for a retry) and an idle prune appends nothing.
- **compaction carries the minting counters** (`store/todo`): the snapshot
  now writes `maxId` and `maxPos`, which is the only place the high-water
  marks survive once the create events they were rebuilt from are deleted.
  Without them the next task took the first free id and position — harmless
  while a hole meant the queue had been cleared, and a real hazard with
  `prune`: a pruned id was handed to a new task and a session holding the
  stale id completed the wrong row.
- **the voice tells the truth about `create`** (`store/todo`): the note
  reports the merge it performs (`queue merged: 2 new, 1 already there`,
  `nothing new`, `queue cleared`) instead of the long-standing `queue
  replaced with 1 tasks`, which taught a model that a create wipes a queue
  it never wipes. Semantics untouched — SPEC_UX 1 kept them for replay
  compatibility and left this wording as the one-liner to land later.

No migration re-keys the old buckets (a hash cannot be walked back to a
path, the rule that killed the earlier re-key); `prune` is the door that
sweeps them. An operator who wants the `~` bucket says so once per session
(`/todo project ~`) and it is a project like any other, marked not a repo.

## [1.3.2]: a job may outlive the clock it never agreed to

A scheduler job's every fire ran under one global 30-minute
`DefaultRunTimeout`. Work that legitimately needs longer — a suite over
a GPU that may be busy at start, a ledger backfill, a long build — got
SIGKILLed at the wall with its work done and only its finalize
outstanding, and the run was recorded as a failure the operator had to
read a log to explain. The bound is now a job field: `timeout`, minutes
per fire, stated where the job is stated.

- **per-job timeout** (`store/scheduler`): `jobs.timeout` (nullable
  minutes, 1..1440, refused outside the range by name) rides the
  create/update events and bounds the spawn context at fire time;
  precedence is the row's own value, then `RunOpts.Timeout`, then the
  unchanged 30-minute default. Orthogonal to the kind — a command job
  carries one too. Schema 4 is the idempotent presence-keyed column add,
  the same shape as schema 3's.
- **`timeout` on the tool and the dashboard** (`tool/scheduler`,
  `frontend/web`): create and update take it, update's `-1` resets to
  the default (an absent field stays "unchanged"), and the in-place
  update form carries it — a cleared field submits the reset. The
  detail-line parser scans the payload's parts instead of fixed offsets,
  so `· timeout 60m` never lands in the model field.

The default stays the default: nothing an existing job did changes
behaviour until it states its own bound.

## [1.3.1]: the docs tell the truth about their reach

A source review of the guardrails (pathguard, the chain, the plugin
zone, `web_fetch`, the loop) found the code sound and two doc claims
wider than the machinery behind them. This release states what the
boundaries actually are, welds one invariant that was only
conventionally true, and pins the chain order where it is written.

- **the egress proxy owns the pin** (`SECURITY.md`): `web_fetch`'s
  resolve-and-pin holds for direct dials; with the proxy in use the
  address check runs per hop but DNS resolves there, so the guarantee
  belongs to the proxy. The boundary now says so instead of implying
  otherwise.
- **the file tools' reach, named** (`SECURITY.md`): `read`, `write`,
  and `edit` are unscoped by design and the path boundary is
  normalization, not containment; the doc said "the path boundary"
  where a reader could hear "file paths are gated". The plugin
  provenance rule is the only file-path rule, and `pathguard`'s scope
  (the `cwd` of `delegate` and `scheduler`) is now written down.
- **`pyLiteral` refuses raw line breaks** (`plugins`): the escaping is
  total only inside a Python single-quoted literal, and the callers
  feed it `json.Marshal` output where breaks are already escaped —
  the gap was an unwritten invariant. A raw `\n` or `\r` now panics
  the cell instead of ending the string literal early.
- **the chain order comments its own definition** (`cmd/rig`):
  `canonicalMiddleware` carries the load-bearing invariants (expand
  before validate; cutoff before approval; deny before ask; cap every
  reply) beside the order they describe, next to the order test.

Two review findings were verified and retired unchanged: the drift
cache is bounded by its 16MB pressure cap regardless of session
churn, and `readCapped`'s probe read already reports exact-cap bodies
as untruncated, correctly.

## [1.3.0]: rig looks at images

A model with eyes had no way to use them: every image the agent met came
back as a byte count. `view` reads a picture, and the picture reaches the
model as a real image part, while the transcript keeps only a line.

- **`view`** (`tool/view`): one path argument, one line of reply. The
  source is decoded (png, jpeg, webp, the first frame of a gif), box-filtered
  to at most 1568 px on its longest side, and re-encoded — JPEG for an
  opaque lossy source, PNG for anything with alpha — then stored
  content-addressed at `<RIG_HOME>/blobs/<sha256>` and never rewritten. The
  reply is the marker line: the address, the mime, both sizes, the sent
  bytes and the source. The bytes never travel through the transcript, so
  a screenshot costs its reference, not its pixels, and compaction can
  never drop them; the same bytes always land at the same address, so
  looking twice sends the wire nothing new. Read-only: it never enters the
  file state, never runs in manual mode's way, and refuses over 20 MiB,
  over 16 megapixels, or anything that is not one of those formats — by
  magic, not by extension.
- **the image reaches the wire** (`provider/openai`): the tool message
  keeps its text (the marker), and rig reads the blob back and appends the
  base64 image part as its own user message after the whole tool batch it
  belongs to, in call order, for the vision model's shape. Only a marker
  the `view` tool actually wrote is honored, so a model quoting the format
  gets text; a blob that is missing, unreadable, or no longer matches its
  address degrades to a text note naming it, never a failed request. A
  non-vision model sends the marker as text and no image at all. Encoding
  stays deterministic: the same transcript assembles to identical bytes.
- **the gate is the model row** (`models`, `config`): a row's `vision`
  flag decides everything — `view` joins the tool table for a vision row
  and not for a text row, and switching models moves it with the row. The
  key is presence-aware (an explicit `false` turns it off on an embedded
  row); the embedded table sets it for nobody, so the wire of every current
  model is byte-for-byte what it was.
- **the row on screen** (`frontend/tui`): a `view` call shows its path,
  `2560x1440 -> 1568x882` when rig resampled, and the sent size — no
  picture is ever painted into the transcript.
- **the marker is one contract** (`imagemarker`): the line's format and the
  blob path live in one stdlib-only leaf, because tool, provider and
  frontend that disagree on those bytes break the prompt cache. The box
  filter is stdlib too; `golang.org/x/image/webp` joins the module (its
  decoder reads lossy webp; lossless refuses, named).

`specs/SPEC_VIEW.md` is the contract; `golang.org/x/image v0.45.0` is the
new dependency (only its `webp` decoder is imported). No frozen path
moved; `imagemarker/` and `tool/view/` join the freeze allowlist as pure
code. 80 new cases: 14 on the marker, 29 on the tool, 17 on the wire, 5 on
the TUI row, 7 on the gate at the root (one of them a run of the real
binary end to end), 8 on the row's `vision` key.

Review pass, same PR: the marker line could be smuggled — a filename with
a newline followed by a marker line made `Find` return the smuggled
marker, so `view` refuses a path carrying a control character (before the
stat), `Parse` refuses a `src` carrying one, and the provider honors only
a result that is exactly the marker line it wrote. The honor rule is also
scoped to the assistant message that owns the current tool batch, so a
call id reused on a later `read` can never be honored as `view`; and the
`plugin` door omits its `name` enum when no plugin is live, because
llama-server rejects `"enum": []` and a plugin-less home could not use it
at all. `-allow` stays the execution gate, not a wire filter — the model's
menu is the native table plus the door, whatever is allowed. 8 more named
cases; the wire goldens moved once, deliberately, for the door's
zero-plugin schema.

## [1.2.16]: the pending paragraph wraps incrementally, and no hidden row is measured

The TUI stuttered while a long unbroken reasoning paragraph streamed:
since 1.2.6 every frame re-wrapped the entire pending paragraph, once
per budget-loop iteration and twice once capped, and then measured
each rendered row with a width pass, so a 100k-character paragraph
burned several milliseconds of the 16 ms frame while the server's
rolling rate stayed flat. Greedy word wrap is prefix stable:
appending text can only change the last row.

- **the pending wrap cache** (`frontend/tui`): `pendWrap` caches the
  wrapped rows plus the cells that begin the last row and folds each
  delta into the last row alone; a rebuild happens only on a width
  change or an edit that is not an append. `liveRegionLocked` starts
  the pending block at the viewport height, the budget loop slices the
  cached rows to the cap instead of re-wrapping per iteration, and the
  pending block's row count is its row length, so `rowsOver` never
  measures rows that will not be painted. The rows shown stay
  byte-identical to a fresh wrap of the full paragraph at every step,
  exact-width rows and the trailing-space trim included; the space a
  soft break skips still charges its width to the row it left.
  `TestPendingWrapCacheStaysByteIdenticalToFullWrap` streams random
  word sequences in random chunk sizes and compares the cache to a
  fresh full wrap after every delta, across a mid-stream width change
  and across a commit. `BenchmarkFramePaint100kPendingParagraph`
  paints one frame over a 100k-character pending paragraph and fails
  above 1ms: the pre-fix frame cost 21.8ms, the fixed one runs near
  0.2ms.
- **the line splitter** (`frontend/tui`): `takeClosedLinesLocked`
  scans only the segs the delta appended for newlines and leaves the
  older segs untouched when none carry one, so a long pending
  paragraph no longer pays a re-split of every seg on every delta
  (2.5ms per 30 deltas over a 1563-seg paragraph before, about 1µs
  after).

## [1.2.15]: a wave's starts land with the wave, and the provider caps its error-body read

A review pass over the runtime found the loop telling the frontend a
concurrent wave ran serially: every call in a wave was dispatched at
once, but ToolStart was emitted one call at a time as the result
cursor advanced, so a TUI could never show what was actually in
flight. The same pass found the provider draining a non-2xx response
whole before truncating it to the 256-byte snippet the fault carries,
so a hostile endpoint could pin memory through a large error body.

- **wave starts** (`loop`): `batch.dispatch` returns the exclusive
  end of the wave it launched, and the loop notifies ToolStart for
  every call in the wave at dispatch time, before any result can be
  posted (results queue behind the advancing cursor on the single
  engine thread). Serial dispatch is unchanged: one start, immediately
  before one call. The TUI keys result blocks by call ID through a
  start-time map, so a wave's results render the call that produced
  them rather than the wave's latest start.
  `TestWaveStartsArriveWhileTheWaveRuns` holds a gate closed until
  three starts have arrived; the pre-fix loop emits one and times
  out.
- **error-body cap** (`provider/openai`): a non-2xx response is read
  through a 256-byte `io.LimitReader`, the exact snippet the fault
  carries, instead of whole. The observable fault is byte-identical;
  `TestErrorBodyIsCappedAtASnippet` is regression-proofing rather
  than red-first.

## [1.2.14]: the chain gets a name and the path vocabulary widens

A review pass over the runtime found the middleware chain written out
twice in the composition root: once in `wire` for the kernel and again,
shorter, in `buildSystem` for the guidelines harvest — the canonical
order living in slice literals whose only guard was a sentence in a
PACKAGE.md. The same pass found the path boundary's vocabulary
understated by its own doc (four fields where the doc named three) and
too narrow to catch the name a tool plausibly invents next. The chain is
now one named constructor the root builds through, and the boundary's
field list covers the path-shaped names a call arrives with.

- **canonical chain** (`cmd/rig`): `canonicalMiddleware` is the one
  place the nine links are listed — `toolset.Resolve`, the approval
  gate, the cutoff, the provenance rule, the allow-list, the bound, the
  round cap, the result cap, and `paths` outermost — and both `wire`
  and `buildSystem` build through it, so the system prompt's harvest
  can never name a chain the tools do not run. Order tests pin the
  load-bearing positions: paths run before the gate (the operator judges
  the expanded path), the allow-list sits inside the round cap (a denied
  call still spends the budget) and outside the gate (an unknown tool is
  refused before the operator is asked), and no link contributes
  guidelines (the harvest stays byte-stable). The links and their order
  are unchanged; this names them.
- **path vocabulary** (`middleware/paths`): `Fields` grows `dir`,
  `directory`, `file`, `target`, `dest`, and `destination` alongside
  `path`, `root`, `cwd`, and `project`. A call whose path-shaped
  argument uses one of these now expands at the boundary with no
  per-tool fix; a name outside the list still rides through
  byte-identical, so a `pattern` or a `command` beginning with `~` is
  never mangled. Tests pin both directions: every vocabulary name
  expands, every plausible non-path name does not.

## [1.2.13]: scheduler jobs without the model

Building an autonomous RFP-digest watcher on rig's scheduler hit the
seam this feature closes: a deterministic daily script had to either
burn a model worker on four commands, or drop to a hand-written crontab
line the store would never know about. Hand-written crontab is escaped
state — the harness cannot pause it, audit its runs, or remember it.
The scheduler now takes a `command` payload: a shell line run by
`sh -c` in the job's cwd instead of a worker session, with the same
tagged crontab line, lock, drift, run records, log capture, and
once-fire `done` semantics as a model job.

- **command jobs** (`store/scheduler`): create/update carry `command`,
  mutually exclusive with prompt/model/busy and refused by name; a
  command job's fire skips the busy probe entirely (a loaded GPU never
  delays a cron command) and runs unjailed — the payload is the
  operator's own, the same trust the crontab line itself carries, and
  the jail exists to contain a model's output, of which a command job
  has none. `update` edits a job's command text or its prompt and never
  converts a job's kind; remove + create expresses the conversion. List
  renders `command <line>` in place of `model <id>`.
- **schema 3** (`store/scheduler`): a nullable `jobs.command` column;
  the 2→3 step is an idempotent column add inside the existing
  migration, keyed on column presence the way the 1→2 fold keys on
  files, verified in test against a v2 store.
- **tool voice** (`tool/scheduler`): the schema and the guidelines
  describe the command payload and steer it toward deterministic
  scripts — pollers, digests, backups — never toward work needing
  judgment, which stays a model job's business.

## [1.2.12]: search asks in whole sentences

A debugging pass on the live SearXNG found one healthy engine serving
navigational junk for brand-heavy queries: a query led by a product
name returns that product's homepage, a query led by a single common
token returns a dictionary entry. The tool's guidelines said nothing
about query shape, so every session relearned the failure mode by
burning queries on it. The guidelines now name the shape to prefer,
route known URLs to web_fetch, and refuse identical retries —
prompt-facing guidance that rides in every session, including the
headless ones that never recall memory.

- **query-shaping guidelines** (`tool/web`): web_search's guidelines
  prefer natural-language multi-word queries, refuse leading
  brand/single-token shapes, send known URLs to web_fetch, and demand a
  reword rather than an identical retry on junk. A rig-over-pane
  divergence, like the announced trafilatura fallback; the schema and
  every runtime voice are untouched, and the golden_020 request pins
  are regenerated for the new description bytes.

## [1.2.11]: approve rides the door

A review pass found the approval gate skipped at the wiring for every
doorless frontend, so `approve: manual` in the rig home's settings.json
leaked onto the whole account — delegates, cron workers, `-p` runs —
where the status bar claimed manual while the tools mutated unasked.
The gate now wires unconditionally, and manual rides the door: a
frontend that cannot ask runs auto, so gating a TUI never binds the
fleet.

- **the gate always wires** (`cmd/rig`): `approve.Gate` is appended
  unconditionally and passes through in auto. The middleware seam is
  9 links (was 8).
- **manual applies where a door exists** (`cmd/rig`): a doorless
  frontend resolves approve to auto at the seam. The live `/approve`
  switch keeps its refusal on doorless frontends — a request that
  cannot be honored errors, while the persistent preference scopes
  itself to the frontends that can honor it.

## [1.2.10]: the jail's env lands in pairs

A review pass over the 1.2.x surface surfaced four fixes: the bwrap
jail's env never parsed, the delegate switched the process's own env
around each spawn, the provenance rule fell open on a symlink crossing,
and `-exec` opened the landlock path from any argv.

- **the jail's env rides explicit pairs** (`store/scheduler`): bwrap's
  `--setenv` takes VAR VALUE (two arguments) and the profile passed one
  `KEY=VALUE` argument, so every jailed spawn died in "bwrap: setenv
  failed" since 0.24.2. The argv now splits on the first `=`, an entry
  without one refuses, and the shape test pins the pairs the spec
  (SPEC_SANDBOX 1) already named.
- **the child env rides the spawn** (`store/scheduler`): the delegate
  and the runner switched the process's own `RIG_HOME`/`RIG_DELEGATE`
  around each spawn and restored after — shared mutable state a fan-out
  sibling could observe, and a race where a concurrent delegate read
  the first one's `RIG_DELEGATE` and refused as a worker. The child env
  is explicit at the spawn site now (`os.Environ()` plus the override);
  the process env never changes, and the jailed runner's `RIG_HOME`
  switch — dead weight, bwrap carries it via `--setenv` — is gone.
- **the provenance rule refuses the crossing** (`middleware/perm`): a
  plugins/ path whose symlink resolves outside the plugins root fell
  through to the tool, and an outside path resolving into the loaded
  tree fell through the same way. The zone is judged on both spellings;
  a pending dir relocated by the operator's own symlink stays the
  landing zone (the crossing is judged against the resolved pending
  dir).
- **the exec door opens only with the profile** (`cmd/rig`): `-exec`
  entered the landlock path from any argv; now only with `RIG_LANDLOCK`
  set, so a one-shot prompt of exactly `-exec` stays a prompt.

## [1.2.9]: the docs name the fan-out

The delegation feature never made it into the README or the landing page:
parallel subagent fan-out, mid-turn execution, the slots gate, and the
GPU busy rule were all undocumented, and the README's tool count and
config table were stale (it said 18 built-in tools unconditionally, and
omitted `workers.json`). The docs now say what the code does.

- **README**: the tool count names the fleet condition; `workers.json`
  joins the config table; a subagents section describes the delegate
  fan-out (parallel workers in one turn, the fleet's slots gate, the
  busy rule, no recursion, resumable transcripts).
- **docs/USAGE.md**: the delegate paragraph names the fan-out and the
  busy:skip refusal; the default allow count is conditioned on the fleet.
- **docs/SETUP.md**: the version examples move; the fleet's slots section
  says a fan-out queues beyond the gate.
- **site/index.html**: the landing page gains the subagent bullet and the
  conditional tool count.

## [1.2.8]: the wire prefix is pinned

The cache win (98-99% prefix hits across sessions and models) is the
product of a byte-stable request prefix: the system prompt, the tool
schemas, and the append-only transcript. Nothing guarded that property,
so a timestamp in the system assembly or per-turn tool trimming would
silently kill the cache. The stability is now pinned by tests.

- **the tools prefix is golden** (`cmd/rig`): the registered fleet's
  wire shape (name, description, schema) is pinned as a sha256; a schema
  or description change fails the test, and the golden moves with a
  deliberate commit — a cache-invalidating change is a reviewed event.
- **the wire is deterministic and append-only** (`provider/openai`):
  the same request marshaled twice is byte-identical, and a later
  turn's message array is the earlier one plus the appended tail; the
  two pins name the property the prefix cache is byte-keyed on.
- **the system assembly is byte-stable** (`cmd/rig`): building the
  system prompt twice yields identical bytes, so `time.Now` or a
  session id in the assembly fails loud.
- **tests**: `TestWireToolsPrefixGolden`,
  `TestWireMarshalingIsDeterministic`, `TestWireMessagesAreAppendOnly`,
  `TestSystemPromptIsByteStableAcrossBuilds`. The cache-ratio query
  already exists (`sessions summary`: `cache_read / prompt`, pinned by
  `TestSessionsSummaryCacheRatioFixture`), so the ratio is measured,
  not inferred.

## [1.2.7]: the refusal lands once

Eight refusals returned the same string as both content and error (the
retry guard's bound and round caps, the allow-list, the plugin
provenance rule's three voices, the python tool's two), and the loop's
fed-back error line appended that string again: the model saw each
refusal twice. The error line is now skipped when the content already
ends with it.

- **the fed-back error line is skipped when it is already there**
  (`loop`): a tool result whose exec failed keeps its content and gains
  the exec error on its own line unless the trimmed content already
  ends with it; a refusal that names itself as both content and error
  lands once, and a failing command that produced output still gains
  its error line. The fourth named reopening of the frozen loop;
  SPEC_CORE and the loop's PACKAGE.md carry the name.
- **the middleware chain's order is documented** (`middleware`): the
  parent PACKAGE.md records the root's `wire()` slice — `Wrap` applies
  in order, so the first-listed link is innermost and the last
  (`paths`) is outermost.
- **tests**: a scripted tool returning `(msg, errors.New(msg))` pins
  the transcript to `msg` exactly once; the malformed-call test's
  transcript now carries `synthetic failure` once, and the
  output-plus-error-line path is unchanged.

## [1.2.6]: the pending tail scrolls by rows, not columns

The capped pending prose line cut its visible tail at an exact column
count, so rows ended mid-word and every delta re-sliced the whole tail
at a new column — at streaming speed the tail jumped several times a
second instead of scrolling. The tail is now computed in rows.

- **the tail is the last wrapped rows** (`frontend/tui`): the pending
  line wraps at words on every frame (the committed path's wrap), the
  cap takes the last `cap-1` rows under the unchanged `· k lines
  hidden ·` marker, and `k` is the wrapped total minus the visible
  tail. A laid row never changes while it stays visible: only the
  last row grows with a delta and the block scrolls by exactly one
  row when a new row starts. `tailSegs` is gone; a row exactly width
  wide gets the same pending-wrap guard (the `toCol(1)` before the
  LF) the committed rows get.
- **the wrap never leaves a trailing space** (`frontend/tui`,
  `wrapSegs`): an emitted row dropped its trailing spaces when the
  text ended on one and kept them once the stream moved past — the
  same row changed while it scrolled. Rows now end at the last
  non-space; the committed path's rows lose only invisible trailing
  blanks.
- **tests**: a scripted stream of a 640+ char paragraph into a narrow
  pane asserts, between consecutive frames, that every visible row
  except the last is unchanged or moved up by exactly one row, that
  no row breaks inside a word, and that the hidden count plus the
  visible rows equals the wrapped total; a no-newline stream of a
  wide word pins the exact-width rows.

## [1.2.5]: the box the jail cannot run on

The operator's box cannot run the bwrap jail: the kernel's
`apparmor_restrict_unprivileged_userns` blocks the user namespace
bwrap needs and the operator has no sudo, so `sandbox: "jailed"` was
always refused there and `"off"` ran the cron workers unjailed. The
fix is a second containment profile the box can actually run: Landlock,
the kernel's unprivileged data-access LSM, no namespaces, no user
namespace, no privileges. One boundary, same guarantees where Landlock
can carry them, each residual named.

- **the third sandbox value** (`config`): `"jailed" | "landlock" |
  "off"`; the refusal voice names the vocabulary.
- **the landlock profile** (`store/scheduler`): `landlock.go` plus the
  two build-tagged syscall files (raw syscalls, no `x/sys/unix`,
  linux/amd64 and linux/arm64); the runner probes the kernel (ABI 4+),
  refuses loud and records the skip when the profile cannot run, and
  spawns `rig -p` with the env scrubbed to the named list and
  `RIG_LANDLOCK=<spec json>` carrying the grants: the cwd rw, the
  scratch home, the kernel dir ro, the rig binary ro+exec, the system
  dirs ro, `/proc` read, the device nodes rw, `sandboxBinds` with the
  same rw/ro semantics. Netless: bind+connect TCP handled and granted
  nowhere; the model call rides the one socket.
- **the domain arrives with the image, the only sound point**
  (`cmd/rig`, `tool/execwrap`, `tool/bash`, `tool/python`):
  `landlock_restrict_self` commits per-thread creds and Go's runtime
  has threads before `main()`, so an in-process restrict leaves the
  worker's own goroutines outside the wall — proven: a worker's
  in-process `read` tool returned the operator's `~/.bashrc` under the
  runner's exact env. The runner spawns `rig -exec rig -p ...`: the
  `-exec` branch restricts on a locked thread and execs, so the new
  image and every thread it creates inherit the domain; the worker
  never applies the profile in-process, and `RIG_EXEC_WRAPPER` lets
  bash/python wrap their subprocess execs through the same helper.
- **fail closed, both ends** (`cmd/rig`): the worker restricts at
  startup, before config and before any tool; a malformed spec, an
  unknown spec version, a missing grant, a probe failure, or an
  old ABI refuses loud with a voice naming the alternative.

## [1.2.4]: the failure's diagnosis is canonical too

The containment rule was already canonical; the failure path's message
decision was not. Under a symlinked session cwd, a file or missing path
that was genuinely inside (in either symlink form) was reported as
"outside the session's cwd" instead of naming the real rule. No access
changed — every path in that branch refuses either way — but the error
lied about why, and a wrong diagnosis is a wrong error.

- **the failure branch knows both forms** (`pathguard`): `Within`'s
  refusal now checks the raw and resolved spellings of both the roots and
  the failed path before choosing the containment message; a path inside
  the roots in any form keeps its specific error (`not a directory`,
  `no such file`), and only a path outside both roots in every form names
  the containment rule.

## [1.2.3]: the review pass's stragglers

A second pass over the same seams landed five follow-ups, each with a
failing test first. The one with teeth was the cwd gate: its lexical
precheck refused a legitimate working directory whenever the session
cwd and the passed path disagreed about symlink form.

- **the cwd gate is canonical only** (`pathguard`): `Within`'s literal
  precheck duplicated the canonical containment and over-refused — a
  resolved path under a symlinked session cwd (and the reverse) was
  refused although the canonical check accepts it. The canonical check
  is the one containment rule; both forms now pass and the symlink
  escape still refuses.
- **the fetch tool introduces itself as rig** (`tool/web`): the
  User-Agent said `pi-web-fetch/1.0`.
- **the bound's concurrent doubles are named** (`middleware/guard`):
  identical calls inside one concurrent run all execute (each passed
  the check before any had failed) and the next identical call refuses;
  the documented consequence now has its test.
- **the busy match reads the driver's typed error** (`store/sqlx`):
  `isBusy` matches the `SQLITE_BUSY` code on the driver's `*sqlite.Error`
  first and the message second, so the retry loop cannot lose the busy
  signal to a message change.
- **the read cap ends on a rune boundary** (`tool/file`): a truncation
  landing inside a multibyte rune no longer ships invalid UTF-8; the
  straddling rune is dropped whole.

## [1.2.2]: the review pass's five findings

A fresh-eyes review pass over the security boundaries, the scheduler's
consistency story, and the memory store landed five findings, each with
a failing test first. One was a real authorization asymmetry — the
store's own `Forget` refused another project's memory while a
`supersedes` could reach the same row from anywhere — and one was a
latent wait: the trafilatura subprocess's 20 s cap never actually
bounded the wait while an orphaned grandchild held the output pipes.

- **supersession is scoped** (`store/rem`): `applySupersedes` refuses a
  target outside the caller's scope or global, naming the owning
  project, before any row is touched — the same rule `Forget` already
  enforced. A refused supersede rolls the whole write back, so the
  learn or reflect lands nowhere.
- **the list names orphan crontab lines** (`store/scheduler`): a tagged
  line with no job row — the crash window between the crontab write and
  the store commit — now lists under `orphans:` with the removal
  instruction instead of being invisible; the runner already refused to
  fire it.
- **the fetch guard's denylist is complete** (`tool/web`): TEST-NET
  (192.0.2/24, 198.51.100/24, 203.0.113/24) and the 6to4 anycast
  192.88.99/24 join the refused ranges, so a host resolving there is
  refused like any private address.
- **the update's version compare pads short segments** (`cmd/rig`):
  `1.0` equals `1.0.0` (the old compare called it older and refused the
  update), and a negative segment refuses as not-a-semver.
- **the extraction rides the fetch's total budget** (`tool/web`):
  `ExtractReadable` takes the fetch's ctx, trafilatura's 20 s cap is the
  floor under the caller's deadline, and the subprocess has a
  one-second `WaitDelay` — so a slow extractor cannot outlive the
  requested timeout, and a killed trafilatura's orphaned children
  cannot hold the pipes past it.
- **the viewport fixture's size writes are synchronized**
  (`frontend/tui`): the mutable size behind the resize tests was read by
  the frame-ticker goroutine while the test wrote it — a data race the
  CI runner's `-p 2` scheduling finally exposed. The fixture is now a
  mutex-guarded pair (`get`/`set`), and every resize test publishes its
  new geometry through `set`.

The version is 1.2.2; the changelog, the SPEC_WEB guard list and
extraction budget, the SPEC_STATE supersede and orphan lines, and the
setup examples move with the code.

## [1.2.1]: the winch follows the terminal

One field report: a resize with nothing streaming never repainted.
The winch guard keyed on the input's nonzero fd — `fdi != 0` — while
stdin is fd 0, so a rig whose input was the terminal installed no
SIGWINCH handler at all: only the pty tests (fd != 0) got one. The
repro: a 44-row pane, one character typed (the caret parks on the
input row), the pane shrunk to 12. tmux deletes the rows below the
parked caret — the status rows — and with no handler nothing
repaints them; they stay missing through the regrow until the next
delta or submit. Verified live: an idle rig writes 0 bytes on
`kill -WINCH` and on a tmux resize, 66 bytes on a keystroke; the
process's SigCgt mask lacks SIGWINCH.

- **the winch handler follows the terminal** (`frontend/tui`): the
  input's terminal-ness is its own flag (`tty`), set where the input
  is found to be a terminal, and the handler installs whenever the
  input is a terminal — fd 0 included. `winchLoop` is unchanged: its
  full draw already repaints from the parked aim.
- **the idle repaint is pinned** (`frontend/tui`): a real-pty test
  where rig is idle — no delta, no keystroke — shrinks the pane and
  asserts the status block repaints; the parked repro types one
  character, shrinks, and asserts the status rows the shrink deleted
  return after the winch and after the regrow, the transcript intact;
  and the stdin shape itself is pinned — a pty on fd 0 owns the
  handler. The existing winch tests stay green.

The version is 1.2.1; the changelog, the SPEC_TUI signal-ownership
amendment, and the TUI docs move with the code.

## [1.2.0]: the delegate fans out

The turn's batch admitted `delegate` as a concurrent native, and the
per-session slot gate stopped refusing when the fleet's slots were
full. Fan-out is N delegate calls in one turn: the calls run
concurrently up to `workers.json`'s `slots` (default 1), and a call
that finds every slot held waits on a short poll for one to free
instead of failing; the refusal is the standing voice only when the
call's context ends, with the wait time named. The worker is jailed
and cwd-guarded, cannot recurse (`RIG_DELEGATE`), and its output
returns as a tool result like a read, so running it in parallel is
non-mutating from the brain's side; the approval gate still counts it
as mutating (it spawns a worker and writes stores).

- **`delegate` is concurrent** (`cmd/rig`): the loop's concurrent
  native set grows by `delegate`, so several delegate calls in one
  turn run as a fan-out instead of one-after-another barriers.
- **the slots gate waits instead of refusing** (`store/scheduler`):
  the acquisition retries on a short interval while every slot is
  held, until a slot frees or the call's context ends; on the context
  ending the refusal keeps the standing voices — the one-slot
  `delegate: a delegation is already in flight (this session)`
  unchanged, the full-set `delegate: the session's delegate slots are
  full (slots N)` now naming the wait time.
- **the bound and the wait are documented** (SPEC_DELEGATE 6, the
  package docs): the fan-out replaces the one-in-flight/refusing
  voice; the tests pin the overlap (slots 3), the sequence (slots 1),
  and the slots-full wait (slots 2).

The version is 1.2.0; the changelog, SPEC_DELEGATE's bounds, and the
PACKAGE.md docs move with the code.

## [1.1.4]: the fed-back error line

One field report: a tool failure could reach the model as a bare
`(cwd /root)` line. When bash's child never ran — a bad or unreadable
cwd, Go's `fork/exec /usr/bin/bash: permission denied` — the output was
empty and the reply was just `(cwd /root)`: the loop substituted the
exec error only when the content was empty, and the model saw no error
at all. Alongside it, the system prompt said "the working directory"
without naming it, which is why the model guessed /root.

- **the fed-back error is always a line** (`loop`): a tool result whose
  exec failed keeps its content and appends the exec error on its own
  line, so the model sees the output and the reason; an empty result
  carries the error alone. The third named reopening of the frozen
  loop; the gate's clause and SPEC_CORE carry the name, and the
  re-freeze follows the merge. An unknown tool now returns the error
  only (the loop appends it), so the refusal is never shown twice.
- **bash refuses a dead cwd by name** (`tool/bash`): the cwd is stat'ed
  and checked before the child starts; a missing, non-directory, or
  unsearchable cwd fails with `bash: cwd X: <reason>` and no content,
  the loop feeds the error line into the result once, and the model
  never sees a bare `(cwd X)` or a fork/exec line naming /usr/bin/bash.
  The refusal is a real failure: the TUI shows the fail glyph and the
  retry guard counts it.
- **`~` is the session home, and the boundary keeps the shell's forms**
  (`middleware/paths`): a leading `~`, `~/…`, or `~user/…` in a
  path-shaped argument (`path`, `root`, `cwd`, `project`) expands at
  the path boundary, before any validation; a `~` anywhere else, an
  unknown user, or an unset home stand as given. The boundary already
  did this; the report that tilde expansion was missing was wrong.
- **the session names itself** (`cmd/rig`): the system prompt carries
  the session's working directory and home at session start, so the
  model never guesses where it is, and a leading `~` in a tool path
  expands to the home it was told about.

## [1.1.3]: the parked aim

One field report: the typed-key fast path repaints the input row in
place and parks the caret on it, `parked` rows above the region's
bottom. The repaint's first move re-anchored with a cursor-down back
to the bottom before aiming up at the region's top. tmux handles a
height shrink by deleting rows below the cursor first and only then
scrolling the top into history — the cursor stays put — so a shrink
that lands while parked deletes the rows under the caret, the
cursor-down is a no-op at the bottom, and the following cursor-up
overshoots by `parked`, writing the region over committed rows above
it. The repro: a 24-row pane, one character typed during a streaming
answer, the pane resized through 20/16/12/16/20/24 a few times at
~70ms per step — the first two committed rows of the answer were
overwritten every run. A second report: a 640-char paragraph with no
newline streams (region ~16 rows), one key typed (park 4), the pane
shrunk 24→12 — five wrapped rows of the paragraph stayed above the
committed copy and the first fifty words showed twice, because the
aim was capped at the shrunken viewport before the park was
subtracted.

- **the aim starts from the park** (`frontend/tui`): the repaint no
  longer re-anchors through a cursor-down. The cursor-up before a
  repaint is the region's uncapped row count minus one minus
  `parked`, and only the result is capped at the viewport — a pane
  that shrank under a region painted for a taller one must aim all
  the way to the region's top, not to the shrunken viewport's (an
  aim capped at the viewport first undershoots by `parked` and
  leaves the region's head above the repaint). The park clears after
  the paint. Every path that aimed from the bottom applies it — the
  region repaint, the submit, the winch re-layout, and the in-place
  input edit, whose cursor-up is measured from the parked row. The
  caret still rests on the input row after an in-place edit, and the
  protocol folds the old `ESC[nB ESC[mA` pair into one `ESC[(m-n)A`
  (the golden streams shrink by the pair). The viewport cases are
  named: paint a region, type a character (park), shrink the pane by
  the parked rows, and deliver a text delta — every committed row
  above the region survives and the region paints exactly once; the
  same through the stepped shrink-then-grow sequence; and a shrink
  that lands under a region taller than the target pane paints the
  pending paragraph exactly once across history and the screen.

## [1.1.2]: the page the frame shows

One field report: the pager stepped by logical lines while the frame
fills the view by visual rows, so a record holding lines wider than
the pane — tool results and code blocks commit unwrapped — showed
fewer lines than the step and the lines in the gap were never
displayed. The repro: 40 lines, lines 20-27 padded to 70 chars, a
52x24 pane with a five-row footer, paging up showed L26..L40, then
L10..L23, then L01..L06 — L24, L25, L07, L08, L09 never shown.
Alongside it, the markdown pass read `_` as an emphasis marker inside
plain words, so `cron_audit, gpu_stats` rendered as `cronaudit,
gpustats`.

- **the pager steps by the frame** (`frontend/tui`): PgUp advances the
  offset by the lines the current frame actually showed, minus one;
  PgDn walks forward from the frame's bottom, accumulating `rows()`
  against the same row budget, and steps by that many minus one. The
  offset stays in lines, the pages overlap by a row as they did, and
  every line is reachable: the repro's pages now cover all 40 lines,
  adjacent pages sharing exactly one.
- **underscores need a word boundary** (`frontend/tui`): an `_` opens
  or closes emphasis only when the neighboring character is not a
  letter or digit, the way CommonMark treats intraword underscores;
  snake_case identifiers render literal in prose and in list items.

## [1.1.1]: the painted aim

One field report: the tear was back on 1.1.0. Mid-stream, content
painted over the model info and the spacing between the content and
the input collapsed, until a keystroke relaid the region. The viewport
bound capped the region's height, but the repaint still aimed with the
geometry about to be painted instead of the geometry on screen — three
doors into that one root:

- **the aim is painted geometry** (`frontend/tui`): the live region
  remembers the row count and the width it was last painted at. Every
  cursor-up aims with the painted count, capped at the viewport (a
  paint that overflowed the pane scrolled its own head into history,
  so the painted span is what the next aim must clear), and the
  stability check refuses the in-place input-row edit while the
  painted width is stale — the keystroke takes the full re-layout,
  which aims correctly by construction. A resize re-lays the region
  once, from the true top, at the new width; the transcript survives
  whether or not the terminal ever delivers SIGWINCH, because the size
  is read at the repaint.
- **the viewport budget counts the rows it paints**: the status block
  contributed its logical rows (split on newline) while the paint
  renders their wrapped rows, so a narrow pane let the region run a
  row taller than the screen and every streaming frame scrolled and
  clamped. The budget now counts the block's wrapped rows beside it.
- **a submit repaints the whole live block**: the echo aimed from the
  input row's top, which let the verb menu's rows survive the submit
  as committed-looking text. The submit now aims below the separator
  blank (which survives) and repaints the menu's rows away; a submit
  on a live turn is a steer, and the activity row the turn still owns
  carries into the new region instead of lingering stale above the
  echo.
- **committed bytes expand tabs on the paint seam** (`frontend/tui`):
  a tab advances to the next eight-column stop while the width math
  counts it as nothing, so a tool result carrying tabs — any Go or
  YAML source — rendered wider than `visualRows` saw, and every row
  after the first tab drifted down the frame, baking fragments of the
  status block and of neighbouring rows into the committed block. The
  flow path already expanded tabs; the seam now covers everything the
  region paints, SGR sequences copying through at zero width.
- **the aim caps at the viewport**: the phone's virtual keyboard is a
  height-only resize, and the first repaint after the shrink aimed
  with the pre-shrink painted span, overshooting the shorter screen
  and leaning on the terminal's clamp. The size is read at the
  repaint; the aim now holds the painted span inside whatever the
  pane currently is.

## [1.1.0]: the viewport bound

Three field reports, one root: the live region had no notion of the
viewport's height. A streamed paragraph taller than the pane repainted
with the cursor-up clamped at the screen's top, writing the region over
committed history and leaving rows the bookkeeping could never clear —
the phone's keyboard-open overlap, the lost spacing between the content
and the input, the model info block gone or too far below the input,
and the tearing under long thinking blocks (every 16 ms frame scrolling
duplicate prose into history). Alongside it, the typing fast path was
dead in production: the region-stability check counted the status
block as one row when it is four.

- **the live region is bounded by the viewport** (`frontend/tui`):
  the height is read at every repaint beside the width; the pending
  prose line renders its last rows under the dim `· k lines hidden ·`
  marker (the line still commits whole to scrollback when it closes),
  the menu's candidate window shrinks under the same budget before its
  `… N more` tail and hint row, and the input's five-row window
  shrinks last. The shrink order keeps the operator's controls alive
  as the room runs out; a pane shorter than its own controls renders
  whole and degrades, named.
- **the typing fast path is restored** (`frontend/tui`): the
  region-stability check counts the status block's real rows, so a
  keystroke on a stable region rewrites the input row alone — the
  golden streams shrink by forty rows of re-laid region per keystroke.
- **the escape-capture harness models the viewport** (`frontend/tui`
  tests): height, scroll, and the cursor clamp a terminal applies at
  the margins; the viewport cases are named in SPEC_TUI's testing
  section (the streamed-tail case, the shrunken windows, the
  no-clear-below keystroke, the no-cursor-down frame after a commit).

## [1.0.0]: the tag

The gate the roadmap set before the tag is met: lived use — a worker
soak and the TUI field-tested as the daily driver — with the receipts
in the session store (63 sessions, 724M prompt tokens, 99.08% served
from the KV cache). The freeze has held since 0.3.0, core/ and loop/
open to extension and closed to modification; this tag says the
interface is stable.

- **the version is 1.0.0** (`cmd/rig`): the const, its freeze test
  (the version must now be semver `x.y.z`, not `0.x.y until 1.0`), and
  the setup docs' `--version` examples agree.
- **the example configuration** (`docs/SETUP.md`): the configure
  section gains a `settings.json`, a `models.json`, and a `workers.json`,
  each annotated — the fastest start from a blank rig home, with the
  allow-list's fleet rule and the `/effort` vocabulary named.
- **the roadmap's gate is marked met** (ROADMAP.md): the v1.0.0 line no
  longer waits.

## [0.25.9]: the pre-1.0 polish

The review's stragglers before the 1.0 tag: a constructor that could
panic, a server that never closed idle connections, a Go requirement the
docs understated. The project's front doors land too: how to report a
vulnerability, how to contribute, and the issue forms.

- **the theme is the constructor's third argument** (`frontend/tui`,
  `cmd/rig`): `tui.New` takes the resolved theme and no longer resolves a
  fallback that could panic; the composition root stays the one place
  that reads the theme file. The constructor's contract is now total.
- **the dashboard closes idle connections** (`frontend/web`): the serve
  server sets `WriteTimeout` and `IdleTimeout` beside `ReadHeaderTimeout`,
  so a slow or abandoned client cannot pin a loopback connection forever.
- **the Go requirement is exact** (README, docs/SETUP): `go.mod` requires
  Go 1.26.6; the docs say ≥ 1.26.6, not ≥ 1.26.
- **the front doors** (SECURITY.md, CONTRIBUTING.md, the issue forms): the
  trust model and the reporting path, the spec-first process and the
  house rules, and the two issue templates that ask for the spec. The
  freeze gate's allowlist gains the two root docs.

## [0.25.8]: the tear harness stops replaying the last unit

The flake that CI kept tripping on `TestTearNoSyncPromptNeverBlanks`:
the terminal model kept the last complete unit of every feed call in
its buffer and replayed it on the next call, at a cursor position that
had already advanced. A chunk boundary landing right after a rune or a
CSI shifted and duplicated the following content until the prompt row
was overwritten ("the prompt vanished at byte 9666"). The model now
keeps a tail only when a call ends mid-unit, so the screen no longer
depends on how the stream is split.

- **the model keeps only an incomplete tail** (`frontend/tui`, tear
  tests): `vtStream.feed` tracks whether the call stopped inside a rune
  or CSI; only then does it retain `buf[rest:]`. A call that ends on a
  complete unit leaves the buffer empty, and the same stream replayed
  in 9-byte chunks and in whole frames produces the same screen. The
  prompt stays on screen at every read boundary.

## [0.25.7]: the live frame never erases before it writes

The tear the operator kept seeing through tmux: the sync pair was a
no-op there. tmux 3.4 consumes `?2026h`/`?2026l` without forwarding
them (the pane-side support landed in 3.7), so every repaint was an
unsynchronized erase-then-rewrite, and a write split at the pty buffer
(a large commit, fast text) showed the region erased and blank — the
input line flickered and lost its content. The frame now writes over
the old region.

- **the frame writes first, erases after** (`frontend/tui`, PACKAGE.md):
  a repaint no longer clears the region before writing it. Each row's
  replacement lands, then the tail is erased (`CSI K`); the shrink below
  the last row is erased with `0J` after the content. A reader that
  splits the frame at the pty buffer sees the old frame, then the new
  one in place — never a blank region. The input row's prompt stays on
  screen at every read boundary. `TestTearNoSyncPromptNeverBlanks` feeds
  the real stream through a terminal model that swallows the sync pair
  and splits writes at 9 bytes, asserting the prompt is never gone;
  `TestTearNoSyncPairIsOneWrite` pins the pair and the frame as one
  write.
- **the sync pair closes the frame's write** (`frontend/tui/live.go`):
  `?2026h`+frame+`?2026l` are one `write`, so the end marker cannot be
  separated from the frame it closes. Terminals that support the mode
  (kitty, ghostty, wezterm, foot, iTerm2, Windows Terminal, tmux 3.7+)
  still paint the pair once; the rest ignore it.
- **the support table says tmux 3.7** (PACKAGE.md): the claim that tmux
  3.4 buffers the pair was wrong — 3.4 consumes it. Under 3.4 the
  write-first protocol is the whole protection; the pair is inert.

## [0.25.6]: the stream's idle bound, the model's no default, the seam's dead weight

The pre-v1 review's three findings: the one unbounded wait left in the
loop (a server that holds the stream open without events hangs the turn
forever), the persistence seam's unused postgres adapter, and the
embedded `local` model default (a fresh install now refuses to start
without a model instead of guessing one).

- **the stream's idle bound** (`provider/openai`, SPEC_HARDENING 11):
  the provider already bounds the wait for response headers (5 minutes);
  the body read had none. A stream with no line for 10 minutes — the
  constant beside the header wait, reset by every line (a delta, a
  comment, a keep-alive) — is closed and Faults, naming the bound and
  the silence; a slow stream that keeps sending data never faults. This
  is not the wall-clock cap decision 9 rejected: only total silence
  faults, not total time. No loop change: the Fault takes the ordinary
  mid-stream path. `TestAStallingStreamFaultsAfterTheIdleBound` drives
  the stall (one chunk, then the connection held open: exactly one
  Fault naming the idle bound), and `TestIdleBoundResetsOnEveryEvent`
  pins the reset (chunks every 30 ms against a 150 ms bound: the stream
  completes with `Done`).
- **the busy refusal names the wait** (`store/sqlx`): the seam's
  busy-wait contract now has a named case —
  `TestTxBusyRefusalNamesTheWait` holds the write lock past a caller
  deadline: the refusal names the busy wait and the deadline, not a
  bare driver error — and the wait-out test trades its `time.Sleep` for
  the holder/contender channel handshake (no sleeps, no vacuous pass).
- **the unused adapter is cut** (`store/sqlx`): `ArrayScanner`, the
  postgres array-literal and JSON parsing that existed for it, is
  called by nothing in the tree, and the seam's PACKAGE.md told a
  postgres story (pgx/v5/stdlib) with no postgres driver in go.mod.
  Both are gone; the seam is one constructor, one isolation, one read
  of the context.
- **the model default is gone** (`config`, `cmd/rig`): the embedded
  settings.json no longer carries `model: local`, and a run that
  resolves no model refuses at start — `--model`, `RIG_MODEL`, or the
  `model` key in settings.json — before any store opens or request is
  made (`TestNoModelRefusesBeforeAnyRequest` counts zero requests). The
  embedded models.json keeps the `local` row: a model table is not a
  default, and `model: local` still works when the operator names it.
  SPEC_CONFIG 5, the SETUP knob table, and the README's first-run
  paragraph say so.

## [0.25.5]: the truncated call comes back in-band

The one fault class the soak has recorded: a stream cut off by the
output token limit mid-tool-call used to Fault, killing the turn and
discarding the streamed reasoning and the partial call. The model got no
feedback, so the operator had to steer with the fault text pasted into
the next prompt (2026-09-09, the 36-minute stream that ended in a cut
`bash` call). The cut call now comes back in-band.

- **the cut call is marked, not faulted** (`core`, `provider/openai`):
  `ToolCall` gains `Cut`, the finish reason that cut a call's arguments
  mid-JSON (empty when the call is complete). The adapter emits every
  accumulated call, and a call whose args are invalid at stream end
  carries the marker and is followed by `Done`, never a `Fault`; a
  complete sibling in the same stream still executes.
  `TestLengthFinishedTruncatedToolCallArgsMarked` replaces
  `TestLengthFinishedTruncatedToolCallArgsFault` (which required the
  fault, and was wrong), and `TestStopFinishedMalformedToolCallArgsMarked`
  and `TestLengthCutCallKeepsCompleteSiblings` pin the two edges. The old
  fault text's advice (`raise MaxTokens or the reserve`) was misleading:
  `Reserve` is the context-compaction threshold, not an output budget.
  The allocation, not the size, was the fault.
- **the cutoff link refuses before the tool** (`middleware/cutoff`, new):
  the root's chain executes the link after the allowlist and before the
  approve gate, and a marked call never reaches the tool: the partial
  args are data, not intent, and a truncated command is exactly the
  thing never to run. The refusal teaches: a length cut says to re-issue
  more tersely or split the call; any other finish reason says the
  arguments were not valid JSON. `TestMarkedCallRefusesWithoutExecuting`,
  `TestUnmarkedCallExecutes`, `TestMalformedMarkedCallTeaches`.
- **the model recovers on the next turn** (`loop`, unchanged): the
  refusal feeds back through the ordinary tool-result path, so the
  transcript keeps the reasoning, the partial call, and the refusal, and
  the model re-issues with full context; no `Fault` row is minted, so
  the soak's fault count does not grow for this class. The refusal is a
  failure, so the guard's bound still counts it and `Rounds` bounds the
  turn. `TestTruncatedCallRecoversInBand` drives the whole path.

## [0.25.4]: the release workflow stops trusting apt

The v0.25.3 release died in the minisign step: `apt-get update` broke on
the Chrome repo's stale index (`Hash Sum mismatch` on `dl.google.com`),
so the binary never arrived. The workflow no longer touches apt at all.

- **minisign comes pinned, not from apt** (`.github/workflows/release.yml`):
  the sign step ran `apt-get update`, which refreshes every runner source
  including the Chrome repo whose flaky index fails intermittently, and
  `apt-get install minisign` after it. The workflow now downloads the
  project's static `minisign-0.12-linux.tar.gz` release, verifies its
  pinned sha256 (`9a599b48ba6eb7b1e80f12f36b94ceca7c00b7a5173c95c3efc88d9822957e73`,
  itself checked against the project's official signature), and puts the
  binary on `PATH`. No apt, no sudo, no `dl.google.com`.

## [0.25.3]: the one cwd rule and the fire-time bind check

The deep review pass found the jail fix's one remaining window and the
cwd rule's one duplication. Each carries a test that failed before and
passes after.

- **the job's cwd is revalidated at fire time** (`store/scheduler`): the
  0.25.2 check validated the cwd at create and update, but the jail
  rw-binds the stored path at fire time, and the model can rewrite the
  session's project between the two: create a real directory, schedule a
  once job against it, then rename the directory and leave a symlink to
  `/etc` (or the operator's home) in its place. The bind follows the
  symlink, so the jailed worker gets rw access to the host again. The
  runner now canonicalizes the stored cwd at fire time and refuses when
  the path no longer resolves to itself: a replaced, moved, or deleted
  cwd skips the fire with a recorded reason and a teaching message, and
  the crontab line stays for the list to flag.
  `TestRunJobRefusesAReplacedOrMissingCwd` replaces and deletes the job's
  cwd and requires the skip, not a spawn.
- **the cwd rule is one function, not two** (`pathguard`): the scheduler
  tool carried its own copy of the delegate tool's `canonicalCwd`, and
  the copies had already drifted: the delegate's accepted a file as a
  cwd (it failed at spawn), the scheduler's refused one at the boundary.
  The rule now lives once in `pathguard` (`Canonical` and `Within`), both
  tools call it, and the delegate inherits the directory check.
  `TestDelegateCwdFileRefuses` points a file at the delegate and requires
  the refusal, not a spawn; `pathguard`'s own tests pin canonicalization
  and containment by name.
- **the store waits out a busy write lock** (`store/sqlx`): the
  transaction seam retried nothing on `SQLITE_BUSY`, so a concurrent
  burst that outran the driver's five-second busy timeout (eighty queued
  writers on a slow CI disk) failed creates with a raw "database is
  locked" instead of serializing. `beginTx` now retries the begin while
  the caller's context lives, bounded at thirty seconds with a doubling
  backoff, and the final refusal names the wait.
  `TestTxWaitsOutTheWriteLock` holds the write lock, waits out the
  release, and requires the transaction to succeed.

## [0.25.2]: the jail escape and the missing brakes

The deep review pass found one sandbox escape and four missing brakes. Each
carries a test that failed before and passes after.

- **the scheduler jail could be scoped to the host** (`tool/scheduler`): a
  job's `cwd` was taken verbatim, and the jail rw-binds it
  (`--bind p.Cwd p.Cwd` in `store/scheduler/jail.go`), so a model could
  create a job with `cwd: "/home/<operator>"` (or `/etc`, or `/`) and the
  jailed worker would have rw access to it. The tool now validates the
  cwd the way the delegate tool already does: lexical and canonical
  (symlinks resolved), and only under the session's cwd or the rig home.
  `TestCreateRefusesACwdOutsideTheSessionRoot` creates a job with
  `cwd: "/etc"` and requires the refusal, not a job row.
- **bash output was capped after the fact** (`tool/bash`): the whole output
  was buffered (`bytes.Buffer`) and truncated only after the process
  exited, so `cat` of a huge file pinned it in RAM. The bounded writer now
  keeps only the head at the cap, drops the rest, and always consumes the
  child's writes (the child never blocks); the kept bytes are
  byte-identical to the old truncation.
  `TestBoundedWriterKeepsTheHeadAndNeverBlocksTheChild` writes three times
  the cap and requires the kept output to be the head plus the marker, and
  `TestHugeOutputIsBoundedWithTheMarker` pins the exec's reply
  byte-for-byte.
- **model-supplied timeouts were unbounded** (`tool/web`, `tool/python`):
  `timeoutMs` was taken as-is; the schemas declared `minimum: 1000` and
  nothing enforced it, so a value of 1 ran anyway, a huge value could hang
  a turn for days, and an overflow wrapped negative. Both tools now refuse
  outside `1000..300000` (web_fetch) and `1000..600000` (python) naming
  the range, and the schemas carry the `maximum`. The web case
  (`TestOutOfRangeTimeoutMsRefuses`) and the python case
  (`TestOutOfRangeTimeoutMsRefuses`) pass a table of hostile values and
  require the refusal, not a run. Named change: the E2E timeout case now
  uses the new minimum (`timeoutMs: 1000`) so it stays a timeout instead
  of hitting the boundary refusal, and the wire goldens carry the new
  `maximum` fields.
- **rem content was unbounded** (`store/rem`): `learn` and `reflect` stored
  any size content, so a model could bloat the store and the fuzzy arm's
  trigram table. Both now refuse above 64 KiB naming the cap.
  `TestLearnRefusesOversizedContent` learns just over the cap and requires
  the refusal, not a memory row.

## [0.25.1]: the panic that could kill the harness

The v1.0.0 review pass found two reachable panics and one missing brake.
Each carries a test that failed before and passes after.

- **web_search and web_fetch refuse out-of-range args instead of
  panicking** (`tool/web`): a model-supplied `maxResults` of 0, a
  negative, or a huge integer made `make([]result, 0, n)` panic with
  `makeslice: cap out of range`; a `maxChars` below the schema minimum
  made `CapChars` slice past the string head. The schemas declared the
  bounds but nothing enforced them — the model reads the schema as a
  contract, and the harness must fail loud, not crash. Both tools now
  refuse out-of-range at the boundary, naming the value and the allowed
  range. The tests (`TestOutOfRangeMaxResultsRefusesInsteadOfPanicking`,
  `TestOutOfRangeMaxCharsRefusesInsteadOfPanicking`) pass a table of
  hostile args and require a refusal, not a run.
- **a panicking tool is a tool error, not a process crash** (`loop`,
  named in SPEC_CORE): the batch's `run` executes a tool in a goroutine
  with no recover, so any panic — a tool bug, a hostile arg slipping
  past a schema — took the whole harness down. `run` now recovers and
  surfaces the panic as that call's tool error: the model sees the
  panic text, the transcript survives, the process survives.
  `TestBatchSurfacesAPanickingToolAsAToolError` runs a tool that
  panics and requires the error to land as a `ToolResult`. This is the
  second named reopening of the frozen loop (the first was SPEC_EVT 2a);
  the gate's clause and SPEC_CORE carry the name, and the re-freeze PR
  after the merge deletes the clause.
- **ROADMAP's pointer lands**: "The queue's next lives in the CHANGELOG's
  `[Unreleased]`" pointed at a section that did not exist. The line now
  points at the CHANGELOG's top.

## [0.25.0]: the clean release

v0.24.6 was tagged from the release branch before it was merged, so the
tag and the GitHub Release pointed at the unmerged head. The tree was
identical to main, but the release was not the merge commit. 0.25.0
ships that same tree from the merged main with a clean tag: the last
pre-1.0 release starts clean.

## [0.24.6]: the pre-v1 sweep

Three findings from the v1.0.0 review pass. Each carries a test that
failed before and passes after.

- **read never materialises the file** (`tool/file`): a read of a file
  larger than the 1 MiB cap read the whole file into memory before
  truncating — a model naming a multi-gigabyte file could pin the box
  (the field measurement: 806 MB allocated to read a 64 MB file). The
  read now streams the file once, hashing every byte for provenance and
  capturing only the requested line window, capped at one byte past the
  output cap so the exact cut point is known; the returned bytes are
  byte-identical to the old split-join contract (the trailing-newline
  line count, the truncation marker, the window), and the drift-diff
  cache remembers the window, not the file.
- **the fetch's DNS rides the request context** (`tool/web`): the host
  resolution used `context.Background()`, so a stalled resolver could
  outlive the fetch's own timeout and return a success past the
  deadline. `LookupFn` now carries the ctx (the seam's shape changed,
  named in SPEC_WEB), the default resolver uses it, and a cancelled
  lookup surfaces the context error instead of a generic resolution
  failure.
- **the winch signal stops at Close** (`frontend/tui`): `signalWinch`
  registered SIGWINCH and never stopped it — a goroutine and a signal
  handler leaked past `Close`. The handler now owns a stop that is
  idempotent and synchronous (the goroutine exits before it returns),
  and `Close` calls it.

## [0.24.5]: the dead session's claim is released

The queue's one unpaid debt: a task claimed by a session that died
stayed `in_progress` forever, and every live session reading the queue
had to fail-retry-start it by hand (the operator's cleanup of the
stale queue was the field report). Each finding below carries a test
that failed before and passes after.

- **the dead session's claim is released** (`store/todo`,
  `tool/todo`, `command/tools.go`, `cmd/rig`, `specs/SPEC_STATE.md`):
  the todo store gains a `release` event op and two doors. `Release`
  (the tool and `/todo release <id>`) returns a claimed task to
  `pending`, clearing the owner, and refuses the caller's own claim,
  an unclaimed task, a finished task, and a foreign claim younger than
  `StaleClaimAfter` (24h) — a live session's work is never stolen by a
  tool call. `Reap` (wired at session open in `cmd/rig`) frees every
  foreign claim whose owner's session row has ended (the exact arm:
  `state.ListSessions`, `Exit != "open"`) and every claim whose
  owner's last event on the task is older than the staleness window
  (the SIGKILL arm: a hard-killed session leaves its row `open`, and
  only age proves it). The reaper never touches the caller's own
  claims; the note names each task and the owner it was freed from,
  printed to stderr at open, silent when idle. Tasks survive the
  release — text, deps, and position stay; only the claim dies. The
  compact snapshot now carries each task's `updatedTs` (the claim
  clock), so a compaction folds the log without resetting a stale
  claim's age.
- **the todo schema rides the wire** (`tool/todo`): the `release`
  action joins the schema enum, and the wire goldens were regenerated
  (`cmd/rig/testdata/golden_020`), which is why this entry's diff
  carries the three `.json` fixture changes.



Each finding below carries a test that failed before and passes after.
Field-tested by the harness's own occupant: the session that found the
label bug was reading its own session row.

- **the served model rides the message row** (`core/provider.go`,
  `provider/openai/openai.go`, `store/state`, `specs/SPEC_CORE.md`,
  `specs/SPEC_STATE.md`): the session store recorded the requested model
  id once, at open, and never learned what actually served each turn — a
  `/models` switch or a backend swap behind the endpoint left the row
  lying about who was home. `core.Done` now carries `Model`, the
  response's own echo, and the recorder stamps it on assistant message
  rows (schema v3: `messages.model`, nullable); user, compaction, and
  re-landed rows stay null. `sessions.model` stays the requested id at
  open — the divergence between the two is the diagnostic. The freeze
  gate learned the distinction it was missing: the frozen surface is open
  to pure addition (every old line survives, in order) and closed to
  modification, so extension no longer reads as a violation.
- **the session names itself and counts its tokens** (`store/state`,
  `command/sessions.go`, `tool/sessions`, `frontend/web`): the listing
  was a wall of anonymous ids. `sessions.label` (schema v3) carries the
  first user prompt's first line, trimmed, at 60 runes — written once by
  the recorder, first writer wins, never rewritten; `ListSessions`
  returns it beside the summed prompt+completion tokens. The command,
  the tool, and the dashboard render both; absent labels render nothing.
- **the busy refusal teaches the escape hatch** (`store/scheduler/delegate.go`):
  on a single-endpoint box a delegate from inside a turn cannot win —
  the worker's model would have to evict the delegator's own residency.
  The refusal already named the holder; it now names the path that does
  work (schedule a once-job, it fires between turns).
- **the regeneration is documented** (`store/state/PACKAGE.md`): the
  exact lift invocation for regenerating `domain`/`ddl` after a metadata
  edit, which lived in nobody's head and nowhere on disk.

## [0.24.3]: the release's signature proves itself

Each finding below carries a test that failed before and passes after.

- **the release's signature proves itself** (`cmd/rig/update.go`,
  `specs/SPEC_BUILD.md` 5): the verifier from 0.24.2 was written against
  an invented wire format — a 40-byte public key and a 72-byte signature
  over the raw file. Real minisign 0.11 emits a 2-byte algorithm tag +
  8-byte key id + 32-byte ed25519 key (42 bytes) and, in its default
  mode, a 2-byte "ED" tag + 8-byte key id + 64-byte signature over the
  BLAKE2b-512 digest of the file; the first signed release would have
  been refused by its own verifier even after `MINISIGN_SECRET_KEY` was
  configured. `verifyMinisign` now parses the real format, accepts both
  "Ed" (legacy, raw) and "ED" (hashed) signatures, and refuses the
  invented short format by name; the suite pins golden fixtures produced
  by the actual minisign binary.
- **the release key is pinned in the build** (`config/settings.json`,
  `config/PACKAGE.md`, `docs/SETUP.md`): the operator's minisign public
  key is now the embedded `updateKey` default, so every built rig knows
  which key signs the releases and `-update` verifies out of the box
  (`RIG_UPDATE_KEY` > settings.json `updateKey` > the embedded pinned
  key); env and file still override, and a build without a pinned key
  still refuses loud. The release workflow's `MINISIGN_SECRET_KEY`
  secret holds the key's base64; a missing secret still refuses the
  release before any asset ships.

## [0.24.2]: the update proves itself and the jail starts empty

Each finding below carries a test that failed before and passes after.

- **the update proves itself** (`cmd/rig`, `specs/SPEC_BUILD.md` 5): the
  `-update` path verified the downloaded asset against `checksums.txt`
  from the same release, corruption protection with no authenticity.
  The asset and its `.minisig` are now fetched and the ed25519
  signature is verified against the pinned minisign key
  (`RIG_UPDATE_KEY` > settings.json `updateKey`); a missing key, an
  unsigned release, and a bad signature each refuse loud before the old
  binary is touched, and the checksum lookup matches the exact asset
  field (a name that shares the asset's prefix no longer steals the
  line). The release workflow signs every asset with the
  `MINISIGN_SECRET_KEY` secret.
- **the jail starts empty** (`store/scheduler`, `specs/SPEC_SANDBOX.md`
  1): the bwrap profile passed the operator's whole environment to a
  jailed worker, exported secrets included. The profile now clears the
  environment and names the whole list (`PATH`, `HOME`, `RIG_HOME`;
  `RIG_DELEGATE=1` for a delegate worker) in explicit `--setenv` pairs.
- **the chain's order is written down** (`docs/DESIGN.md`): the
  middleware paragraph described the wrapper stack from the tool
  outward, and "the listing order reads as the execution order" was the
  reverse of the code. The paragraph now prints the execution order
  explicitly: paths -> cap -> rounds -> bound -> allowlist -> plugins
  -> approve -> resolve -> the tool.

## [0.24.1]: the turn's end always leaves one blank row before the prompt

Each finding below carries a test that failed before and passes after.

- **the turn's end always leaves one blank row before the prompt**
  (`frontend/tui`, PACKAGE.md): a reply that ended without a trailing
  newline drew the input line directly beneath the last row. The live
  region supplies its blank margin from `lastBlank`, which was still
  stale-true when the final commit's lines were computed, so the margin
  never appeared. `TurnEnd` now commits one blank row after the pending
  text drains when the last committed row is not blank, and `live.draw`'s
  blank merge keeps a double out when the reply already ended with a
  newline. `TestSpacingRule` pins all three endings (trailing newline,
  trailing blank line, no trailing newline) to exactly one blank row
  before the prompt.

## [0.24.0]: the live region paints once per frame

Each finding below carries a test that failed before and passes after.

- **the live frame is wrapped in the synchronized-output mode**
  (`frontend/tui`, PACKAGE.md): every flushed repaint is written between
  `?2026h` and `?2026l`, so a supporting terminal buffers the pair and
  paints once — the tearing is gone outright. tmux 3.4, kitty, ghostty,
  wezterm, foot, iTerm2, and Windows Terminal support the mode; the rest
  ignore it. `TestLiveSyncFrames` pins the pairing.
- **the live region repaints on a 16 ms frame cadence**
  (`frontend/tui`, PACKAGE.md): `flow` no longer paints per delta — it
  marks the region dirty and the frame tick paints once per frame, so
  tokens that arrive together repaint together (at 75 tok/s the region
  repainted 75 times a second before). The commit points stay immediate
  and drain the pending chunks, and the activity spinner keeps its
  120 ms pace on top. `TestFlowCoalescesDeltas` pins one paint per frame
  window.
- **the frame ticker lives only while a turn or a compaction can paint**
  (`frontend/tui`, PACKAGE.md): the 16 ms ticker used to run for the
  life of the TUI, so an idle session woke about sixty times a second to
  find nothing dirty. It starts with the turn and the compaction, and
  stops once the final commit has drained the pending chunks.
  `TestFrameTickerLifecycle` pins the four transitions.

## [0.23.1]

Each finding below carries a test that failed before and passes after.

- **the retry guard's streak identity is canonical JSON**
  (`middleware/guard`, PACKAGE.md): the bound keyed the streak on the raw
  args bytes, so a model re-serializing the same call with a different key
  order or whitespace got a fresh streak each time and could dodge the
  bound forever. `canonical` re-encodes args with object keys sorted
  (numbers and strings preserved; invalid JSON falls back to the raw
  bytes), so semantically identical retries share the streak while a
  changed value still resets it. `TestCanonicallyIdenticalArgsShareTheStreak`
  and `TestCanonicalIdentityRespectsValuesNotKeys` pin both directions.
- **an interrupted python call no longer leaves a busy kernel behind**
  (`tool/python`, PACKAGE.md): on a context cancel the call only dropped
  its reply, and the still-running cell kept the kernel slot occupied, so
  the next call queued behind it (or timed out) instead of running.
  The interrupt now tears the kernel down like the timeout path does, and
  a reply that arrived before the cancel is still returned rather than
  discarded. `TestInterruptTearsDownTheBusyKernel` pins the poison.

## [0.23.0]: the quality sweep

Each finding below carries a test that failed before and passes after.

- **tool calls are scoped to the session and the message**
  (`store/state`, SPEC_STATE): model-minted call ids are not globally
  unique, and the old `(id)`-only key let one session's result overwrite
  another's row in the shared workspace file. `tool_calls` is now keyed
  `(session_id, message_seq, id)`, the recorder scopes every call and
  result to the session and the landing message, a duplicate id within
  one message is minted to `id-2`, `id-3` (results arrive in call
  order), and the v1 store is migrated on open: `session_id` is
  backfilled from `messages` and the table rebuilt. Compaction replay
  keeps wire ids and attributes a reused id to its own re-landed
  message; the relanded result copies the `err` column from the
  original row.
- **the env and file knobs validate their bounds at the boundary**
  (`cmd/rig`, `config`): a negative `RIG_RETRIES`/`RIG_ROUNDS`/
  `RIG_RESULT_CAP` silently disabled its guard (the guard clamps
  negatives to 1 or 0), and `RIG_RETRIES` ignored a parse error while
  its siblings refused. All three env knobs now refuse loud, and
  `settings.json` `retries` gets the same `v < 0` check as `rounds`
  and `resultCap`. `jsonInt` rejects values at or beyond the platform
  range: `int()` of a float64 past `MaxInt64` is implementation
  dependent and turned `1e300` into a negative.
- **the worker spawn bounds its output capture** (`store/scheduler`):
  stdout and stderr were captured unbounded in memory; each stream now
  keeps the first and last 128 KiB of a 256 KiB budget with a
  truncation marker, so a verbose worker cannot OOM the runner.
- **the memory dedup digest is sha256** (`store/rem`): the natural-key
  digest of model-generated content was md5; the v2->v4 migration
  rehashes legacy rows and renames the column (`content_md5` ->
  `content_sha256`), so the field, the column, and the unique index all
  agree.
- **the dashboard opens the scheduler store with its migration**
  (`frontend/web`): `rig serve` alone on a v1 scheduler store 500'd
  with a schema mismatch; the state store's migration is wired there
  too.
- **a once job's `at` must be in the future** (`store/scheduler`): a
  past `at` made the crontab line fire at the next local occurrence and
  keep firing daily until a run finally succeeded. `NextFire` computes
  in the caller's location, the list shows the stored `at` for once
  jobs, and the crontab calls are bounded at five seconds.
- **an empty completion's usage no longer lands on the previous
  message** (`store/state`); the sessions list counts user turns after
  the last summary marker, so compaction replay no longer inflates the
  turn count.
- **the compact policy locks the transcript mutation**
  (`policy/compact`); the job socket is chmod'd 0600 after listen
  (`store/scheduler`); the pending plugin write opens with
  `O_NOFOLLOW` and a zone move re-checks the destination after the
  rename (`plugins`); the dashboard page limit is capped
  (`frontend/web`).

## [0.22.0]: the third review

Three PRs from the outside review, then a clean pass over every package;
each finding below carries a test that failed before and passes after.

- **edit requires a recorded observation** (`tool/file`, SPEC_CORE
  amended): a threaded session refuses an edit whose path has no
  recorded `FileState`; `read` or `write` mints the license, a
  standalone exec carries no session and so no license to check. The
  drift diff keys on the session: the remembered bytes are keyed by
  session id and path, capped at 16 MiB and FIFO-evicted, so two
  sessions sharing one process never diff each other's observations and
  the cache never pins a session or a file's bytes.
- **the provider bounds its wait for response headers**
  (`provider/openai`): `ResponseHeaderTimeout` on both transports, the
  5-minute default through `NewWithHeaderTimeout`; the plain path is
  `http.DefaultTransport`'s clone, so the dial and TLS-handshake bounds
  survive. A server that accepts and never speaks faults instead of
  wedging the headless worker; the stream after the headers stays
  unbounded.
- **evt refuses adds after stop; the loop keeps empty completions out of
  the transcript** (evt, loop): an `Add` after `Stop` queues nothing and
  returns no id, so a producer of a dead engine cannot grow it; a
  completion with no text, no reasoning, and no calls appends no
  assistant row.
- **the recorder persists file states under the file tool's lock**
  (`store/state`): the files upsert rides the wired `Snapshot` seam, so
  a user typing during tool execution no longer races the tools'
  concurrent records.

## [0.21.0]: the second review

An outside review of the whole tree; every finding below carries a test
that failed before and passes after.

- **the plugin door admits plugins only** (`specs/SPEC_GROWTH.md` 9):
  the door's `Live` seam is `Plugin(name)`, absent for a native. A
  native named through the door was reaching `Exec` past the allowlist
  and the approval gate, which key on the outer call's name.
- **the plugin zone expands `~` itself** (`specs/SPEC_SANDBOX.md` 2):
  `resolvedPath` applies the `paths` rule before the symlink walk, and
  the root lists `paths` last (outermost). A `~/.rig/plugins/x.py`
  write was passing the zone unexpanded and landing live.
- **the SSRF guard parses** (`specs/SPEC_WEB.md`): `net/netip`, 4-in-6
  unmapped, loopback/private/link-local/multicast/unspecified refused
  plus the reserved prefixes the string table named. The vetted
  addresses pin the dial; the transport never re-resolves, and every
  redirect hop re-vets and re-pins. `::ffff:7f00:1`, `0:0:0:0:0:0:0:1`,
  `0::1`, and `::0001` reached loopback before.
- **the scope resolves symlinks** (`store/scope`): the git common dir is
  realpath'd before hashing. git prints it relative from the main
  worktree and absolute from a linked one, so a symlinked cwd split one
  repo into two keys. A repo under a symlinked path gets a new key.
- **a once-job's `done` is an event** (`specs/SPEC_STATE.md`): appended
  in the run's transaction and applied by the fold; the direct
  projection write is gone. The next refold was reverting the job to
  `active`. A done job's consumed crontab line is not drift.
- **the TUI restores the terminal on every exit**: the two `os.Exit`
  paths after raw mode (a bad `-resume` id, a loop error) close the
  frontend first.
- **the freeze gate bites on `main`**: the diff base is the merge-base
  with `origin/main`, then `main`. On `main` itself the old base was
  `HEAD`, an empty diff.
- **the tui tests compile on darwin**: `pty_test.go` is `linux`-tagged;
  the goldens, the freeze gate, and the session tests run here again.

## [0.20.1]: the clock never repeats

- **`evt.Monotonic()` refuses the id collision** (`specs/SPEC_EVT.md`
  decision 2): a same-nanosecond step, or a wall clock stepping
  backwards, takes `last+1` under a CAS, so the id stays unique and
  arrival-ordered. The C accepted the collision and misordered the tie;
  in Go a repeated id was a dropped push, since the queue treats the id
  as identity. The default `Counter()` clock is unchanged.
- **`Event` loses `UpdatePriority`**: mutating a queued event's priority
  from outside broke the heap invariant; `Engine.Update` is the one
  door and already fixes the heap.
- **`Execute(ctx, event)`** takes the context first, as Go does.
- The landing page and README use plainer copy; `ci` and `pages` accept
  `workflow_dispatch`.

## [0.20.0]: documentation release

- Rewrites the documentation for direct, concise technical reading.
- Removes em dashes from Markdown, package documentation, specs, and website
  copy.
- Changes no runtime behavior.

## [0.19.1]: the phone row goes horizontal

- **the scheduler page's phone controls go horizontal**
  (`specs/SPEC_SERVE.md` 16): below 720px the job row's controls
  (pause/resume, remove, runs) no longer stack into full-width
  blocks; they keep the base flex row, wrapping, with the 44px tap
  targets (the `.rowact` class) intact. The remove-confirm and the
  runs row share the rule; the update form stays one column.

## [0.19.0]: the workers file is the fleet

- **the startup block says the fleet, and how to begin**
  (`specs/SPEC_TUI.md` 3, amended): under the session id, `workers:
  <model>` or `workers: none`, then `chat with your model, or type /
  for commands`. The live status row loses its `workers:` segment; a
  static fact reads once at start; the row is for what changes under
  you. The scheduler news line leaves the block too (SPEC_TUI 6, cut):
  identity and invitation only; a job's failure has `/scheduler` and
  the dashboard.

- **the workers file is the fleet** (`specs/SPEC_CONFIG.md`):
  `~/.rig/workers.json` is `{"model": "<merged-table id>", "slots": 1}`:
  `model` is required and must resolve against the merged models table,
  `slots` defaults to `1` and gates concurrent `delegate` calls per
  session. The embedded `models.json` sheds its `qwen3.8-workers` row
  (`local` alone), `settings.json`'s `defaultJobModel` is cut; the
  first start mints `workers.json` from it once and says so, later
  starts nag until the key is deleted, a disagreement refuses:
  and the default allow sheds `scheduler` and `delegate`, growing them
  back only when a fleet stands and the operator named no allow of
  their own. Absence of the file is absence of workers: no
  `scheduler`/`delegate` on the wire, a named `/scheduler` refusal
  (no fleet configured, the file's path in the voice), a dashboard
  refusal in place of the create form, and `workers: none` on the
  status row (a configured fleet names its model there instead).
  `store/scheduler` drops its `defaultModel` constant: `Create` takes
  the model from the caller and an empty one refuses by name, the
  scheduler tool's default and the `run-job`'s model both ride the
  fleet or the job's own row. The delegate's single per-session flock
  becomes one lock per slot: the standing "already in flight" voice at
  one slot, "the session's delegate slots are full (slots N)" at the
  full set. The dashboard's `GET /api/scheduler` carries the fleet's
  model (`worker`, empty when absent) so the view renders its half,
  and `POST /api/scheduler` refuses 400 without a fleet, supplying the
  fleet's model when a body names none.

## [0.18.0]: the job's whole life

- **the scheduler's doors come to the dashboard** (`specs/SPEC_SERVE.md`):
  `POST /api/scheduler/pause|resume|remove|update` and `GET
  /api/scheduler/runs?id=jN&n=` stand beside the create; each calls
  the store verb the `scheduler` tool calls (`Pause`/`Resume`/`Remove`/
  `Update`/`Runs`) with the `dashboard` attribution, behind the same
  walls as every write (POST, the Origin check, the body cap, the id
  checked to the tool's shape `jN`, the store's refusals by name: an
  unknown id, a removed one, an update with no change), and the reply
  is the store's voice, verbatim. The phone gets the row's hand: every
  job row carries its controls (pause or resume by state, remove, and
  the runs' audit trail) beside an update form that opens in place
  with the row's current fields (cadence, prompt, model, cwd, busy)
  and submits only what changed; the list re-reads after a move, no
  page reload. Below 720px the row stacks under full-width 44px tap
  targets (`.rowact`), the form is one column, the buttons stop
  propagation so a tap never opens two things, and remove asks once,
  in-page.

- **the scheduler gets `update`: change a job without losing its
  trail** (`specs/SPEC_STATE.md`): `{"action": "update", "id": "jN", …}`
  is partial; any of `prompt`, `cron`/`at` (mutually exclusive in one
  call, refused by name; create's refusals apply verbatim: a bad ISO, an
  invalid cron, a `once` without its `at`), `model`, `cwd`, `busy`,
  `name`. No fields → `update needs a change`; an unknown id, or a
  removed one, refuses by name; `name` keeps its store-wide uniqueness.
  One `update` op is appended and the fold overlays only the fields it
  carries: the id and the runs stay; remove + create is the rejected
  alternative (the id re-mints, the runs orphan, two crontab moves
  express one change; the trail surviving an edit is the point of having
  one). A cadence change rewrites the job's one crontab line under the
  same key; a paused job stays paused (its line rewritten commented) and
  the new line lands on resume; `pause`/`resume` stay their own ops and
  `update` never changes the state. The `/scheduler` line takes the verb
  free (the command is tool-backed): `update <id> [name <n>] [model <m>]
  [cwd <dir>] [busy <skip|force>] [cron <5 fields|once>] [at <ISO>]
  [prompt <the rest of the line>]`; prompt is last so a prompt that
  says "model" keeps its tail. The tool menu's cost is the one enum word.

## [0.17.1]: a read names its staleness

- **a read that finds a stale observation names it** (`specs/SPEC_CORE.md`):
  when the session's recorded FileState for a path no longer matches
  on-disk, the read's content opens with `[changed since your
  observation] <path>; re-read before acting on it`, and the fresh read
  re-records so it says it once; the drift refusal moved from the edit
  to the moment the model can still act on it.

## [0.17.0]: the soak's vitals

- **sessions, the soak's vitals** (`specs/SPEC_STATE.md` and
  `specs/SPEC_COMMANDS.md`, amended): a read-only native tool (the
  eighteenth) over the session store; `list` is the recent sessions
  (id, started, model, version, turns, faults; newest first), `summary`
  the vitals over the same slice (session and turn counts, the models
  with their versions, the fault count with the latest fault's first
  line, the aggregate cache ratio). The store gains the typed fault read
  (`SessionFaults`); `ListSessions` takes a named limit (`ListCap` the
  default and the maximum) and `SessionRow` carries the model, the
  version, and the fault count. `project` names another workspace (the
  state file is cwd-keyed, one per workspace; a subdirectory or a
  second worktree reads its own file); `n` caps the slice at 1..50.
  The operator gets `sessions summary` (this workspace, the tool's
  reply verbatim). Read-only: absent from the root's `mutatingNatives`
  (it never pauses at the gate) and from the concurrent read set (it
  opens a store, like todo/rem/scheduler, so it is not a pure
  observation).


## [0.16.1]: the kernel knows where it is

- **the python kernel is born in the session's working directory**: it
  inherited the rig process's cwd, so a session started one directory
  up ran kernel code whose relative paths silently pointed at the wrong
  place; a model named it from inside. `SetCwd` on the tool, wired at
  the root; the description says so.

- **grep's glob names its rule**: the schema line reads "a path glob
  (** spans directories; * does not cross /)"; `*.go` matching nothing
  cost a model a call; find already advertised the rule, grep now does.


## [0.16.0]: every row a project

- **rem, the deliberate project** (`specs/SPEC_STATE.md` and
  `specs/SPEC_COMMANDS.md`, amended): learn/reflect/recall/prune gain an
  optional `project`; a path, resolved through `store/scope`, replacing
  the session cwd for that call (worktree-safe, `~` expands at the
  `middleware/paths` boundary), so a session in `~/Projects` learning
  about `~/Projects/rig` files facts into a scope the repo recalls.
  `project` + `scope: global` refuses by name; the description carries
  one short Guidelines clause ("name project when the fact belongs to a
  repo you did not start in"). The operator gets `rem project <path>`:
  that project's live memories in `rem list`'s shape (empty names the
  project); show/forget stay id-addressed and file-wide; the 0.13.0
  forget wall stands.
- **todo is one store, scoped by project** (`specs/SPEC_STATE.md`,
  amended): the per-cwd partition is gone; one `todo/todo.sqlite`,
  every event row carries its `scope`, and the identity is never a
  filename. The scope is the repo (the short sha1 of the git common
  dir, extracted into `store/scope`), falling back to the cwd hash
  outside a repo: a session started in `~/Projects/working on
  ~/Projects/rig` files into the right queue, a renamed directory keeps
  its queue, and two worktrees of one repo share a plan. Minted ids stay
  `tN` per scope (two projects both have a `t1`); reads, folds, drift,
  and claims all filter by scope; the fold's compact and stale footer
  stay per scope. Bare read/create/every verb still resolves the scope
  from the session cwd, so the human and the agents in a directory share
  one plan.
- **the deliberate door**: every todo action gains an optional `project`
  (a path, resolved through `store/scope`; `~` expands at the
  `middleware/paths` boundary), and the operator gets `todo project
  <path>`; a one-off read of that project's queue, writes staying the
  model's or the session's own bare verbs. The empty reply names the
  queue it read (`(no tasks in <label>'s queue)`, SPEC_CORE).
- **migration, lossless**: every `<12-hex>.sqlite` in the todo dir folds
  into `todo.sqlite` with `scope = <that hash>` verbatim, in filename
  order (identity preserved without walking a hash back to a path), the
  legacy files and their `-wal`/`-shm` moved aside as `.migrated`; then
  rem's lazy re-scope moves a cwd-hash queue to the repo scope once, the
  `migrated:<oldScope>` marker, one transaction, counted once on stderr,
  and a no-op on the second open (the fold keys on the files existing).
  The dashboard's `/api/todo?cwd=` routes resolve through the same
  scope. Goldens regenerated (the schema grew `project`).
- **no colored inline text in a response** (`specs/SPEC_TUI.md` 11,
  amended): `*em*` and `` `code` `` render in the text color, marks
  dropped; bold keeps its weight, headings their accent, lists and
  quotes their furniture. The operator's call: the coloring read
  inconsistent, plain reads better. And the status row paints `auto`
  in the warn color beside `manual`; the permissive mode is the one
  worth a glance.


## [0.15.1]: the binary updates itself

- **`rig -update`** (`specs/SPEC_BUILD.md` 5): the binary's own
  installer beside `-version`; resolves the latest release by the
  `releases/latest` redirect (no API call), maps the platform into
  `rig_<os>_<arch>`, verifies the sha256 against `checksums.txt`
  before anything moves, and renames a 0755 temp over the resolved
  executable; atomic on one filesystem, a running rig keeps its old
  inode and the scheduler's next fire gets the new one. Already-latest
  is a no-op; an unwritable directory names itself and the sudo line;
  a platform with no asset and a build with no release tag each say
  so rather than downgrading.

## [0.15.0]: one store

- **the scheduler is one store** (`specs/SPEC_STATE.md`, amended): the
  cwd partition is gone; `scheduler/global.sqlite` holds every job, the
  crontab key is `jN` for all, ids are one sequence, `name` unique
  store-wide, and the `scope` arg leaves the tool's schema and the
  command's grammar (`cwd` stays the job's own field). `list` is one
  list grouped by each job's `cwd`, this directory first; an empty list
  names the store. A one-time schema-1→2 migration folds every
  `<hash>.sqlite` into `global.sqlite` (re-minted ids, runs
  re-keyed, crontab lines rewritten from `cwd-<hash>:jN` to the new
  `jN`, old files moved aside as `<hash>.sqlite.migrated`), counted once
  on stderr and a no-op on the second open. Run logs live under
  `runs/<id>/`. Goldens regenerated (the schema is on the wire).

- **a dashboard write from its own front is same-origin**
  (`specs/SPEC_SERVE.md`, amended): the Origin check accepted only the
  bound address (`127.0.0.1`/`localhost`), so behind `tailscale serve`
  every POST from the phone was `origin mismatch (same-origin only)`.
  Same-origin is now the browser's definition; the bound address, or
  the request's own front (`X-Forwarded-Proto`/`-Host`, else `http://` +
  `Host`). A foreign Origin against the real Host still refuses.

- **an empty reply names its scope** (`specs/SPEC_CORE.md`, amended):
  `grep` with a root that defaulted to a subdirectory answered "(no
  matches)" and a model read it as "does not exist", then spent four
  turns doubting a file it had just read. `ls` names the directory
  (`(empty: /abs/dir)`); `find` and `grep` the pattern, the absolute
  root and the glob (`(no matches for /re/ under /abs/root, glob
  '*.md')`); `web_search` the query; `rem` recall the scopes and the
  query (`(no memories in rig, nor global for 'q')`); `todo` that the
  queue is this directory's; `/plugins` and `plugins list` the
  directory. The `ls`/`find`/`grep` schemas say the default root is the
  working directory. Goldens regenerated.

- **the dashboard's disable/enable doors** (`specs/SPEC_SERVE.md`, 12c):
  `POST /api/plugins/disable {name}` and `POST /api/plugins/enable {name}`
  beside `approve`, each calling `plugins.Move` and replying in the
  command's voice (`disabled 'x' (plugins -> plugins/disabled); hidden
  next turn`, `enabled 'x' (plugins/disabled -> plugins); live at the
  next plugins reload`), the same walls and name refusals as every
  write. The plugins page carries the phone rule: every loaded row a
  disable control, every disabled row an enable one, the list re-read
  after the move.

## [0.14.1]: the path boundary

- **`~` is the home at the tool boundary** (`specs/SPEC_FS.md`, amended):
  one chain link, `middleware/paths`, expands a leading `~`, `~/…`, or
  `~user/…` in the path-shaped arguments (`path`, `root`, `cwd`) before
  any tool runs; read/write/edit, ls/find/grep, and bash's, delegate's
  and the scheduler's `cwd` inherit it; the tools stay pure. A model
  named the footgun from inside rig (`ls ~/Projects/x` failed where the
  absolute path worked) and the fix's shape: at the boundary, not per
  call. Bytes pass untouched when nothing expands.

- **the scheduler's cwd section names its directory**: `cwd
  /home/x/proj: no jobs` instead of `cwd: no jobs`, so an empty section
  reads as "none here", not "none anywhere"; a model read the old line
  as a lie. The description says another directory's jobs are listed
  from there.

## [0.14.0]: plugin and plugins

- **no round cap by default** (`specs/SPEC_HARDENING.md` 9, amended):
  `rounds` is `0` in the embedded settings and `guard.Rounds(0)` counts
  but never refuses; a one-shot turn with a large model on a real task
  legitimately exceeds 200 calls now that reads overlap, and a cap that
  ends a correct run mid-work is worse than the runaway it guards
  against. The retry bound and compaction still stand. `rounds: N` (or
  `RIG_ROUNDS=N`) caps the turn for an operator who wants the wall.

- **the plugin surface is two natives** (`plugin` run|schema, `plugins`
  list|create|delete|reload; `plugin_schema` and `plugins_reload` fold
  in): delete is disable; the loaded file moves into `plugins/disabled/`
  (reversible with `/plugins enable`), one `Move` shared with the
  `/plugins` command; create and the web forge share one
  `plugins.WritePending` (the filename-stem rule, the native collision,
  the DESCRIPTION/SCHEMA/def run contract; a bad name, a missing
  contract, and a collision refuse identically through both doors).

- **a settings-file allow list needs `plugins`**: a home whose
  `settings.json` writes an `allow` key replaces the embedded default
  (which is the native set), so it must carry `plugins` or every
  `plugins` call is `permission denied`; the same rule that landed
  `plugin`/`plugin_schema` in 0.12.1.

## [0.13.0]: rem is deliberate

- **the dashboard's quick hits**: the plugin listings read every
  DESCRIPTION form; the parenthesized implicit concatenation seventeen
  of the operator's plugins use listed as "(no DESCRIPTION)" (one static
  extractor in `plugins`, shared by the dashboard and `/plugins`); the
  models view shows the context window and the output length only; a
  failed task carries `retry` beside the hands (`/api/todo/retry`,
  `todo.Retry`); lines hang their wrapped text under the text, not the
  glyph, on phones.

- **rem is deliberate** (`specs/SPEC_STATE.md`): every rem operation is
  something chose; the model learns/recalls/reflects/prunes through its
  tool, the operator prunes through the `/rem` verb; nothing is written
  by a compaction and nothing is read into the prompt by a session
  start. Two cuts, one new verb surface, one scope change.

- **the injection is cut**: the root's `remembered` segment (the
  `remstore.Recent` read, `rememberedK`, `renderRemembered`) is gone:
  no memory is read into the prompt at a session start. The system
  prompt's "Remembered notes are suggestions…" becomes "Memory is a
  tool: recall before re-deriving a project fact, learn deliberately
  what the next session should not re-derive, supersede by id when the
  code disagrees" (settings.json, the goldens, the config tests).

- **the auto-reflection is cut** (`specs/SPEC_COMPACT.md` 6): the
  `WithAutoReflect` seam, `store/rem.AutoReflect`, and the
  `autoReflectionImportance` constant are gone; compaction writes
  nothing to rem. The summary stays a marked user row in the transcript
  (context, not memory).

- **scope is a repo identity**: `store/rem` hashes the absolute git
  common dir of the cwd (`scopeKey`), so two worktrees of one repo share
  one memory; a non-repo dir scopes by cwd, hashed as today. The schema
  bump (1 → 2) carries a one-time idempotent migration
  (`remstore.Migration(cwd)` through `store.Open`'s migration hook):
  rows under an old cwd-hash scope now inside a repo re-scope to the
  repo's, and rows with source "session compaction" are removed (never
  deliberate), counted once on stderr. The migration is one transaction
  and survives two processes opening the file at once (`INSERT OR
  IGNORE` on the marker). Known bound: the re-scope is keyed on the
  launch cwd; rows learned from another directory of the same repo move
  when rig is next started there, since a hash cannot be walked back to
  its cwd. The git probe resolves a relative common dir against the cwd
  and treats an echoed option (git < 2.31 passes `--path-format` through,
  exit 0) as no path.

- **`store.Open` refuses what it cannot migrate**: an older file opened
  by a build that passes no migration is a named mismatch, not a silent
  version stamp; the newer-file refusal runs before any schema statement
  touches the file; the migrations and the version bump commit together.

- **`/rem forget` is scoped**: ids are file-wide, so a typo must not
  reach another repo's row; only this project's or a global memory is
  removed; another project's id is refused by name with its label.

- **the `/rem` command** (`specs/SPEC_COMMANDS.md` 11): `rem [list|show|
  forget]` over the same store; list the live memories (project then
  global, one line each), show by id, forget by id. The model's
  multi-line learn/recall/reflect/prune stays the tool; the operator's
  read and prune get a typed line. Rejected, named: `rem pin`; importance
  is only the per-access reinforcement multiplier, so the verb changed
  nothing the operator could see, and an operator write that is not a
  prune is off the surface.

## [0.12.2]: the two bounds

- **the description's shape** (`specs/SPEC_CORE.md`, the Tool section):
  every tool's description is the same four parts; what, a
  `Guidelines:` sentence on when and when not (it was missing from 13 of
  18), the reply's shape, at most one gotcha; storage mechanics leave
  the wire for the `PACKAGE.md`s; the scheduler's "pi session" / "pi
  model id" become rig's words. A case in `cmd/rig` pins the whole
  menu under 14,000 chars on the wire and refuses another harness's
  voice, so growth is a decision; the schemas of rem, scheduler, todo,
  and diff are the next lever, named.

- **no comments in Go, anywhere**: the sweep strips every comment from
  implementation and test code (147 files; generated code and the
  `metadata` packages exempt; lift reads their doc comments), the
  substance that was not already in a `PACKAGE.md` lands there (three
  new: the root kernel, `frontend/oneshot`, `middleware/approve`), and
  AGENTS.md carries the one rule with its reason: a small model reads
  the repository as one corpus. The freeze gate now compares
  comment-stripped sources, so a comment is never a change to the
  frozen surface. No behavior change: the stripper refused any file
  whose comment-free AST would differ, and the suite is green under
  `-race`.

- **the round cap** (`specs/SPEC_HARDENING.md` 9): `guard.Rounds(n)` counts
  every tool call in a turn (settings `rounds`, default 200, `RIG_ROUNDS`
  env with a loud invalid-value refusal) and past `n` refuses every
  further call without executing, in a teaching voice naming the cap and
  what to do; stop and report, or ask the operator. It caps the
  alternation the retry bound's per-args streak does not (SPEC_HARDENING
  7's named consequence) and a runaway batch: a concurrent run of 50
  reads counts 50, the cap on calls, not turns. The counter sits under a
  mutex (SPEC_EVT 6's concurrent chain) and `TurnStart` clears it like
  the bound. `core/` and `loop/` stay frozen at 0.12.0.
- **the result bound** (`specs/SPEC_HARDENING.md` 9): `guard.Cap(bytes)`
  (settings `resultCap`, default 64 KiB) bounds every tool result before
  the transcript, in one place; an oversized result truncates to the
  head and the tail with the loud `[TRUNCATED]` marker naming the full
  size and the teaching line "re-read a narrower range"; a small result
  is byte-identical. It closes the named field failure: a 287 KB read
  result that fed the 2026-08-21 compaction fault and sat in context all
  session. Every tool's own cap stays; this is the wall behind them.
- **read's narrower range**: `tool/file`'s read gains `offset`/`limit`
  line arguments (past-the-end and negative refusals loud), so the
  teaching has a real door.
- **wiring**: both are innermost after the bound (first-listed is
  innermost) in the root's chain; workers and delegated workers run the
  same `wire()`. The TUI shows a capped result's marker the way it shows
  bash's.
- **test-only data races closed**: `cmd/rig/plugins_test.go:87` (the fake
  plugin server read `replies` the test set without the mutex; the write
  now holds it) and `tool/delegate`'s in-flight wait (`len(spawn.calls)`
  read without the mutex while the spawn goroutine appends; the read now
  goes through a locked `count()`). CI's test step runs `go vet ./... &&
  go test -race ./...` so a race cannot return.

## [0.12.1]: the door is allowed

- **the plugin door was never allow-listed** (`specs/SPEC_GROWTH.md` 9,
  amended): `plugin` and `plugin_schema` joined the native set in the
  door round but not the embedded `allow` default, so every plugin call
  through the door since 0.9.0 was `permission denied: plugin is not in
  the allow-list`; on a home without an `allow` key (the embedded
  default), which is the daily driver's. The two names are in the
  default now, and a named case pins the rule the bug broke: the
  embedded allow is the native set, exactly.
- **the plugin switch is a directory** (`specs/SPEC_GROWTH.md` 9,
  amended): `settings.json`'s `plugins.enabled` inverted the default:
  on a home with no list, `disable` changed nothing and `enable X` hid
  every other plugin, and its filter ran only at wiring, so a reload
  undid a toggle until the next start. Retired. `/plugins disable
  <name>` moves the file into `plugins/disabled/` and reloads; `enable`
  moves it back; `plugins disabled` lists the zone; the dashboard shows
  all three zones; `max` now applies on every reload. A non-empty
  `plugins.enabled` refuses at load naming the move.

## [0.12.0]: the event loop

- **the loop is the engine's consumer** (`specs/SPEC_EVT.md` 7, the
  named reopening after 2a): every step of a turn is a closure on the
  loop goroutine; the Frontend's `Input`, the provider's stream, and
  every tool run are producers that post (input 90, stream 50, tool
  completion 50). The Frontend contract, the event bracket, the
  transcript, and every named loop case are unchanged; thirty-six
  cases pass byte-for-byte under `-race`; two new ones pin the one
  goroutine and the stale-event rule. The loop goroutine never blocks
  on a tool; `Assemble` and the `Stream` call still run on it (named).
  `loop` now imports `evt`, an in-repo stdlib-only leaf.

- **the batch** (`specs/SPEC_EVT.md` 6, the named reopening of the
  frozen loop): a turn's tool calls the kernel's `Concurrent` predicate
  admits run together (bounded by `Parallel`, default 8); any other call
  is a barrier in call order; results are emitted and appended in the
  order the model asked, each with its own duration. The root admits the
  pure reads (`read`, `ls`, `find`, `grep`, `web_search`, `web_fetch`,
  `diff`); everything with effects, a store, or the shared kernel stays
  sequential. The guard and the file tool lock the state they share
  across a run. A nil predicate is the 0.11 loop, byte-identical. The
  freeze gate carries the reopening by name; the re-freeze follows the
  merge.

## [0.12.0]: the event loop

- **the event loop, phase 1** (`specs/SPEC_EVT.md`): libevt's shape
  (`~/Projects/libtrdr`) made Go-centric as the leaf package `evt`:
  `Context`, `Event`, `Queue` (the 4-ary max-heap, priority desc then
  arrival, with the one addition `Update`), `Engine` (one consumer,
  many producers, a mutex and a cond where the C spun), `Scheduler`
  (the harness, the codes as errors), `Clock`. libevt's tests by name.
  Consumed by nothing yet: phase 2, named in the spec, makes the turn
  loop its consumer (parallel tool calls as goroutines posting ordered
  completions) and reopens SPEC_CORE.

## [0.11.0]: the delegate

- **the one-shot worker tool** (`specs/SPEC_DELEGATE.md`): `delegate`
  spawns a headless worker on a task now, in a cwd under the session's
  or the rig home, waits, and feeds back the worker's last message.
  One tool over the existing runner; the jail per the sandbox setting
  (fail closed exactly as workers do), the socket proxy, the worker
  command, the GPU busy rule with `busy:skip` semantics. A failed or
  timed-out worker is a tool error naming which; a timeout kills the
  worker's process tree.
- **the record and the transcript**: every delegation is a recorded
  run in the cwd-scope scheduler store under a minted ad-hoc key (no
  crontab line, nothing scheduled), so `scheduler runs` and the
  dashboard show it beside cron runs with its log path; the worker's
  transcript is its own resumable session in the state store (the
  jailed worker's sessions dir is bound in), named by the tool result
  for `sessions resume`.
- **the bounds, named**: one delegation in flight per session (a
  concurrent call refuses); a worker cannot delegate (the `RIG_DELEGATE`
  marker, refused by name); the embedded allow default gains
  `delegate`, a worker's allow-list omits it; `delegate` counts as
  mutating for the approval gate, whose prompt shows the task's first
  line.

## [0.10.2]: the todo's hands

- **start and done from the dashboard** (`specs/SPEC_SERVE.md` 15): a
  pending task carries `start` and `done`, an active one `done`; each is
  the store's own verb (`todo.Start`, `todo.Complete`) attributed to
  `dashboard`, the reply verbatim, the queue re-read; a task the model
  claimed refuses in the store's voice (fail it first to take over), as
  the tool would.
- **the models view in three rows**: name and role, the context line
  (window · max · reserve · keep · trigger), the effort ladder in the
  ramp's colors.
- **the mobile header stacks**: the workspace on its own row, add and
  browse on the row after.

## [0.10.1]: the dashboard in the TUI's grammar

- **the dashboard speaks the TUI's grammar** (`specs/SPEC_SERVE.md`
  11–14): every view is a tool block (`● name · detail` … `name ✓`),
  input is a `❯` prompt row, no panels; the models view in the `/models`
  table's line with the effort ramp; the memory tab and route removed;
  the plugins page split approved | pending with the forge; an embedded
  python editor (gutter, highlighting, no dependency) over three doors:
  source by zone, save into the pending zone (the contract checked, a
  native name refused), approve (the command's verb; an installed name a
  409 until `replace`); a folder browser for the workspace picker, rooted
  at home, directories only, symlinks resolved, capped; the mobile nav
  toggle hidden on desktop (the bug: it was hidden by a class the button
  did not carry).
## [0.10.0]: the dashboard's polish round

- **the polish round** (`specs/SPEC_SERVE.md`, phase 2): the local
  dashboard grows its two named phase-2 writes; a scheduler create
  (the same `scheduler.Create` verb a live session calls, attributed to
  `dashboard`, the runner command the root wires) and a plugin create
  (one contract file into `plugins/pending/`, the provenance rule's
  landing zone, refused by name against both zones). The plugin listing
  goes live (read per request, never cached), the cwd picker accepts
  new workspaces (client state over the server's list), and the sidebar
  collapses into a drawer below 720px.
- **the TUI homage** (phase 2, decision 10): the page adopts the TUI's
  visual grammar; the todo and scheduler text are parsed with the
  `frontend/tui` `tools_render.go` rules and rendered in the oled slots
  (the progress bar, the status glyphs, the dim metadata, the warn
  `drift:`), the sessions and transcript take the tool-block shape
  (the `✓ name · detail` opening, the `❯` prompt, the `→` result, the
  aggregated usage row), and unparseable text keeps the verbatim
  `<pre>` (the TUI's own fallback).

## [0.9.2]: the streamlined contract and the self-healing door

- **the contract split** (`specs/SPEC_STREAMLINE.md` 1): the standing
  context carries shape, the responses carry semantics. The todo,
  scheduler, and rem descriptions trim to the verbs and the arguments;
  the state machine, the claim rules, the auto-start, and the
  compaction sentence ride the voices that already teach them on
  contact. The pinned goldens regenerate in place (the SPEC_PLUGINS 8
  precedent); the wire carries the trimmed bytes.
- **the compaction fact** (2): the operation that crosses the
  1000-event threshold names the fold in its own reply:
  `· log compacted (N events folded into the snapshot)`, so the stale
  footer's quieting after the fold reads as explained, not as state
  loss. Below the threshold the replies are byte-identical to
  0.9.1's.
- **the minted-id voice** (3): the unknown-id refusal at every verb
  now reads `no task 'tN' (ids are minted by the tool; copy from a
  reply)`, the teaching on contact instead of standing prose.
- **the door's self-heal** (4, 5): the `plugin` and `plugin_schema`
  doors take a redo seam; an unknown name runs the root's reload once
  and re-resolves, a nil redo keeps today's refusal, and a failing redo
  is named in the refusal. The authoring dance loses its reload step:
  write to pending, the operator approves, the model calls the door:
  and the `/plugins create` template says so. `plugins_reload` stays
  the operator's explicit verb.

## [0.9.1]: the estimate tells the truth

- **calibration trusts a measurement, not a turn** (`specs/SPEC_COMPACT.md`
  4, amended): a delta under 2% of the anchor no longer moves the factor
 ; a tool-loop turn's `reported − anchor` is the template's overhead and
  the reasoning it keeps or strips, none of it in the delta's bytes; the
  clamp ceiling is 2.0. The field shape: per-turn ratios of 44, 0.31,
  31.8, 0.02 pinned the factor at 4, the brain compacted 20 times at ~50k
  real tokens, and a 125k summary input read as 427k and faulted.
- **history reasoning leaves the estimate**: `Reasoning` counts on the
  last assistant message only; every chat template strips it from prior
  turns, so the server never counted the 8.3 MB one session carried; a
  `-resume` of it would have compacted everything on the first assemble.
- **the oldest slice that fits** (3, amended): an older prefix whose
  summary input does not fit the window is cut to the largest prefix that
  leaves the summary floor, one call, the remainder folding on a later
  pass, where it was a fault that stuck the session until `/new`. A
  single message that alone does not fit is still the loud failure.

## [0.9.0]: the plugin door and the enablement

- **the plugin door** (`specs/SPEC_GROWTH.md` 9, SPEC_PLUGINS 7's named
  "later decision once the count grows"): the count has grown, and the
  flat shape is the context problem. `toolset.Carry` stamps the natives
  plus one `plugin` door into every request instead of every per-plugin
  schema; `plugin_schema` fetches one plugin's contract on demand.
  Plugins stay real `core.Tool`s in the table, callable by the python
  tool and by the door's name. The door's `name` enum is the live
  plugin names (the swap's own list), cheap.
- **the enablement** (`settings.json` `plugins.enabled` + `max`, SPEC_CONFIG):
  a disabled name is not wired as a tool (the door's enum carries enabled
  plugins only), hidden entirely; `max` caps the enum. `/plugins enable
  <name>` / `disable <name>` toggle the file and reload; the
  models-switch semantics, next-turn.

## [0.8.2]: the plugins land

- **the allow-list's presence reversal** (`specs/SPEC_PLUGINS.md` 7,
  amended): an installed plugin's presence in `plugins/` root is itself
  the allow-list entry. The provenance rule forces a model's
  `write`/`edit` into `plugins/pending/`, so a plugin in the root can
  only have arrived by the operator's `/plugins` approve; that presence
  IS the admission, and the operator need not add a name to `allow`.
  The mechanism is a second door (`perm.AllowlistWithDoor`) wired to the
  live plugin table's `IsPlugin`: a name the table carries as a plugin
  passes though absent from the static list. Plugins only, never natives
  (the collision rule keeps the sets disjoint); nil door is today. Pinned
  with approved-passes / pending-refused / deleted-after-reload-refused /
  native-still-refused. `loop/` and `core/` stay byte-frozen.
- **the plugins are a live surface** (the local install): `listen`,
  `net_conn`, `syshealth`, `url_check`, and `plugin_scaffold` land as
  real tools; read-only probes, the SSRF-guarded fetch, and the
  scaffold that writes the next plugin into `pending/`. `plugin_scaffold`
  closes the loop: author, approve, reload, land.

## [0.8.1]: the walls speak

- **the system prompt names the walls** (`config/settings.json`): the
  default grows from two sentences to five; the harness enforces an
  allowlist, a retry guard, an approval gate, and a plugin landing zone,
  names each refusal, and a refusal is final for that call (change it or
  ask, never reach the same effect through another tool); remembered
  notes are suggestions, the code and the spec are the truth; python is
  a persistent kernel and a capability built twice belongs in a plugin.
  Operator-overridable as before (`system`, `RIG_SYSTEM`, `-system`).
- **python's action vocabulary is closed** (`specs/SPEC_PYTHON.md`,
  amended): `code` (or no action) runs the code, `vars`/`reset` are the
  host's, anything else is refused by name before the kernel is touched.
  The field failure: a model sent `action: "code"` on every call, the old
  dispatch forwarded it without the code, the host ran the empty string,
  and 457 calls came back `(no output)` ok; a silent success that ran
  nothing. `code` joins the schema enum.
- **the guard's spec says one thing** (`specs/SPEC_HARDENING.md` 7):
  the streak is per tool with a last-failed-args marker; identical
  retries cap, a changed call resets; the named consequence, alternating
  failing calls never trip the bound; the test is named for what it
  asserts (`TestDriftingArgsEachGetAFreshStreak`); `bound.go` carries no
  comments.
- **the flaky kernel test was a race** (`tool/python`): the unwritable-
  kernel host now closes stdin before it answers, so the client's next
  write is EPIPE by construction instead of sometimes waiting out the
  timeout on a loaded runner. Flaky is a bug, never a rerun.
- **distribution, proven**: v0.8.0 shipped through the tag path on the
  first try; the installer at `https://mrsirg97-rgb.github.io/rig/install.sh`
  verified end-to-end; the repo is public.

## [0.8.0]: the modes

- **`/effort`** (`specs/SPEC_MODES.md` 1): the session's reasoning
  budget in the row's own vocabulary (`models.json` `efforts`); a
  provider decorator stamps the dial onto requests that carry none;
  the compaction summary's own effort survives untouched; unset is
  today's bytes. A model switch resets a level the new row does not
  name; loudly, in the `/models` reply; never stamping a level into
  a template that cannot speak it.
- **`/role`** (2): default, architect, or reviewer: the stance's
  prose sits between the system prompt and AGENTS.md (position is
  precedence: the contract reads after it and wins), rebuilt on the
  switch, next-turn.
- **`/approve`** (4): auto (today) or manual: every mutating tool
  call pauses for the operator's y/n at a TUI ask row (y runs, n
  declines, Esc declines and interrupts; a denial is a model-visible
  teaching refusal, the turn continues). The gate is a middleware
  wired with closures; the ask door is an optional frontend
  interface; `core/` and `loop/` byte-frozen, the Frontend seam
  untouched. The read set and the store tools pass silently; every
  plugin pauses. The default is settings.json `approve`; workers and
  the one-shot never ask.
- **the status is three rows** (3, amended): identity (`model ·
  used/window`), the stance (`effort · role · auto|manual`; the
  effort in pane's footer ramp colors, new `effort*` theme slots;
  the role abbreviated; manual in the warn), and the usage totals.
- **the distribution surface** (`specs/SPEC_BUILD.md` 5): the first real
  tag ships through a release workflow (the tag asserted against the
  `Version` const before any asset is built, `CGO_ENABLED=0` cross-builds
  linux/darwin x amd64/arm64, checksums, provenance attestation, the body
  from the matching CHANGELOG section), a POSIX installer fetches and
  verifies the binary, and a static install site serves it.

## [0.7.0]: the reload and the forge

- **plugins register without a restart** (`specs/SPEC_PLUGINS.md`
  decision 8): `plugins_reload`, the fifteenth native; re-runs the
  discovery over the rig home's `plugins/` directory (the same loud
  skips, the same collision refusal, removal free: the list rebuilds
  from disk) and swaps the kernel's tool list at the root. The seam
  is named and built (`middleware/toolset`, pure core): the root owns
  one live tool table; a provider wrapper stamps the table's specs
  into every request before delegating, and a middleware, innermost
  first, resolves a call against the table before the chain's
  participants bound its result, falling through to the loop's own
  exec. A swap is one atomic write to that table: the next turn's
  request carries the new list and the new tool executes, by
  construction; the models-switch's semantics, **zero `loop/` and
  `core/` lines** (both byte-frozen against the branch's base), the
  loop's snapshot is the bootstrap and the table is the truth.
  `/plugins reload` is the operator's verb (the same re-discovery,
  from the command door); `/plugins create <text>` queues the
  authoring prompt (the steer precedent: the command queues a line,
  never dispatches a turn); the forge's contract, the model
  authors, the plugin lands in the pending zone (SPEC_SANDBOX's
  provenance, the gate this decision waits on), the operator
  approves. **The approve's tail is the reload's**:
  `/plugins approve <name>` moves the file and re-registers, and the
  next `/plugins` listing follows the swap (the listing reads the
  root's state at call time). The reload imports into the running
  kernel, so a new plugin's functions are callable from the python
  tool immediately; the shared namespace, one process, and callable
  as a tool on the next turn. The no-plugins wire's golden pin moves
  with the set: the fixtures regenerate in place (the 0.2.0 wire
  baseline, the 0.7.0 native set; 15, `plugins_reload` among them),
  as the earlier releases' did. `core/` and `loop/` zero diff.
  Version 0.7.0; still pre-1.0.

## [0.6.0]: the worker jail

- **the scheduled worker runs jailed** (`specs/SPEC_SANDBOX.md`
  decisions 1, 3, 5): the `run-job` runner spawns the worker under
  bubblewrap's `--unshare-all` profile; the spec's block, verbatim,
  composed by a pure function (`store/scheduler/jail.go` pins it
  line-for-line): the ro system, the fresh `/proc`/`/dev`/`/tmp`, the
  job's cwd rw, the operator home's kernel directory and the rig
  binary ro, the socket's one bind, and the worker payload. The
  worker is netless except **one unix socket**; the runner's socket
  proxy (stdlib `net` + `httputil`, no new Go dependency) listens on
  it and forwards to the swap endpoint, the OpenAI `/v1` prefix
  applied exactly once, whatever the operator's spelling; the socket
  is removed after the run, nothing answers. The worker's rig home is
  the **scratch home** `<job cwd>/.rig-job`: the worker's stores land
  inside the jail, and a worker that cannot write the operator's
  stores cannot poison the next session's transcript; the
  operator's `~/.rig` is byte- and mtime-untouched after the run.
  **Fail closed is the default**: `sandbox: "jailed"` refuses the run
  loud (recorded as a skip, the outcome row carries it) when bwrap is
  absent, on a non-linux platform, or where the operator home's
  kernel directory is absent; the refusal names bwrap and the
  profile and teaches both settings keys; `sandbox: "off"` is the
  operator's explicit act, runs the worker as before with exactly one
  loud line per worker run. `sandboxBinds` rides the profile as extra
  binds (an absolute path, ro by default, `:rw` opts one in; the
  operator's venv need). The provider dials a `unix:` base URL
  (socket transport, the OpenAI path clean on the wire). The
  interactive REPL never consults the sandbox code (the fake PATH
  shim's marker stays untouched, the golden path is byte-for-byte
  the model's reply). The real-jail fixture jobs gate on a box that
  can run unprivileged bwrap (the probe is the profile's mechanics;
  the skip names the box's
  `kernel.apparmor_restrict_unprivileged_userns`); a bare box skips
  cleanly, no flake. SETUP names bubblewrap as the jailed worker's
  one environment dependency (the package is `bubblewrap`, the
  binary `bwrap`) and the Ubuntu 24.04 sysctl's named workaround.
  `core/` and `loop/` zero diff. Version 0.6.0; still pre-1.0.

## [0.5.0]: the plugin provenance rule

- **creation separated from installation** (the forge's gate,
  `specs/SPEC_SANDBOX.md` decision 2): `~/.rig/plugins/`, top level,
  stays the TRUSTED set the discovery loads; `~/.rig/plugins/pending/`
  is the FORGE's landing zone; invisible to discovery by the existing
  top-level rule (no loader change at all), created at startup, silent
  and idempotent (the write tool makes no directories, so the model's
  first pending write must not depend on the operator's mkdir). The
  perm middleware gains one path rule beside the allow-list: the
  model's `write` and `edit` refuse a target inside `plugins/` that is
  not inside `plugins/pending/`, and the refusal teaches:
  `permission denied: <path> is in plugins/ outside plugins/pending/
  (plugins install by the operator's /plugins approve; write to
  plugins/pending/)`. The rule is the guard for the honest path, not
  the boundary: bash can still move a file there (the operator's shell
  is the operator's); the worker jail is the boundary, the provenance
  rule is the workflow. `/plugins pending` lists the zone with each
  file's DESCRIPTION (the file's top-level string literal, read without
  running the file; a pending file is untrusted); `/plugins approve
  <name>` moves one to the top level with the atomic rename; a name
  that collides with a native tool refuses with the existing rule's
  voice (the existing rule at the new door), and a file of the name
  already installed refuses too (a clobber is not an operator's verb by
  accident). Approval is the operator's verb: it never runs from a tool
  call (the command door is Frontend-side by construction). The reload
  is SPEC_PLUGINS decision 8's, and the forge (SPEC_PLUGINS 8) is
  unblocked by this. `core/` and `loop/` zero diff. Version 0.5.0;
  still pre-1.0; the freeze's discipline and the tag's criterion
  unchanged.

## [0.4.0]: the rig home and the python plugins

- **the rig home** (the `~/.config/rig` move, `specs/SPEC_CONFIG.md` 11):
  the config home is `~/.rig`; the `.pi`/`.omp` convention, not the XDG
  one. The resolution is stated once: **`$RIG_HOME` > `~/.rig`**; the
  env var (non-empty) is the home, the operator's spelling used as-is;
  unset, `~/.rig`. `RIG_HOME` is the one new env var (the config spec's
  non-goal's named amendment); `XDG_CONFIG_HOME` is no longer consulted.
  The migration is once and deterministic: at startup, if the resolved
  home is **absent** and the old `~/.config/rig` **exists**, the old
  directory is **renamed** to the resolved home (atomic; both live
  under `$HOME`) and exactly one line says so; a present home wins,
  whatever the old directory holds, a failed rename refuses the
  start loud, and it is a **default-path event**: under an explicit
  `RIG_HOME` the migration never runs (the override is isolation, not
  a move order; an absent override stays absent, the old home stays
  put). The stores, the python kernel's materialised host
  (`~/.rig/kernel/`), and everything else ride the home; the root and
  `tool/python`'s host resolution carry the same one rule, named.
  `config.Load(dir, cwd)` keeps its seam; the invariant's companion
  holds: a fixture run has neither home, so the 0.2.0 wire stays
  byte-exact.

- **python plugins** (the pre-1.0 extension surface,
  `specs/SPEC_PLUGINS.md`): one file under `~/.rig/plugins/`, one tool
  per file, the name the filename stem. The file's contract is three
  names: `DESCRIPTION` (str), `SCHEMA` (dict, the wire's
  `function.parameters`), `run(args: dict) -> str`. Discovery at startup
  through the **shared python kernel**; the same persistent kernel as
  the `python` tool, one process, the namespace shared on purpose (the
  model's python can call plugin functions directly; plugin state
  persists across calls); imports each file, reads the three names,
  and registers the tool on the existing `core.Tool` seam,
  indistinguishable from a native tool on the wire (the wire's head is
  the 14 natives in order; the plugins ride the tail, in file order). A
  file missing a piece or failing import is a **loud skip** (one line
  naming the file and the field; the kernel's own voice; startup
  continues); a name colliding with a native tool is a **loud refusal**
  at startup (native-wins would be silent shadowing; refuse instead);
  a kernel-level discovery failure refuses loud (fail closed). A call
  invokes the module's `run` with the model's args dict in the same
  kernel: the return `str()`s into the tool result, an exception is a
  tool error carrying the traceback tail, and the kernel stays alive.
  No plugins directory (or an empty one) is a no-op that never starts
  the kernel; the `golden_020` fixtures are untouched. `/plugins` is
  the standard set's eighth entry: the loaded (name, description, file)
  and the skipped (file, reason), in file order. The plugins are
  subject to the allow-list like any tool and are **not** in the
  built-in default (the operator allow-lists a plugin's name). The
  sandbox is named and deferred: pre-sandbox, trust the plugins as you
  trust your own python. `core/` and `loop/` zero diff; the `plugins/`
  leaf and the `tool/python` `Run` door (the raw reply the discovery
  and calls ride) are the only new surface. Version 0.4.0; still
  pre-1.0; the freeze's discipline and the tag's criterion unchanged.

## [0.3.0]: config as a first-class runtime component

- **config loading** (the pre-10 runtime component, `specs/SPEC_CONFIG.md`):
  flags, env, and constants become a four-layer resolution per key:
  **flags > env > file > embedded defaults**, with the defaults moved out
  of code into the embedded `config/settings.json` and
  `config/models.json`. `config/` is a new stdlib leaf that parses; the
  root (`cmd/rig`) consumes; one `config.Load(dir, cwd)` per process,
  after flag parse and before any store is opened, on every entry mode
  (REPL, `-p`, `run-job`; the worker inherits its own job-cwd's
  `AGENTS.md`, not the creating session's). The user files under
  `~/.config/rig/`: `settings.json` (the existing knobs, flat, by their
  env names; `allow` a JSON array there, CSV in the env), `models.json`
  (the table out of code: `models.Defaults` removed, the user file merges
  over the embedded row by row; set fields replace, unset fields keep,
  new ids added with required numerics; rows gain `role`
  (interactive/worker, the `/models` column) and `effort` (the
  compaction summary call's reasoning effort, `""` = the policy's
  `medium`)), `AGENTS.md` (global then `<cwd>/AGENTS.md`, placed between
  the system prompt and the participants' guidelines), and `theme.json`
  (reserved for the TUI: the loader reads it raw, well-formedness only).
  Malformed or unreadable files refuse at start, naming the file and the
  field (the operator's JSON spelling); absent files are silent; unknown
  keys refuse. The two presence keys (`webFetchProxy`, `trafilatura`)
  keep the 0.2.0 set-empty semantics at every layer; `RIG_MODEL_*` now
  overlays the active id's row fields, set beats the row; the scheduler's
  default job model moves to the settings (`defaultJobModel`, file over
  embedded; the store keeps its constant as the direct-`Create` safety
  net). **The invariant**: with no user files, every entry mode is the
  0.2.0 bytes; pinned against golden request-body fixtures captured from
  the 0.2.0 build (the named exception: the `/models` role column).
  Version 0.3.0; `core/` and `loop/` zero diff.

## [0.2.0]: the feature-complete runtime

- **user commands** (roadmap deliverable 9, `specs/SPEC_COMMANDS.md`): the
  command seam and the first seven commands; the human's own verbs, over
  the same `core.Command` registration as the tools (`WithCommands`),
  dispatched Frontend-side by the `/` prefix before `Input` returns to the
  loop (zero loop change; a frontend without dispatch stays byte-identical,
  `//` escapes the prefix, an unknown command is a loud refusal). `compact`
  forces the compaction policy's exported `Compact(ctx)` seam (the caller
  owns the `Compacted` delivery); `new` closes the session row `ok`, mints
  a fresh session and recorder, and re-targets the retiring recorder before
  its in-flight `Input` completes (the handoff); `sessions` lists (newest
  first, capped, turns = user rows minus `[compaction]` rows, unclosed rows
  render `exit open`), shows (the `-resume` projection, rendered plain),
  and resumes (validate-before-mutate over the real store); `models` lists
  the runtime table (the env-synthesized row included) and switches the
  active model by rebuilding the provider+policy pair at the root, effective
  on the next turn's request; `steer` is the deliverable 7 slot made a verb
  (queue-and-interrupt, latest wins); `todo` and `scheduler` parse the line
  into the same tool instances the model gets and print the reply verbatim.
  One-shot and `run-job` never dispatch: a command-shaped prompt is a
  prompt. The runtime is feature-complete (Version 0.2.0; the freeze
  discipline holds, the 1.0 tag waits for lived use); the loop is
  byte-identical through 9.

- **openai adapter: truncated tool calls** (fix surfaced by the compaction
  live e2e): a stream cut off by `max_tokens` can cut a tool call's
  `arguments` mid-JSON while still reporting `finish_reason: "length"`. The
  adapter now checks the accumulated args with `json.Valid` before emitting
  the `ToolCallEvent`; invalid args fault with the truncation and the finish
  reason named, no partial call in the transcript, no `Done`. Empty args
  (a no-arg call) stay legal. Named test
  `TestLengthFinishedTruncatedToolCallArgsFault`, complement
  `TestNoArgToolCallStillEmitted`; the invariant is recorded in the event
  contract (`specs/SPEC_CORE.md`).

- renamed looper -> rig

- **compaction** (roadmap deliverable 8, `specs/SPEC_COMPACT.md`): the
  first non-passthrough `ContextPolicy`; `policy/compact` wraps the
  passthrough (byte-identical below the trigger) and, at a window-relative
  per-model trigger, rewrites the older transcript into a summary through
  the same `core.Provider`, keeping a whole recent tail (`models` table:
  `local` and `qwen3.8-workers`, `RIG_MODEL_WINDOW`/`RESERVE`/`KEEP_RECENT`
  env synthesis, loud refusal at start). The trigger anchors on the
  server's own count (`Message.ContextTokens`, loop L8 stamps
  `Done.Usage`'s prompt+completion), calibrating only the delta. Overflow
  recovery: a provider decorator classifies a context-length fault and
  compacts-and-retries exactly once, never a silent loop. The summary is a
  marked user row (`[compaction] `) the CLI renders as one line, the
  recorder lands it plus its usage row and re-lands the kept tail
  (fresh seqs, fresh call ids), `-resume` projects from the last summary
  row, and the summary is handed to rem's `AutoReflect` (deduped,
  low-importance, scoped to the cwd). `-p` workers get the same wire and
  numbers; `Request.MaxTokens` carries the request-side reserve on the
  wire. **Refuse-loud clamp**: when `Window - est(request)` is below the
  minimum (the smaller of `Reserve/4` and 256) the decorator fails loud
  (a `Fault`, so `-p` exits non-zero) instead of the floor-1 one-token
  answer that logs success; a kept batch larger than the model can hold.
  The summary call carries a lower reasoning effort (`medium`) where the
  provider supports it (`Request.ReasoningEffort` on the wire in both
  shapes: top-level `reasoning_effort` for OpenAI-shaped servers and
  `chat_template_kwargs.reasoning_effort` for llama.cpp; measured on the
  swap, only the kwargs entry changes the think length): it is the one
  call whose thinking nobody reads. The summary prompt also says prose
  only, no tool calls, so a max-effort model can't answer the summary
  with a call. **The summary request is two messages**: a short system
  role, and one user message carrying the older prefix as a quoted
  `<transcript>` block (role-prefixed lines, tool calls and results
  included) followed by the prompt's instruction; the prefix is data,
  not a live conversation, so the model summarizes it instead of
  continuing it (a last "reply with only X" stays inside the block).

- **runtime hardening** (roadmap deliverable 7, `specs/SPEC_HARDENING.md`):
  the seams and events everything after this needed, in one named loop
  change (L1–L7). **Tool events**: `ToolStart{Call}` and
  `ToolResult{ID, Content, Err, Duration}` bracket each execution in the
  Event vocabulary (the bracket wraps the whole middleware chain, so the
  result carries the guarded verdict); the CLI renders `● name` and
  `name ✓|✕ duration` and the old `[call]` line is gone; the recorder's
  middleware tap is retired; it sources its rows from the loop's events,
  and the root's chain is `[perm, guard]`. **Reasoning round-trip**:
  `ReasoningDelta` streams (thinking precedes speech), `Message.Reasoning`
  accumulates in both assistant branches and rides the wire back as
  `reasoning_content` (the `messages.reasoning` column lands); the CLI
  renders it verbatim, one-shot ignores it. **Usage cache fields**:
  `CacheRead`/`CacheWrite` on `Usage`, mapped from
  `prompt_tokens_details` (absent → zero); the adapter now requests the
  stream's usage chunk (`stream_options.include_usage`, wire-shape asserted)
 ; without it OpenAI and llama.cpp emit no usage at all; the CLI sums per
  turn and prints `↑P ↓C · cache R hit%` at the turn's end (pane's
  `formatTokens`). **Steering**: the turn's cancel rides the Input ctx as
  the interrupt handle (`core.WithInterrupt`/`InterruptFrom`); a line typed
  during a live turn interrupts the turn and is delivered on re-entry (one
  slot, latest wins); between turns it is served directly. Ctrl-C keeps its
  meaning: the session ends once the in-flight step unwinds. **Session
  resume**: `state.Resume(ctx, db, id)` rebuilds the session from the state
  rows in one read-only transaction (transcript in seq order, dangling calls
  kept, files rebuilt; unknown id loud); `-resume <id>` at the root, `-p`
  and `-resume` refuse at construction; the recorder upserts the files
  snapshot at each turn boundary, closing the `RecordFile` gap. **One
  extension mechanism**: `ToolMiddleware` widens from a function type to
  an interface (`ToolMiddlewareFunc` adapts; the `perm` wrap-only shape is
  unchanged), with assertion-checked `TurnObserver` and
  `GuidelineContributor` capabilities; the loop fans out `TurnStart` once
  per turn (L6) and the root collects guidelines into the system prompt
  before the policy is built. **Guard alignment**: the bound is keyed by
  tool name, cleared at every turn start (pane's retry-guard semantics),
  the limit-th failure carries pane's note verbatim (appended, never
  replacing), and the bound refusal is kept. **Turn boundaries**:
  `TurnEnd{over|fault|interrupt}` closes every turn inside the run and is
  absent on run-context end; a dead turn ctx at the pre-stream seam
  (Assemble, the Stream call) reads as an interrupt, not a fault. All 15
  existing named loop cases pass byte-for-byte; `-p` and `run-job` are
  unchanged.
- **tool/web** (roadmap deliverable 6): pane's web_search and web_fetch,
  ported. web_search: SearXNG JSON over net/http (the web-tools compose
  on loopback :8888; LOOPER_SEARXNG_URL to point elsewhere), results
  mapped to title/url/snippet (tags stripped, 300-char cap),
  maxResults 1..20 default 5, the 15s budget, "no results" loud.
  web_fetch: the guarded fetch; http(s) only, DNS refuses private and
  link-local space before any connection, every redirect hop re-guarded,
  hop cap, textual content types, declared-and-streamed 5 MiB byte cap
  with the loud marker, the 20 000-char cap with pane's elision marker,
  the 30s whole-fetch timeout, egress through the compose's tinyproxy
  (:8889; LOOPER_WEB_FETCH_PROXY, set empty = direct), and the
  unreachable-proxy fix-it voice. Extraction: trafilatura as a
  documented external (shared venv, then PATH; LOOPER_TRAFILATURA
  explicit, empty = off), degrading to pane's stdlib text pass, and
  announcing it in the content, where pane is silent. Stdlib only.
  Pane's 24 named cases plus 5 looper-side cases green against httptest
  servers; the suite is green on a bare box with the trafilatura-present
  arms skipping.
- **tool/python** (roadmap deliverable 5): pane's persistent IPython
  kernel, ported. One kernel per session; state (variables, imports, defs)
  survives across calls. Stdlib only: `os/exec` and the kernel's
  JSON-lines protocol over stdio, no third-party client; the host is
  pane's `kernel_host.py` verbatim, embedded and materialised on demand
  (pane's installed path preferred for interop, the choice logged at
  startup). Interpreter is pane's shared venv; the lazy bootstrap
  (single-flight, re-tryable, verbatim voice) is the default path's
  policy; `LOOPER_PYTHON` is the operator's explicit interpreter and the
  `NewWith` seam's contract is no bootstrap. Timeout kills the whole
  kernel and says so (pane's voice, half-up rounding); an unexpected death
  is announced on the next call once, with exit description and stderr
  tail; a deliberate restart leaves no note. Protocol state is per process
  (no stale buffer, no stderr leak); first-writer-wins delivery; EPIPE
  fails fast; `Setpgid`/`WaitDelay`/group kill. Pane's 21 named cases pass
  against a real kernel in pane's order; the suite skips cleanly on a bare
  box.
- **tool/scheduler** (roadmap deliverable 4): background jobs on the
  user's crontab, ported from pane. Two stores (global, per-workspace) with
  the event-log spine and `jN` ids never reused; crontab as the scheduling
  truth (tagged lines, surgical rewrites, foreign lines byte-identical,
  written before the store commit, drift surfaced in `list`); 5-field
  vixie validation and `once` + `at`; the `runs` container as the audit
  read. The runner is looper's own `run-job <key>` verb: flock per key,
  busy policy against llama-swap (`skip` | `force`, own-slot-loaded runs,
  unreachable fails closed), spawn under a timeout with process-group kill,
  run records and logs (newest 20), a once job consumed after its fire.
  Workers are `looper -p` with the swap endpoint passed explicitly; the
  default `-base-url` now matches the swap (8090).
- **`-p` one-shot mode** (`frontend/oneshot`): one prompt in, the response
  on stdout, faults propagate to a non-zero exit so a run record cannot
  log false success. Borrowed from roadmap deliverable 7 to give the
  scheduler its worker.
- **tool/rem** (roadmap deliverable 3): memory, ported from pane. learn
  (idempotent on scope + content md5), recall (FTS5 and trigram arms fused
  by reciprocal rank at k=60, effective strength at read, project-first
  with global fill, live-hit budget), reflect (distilled memory with its
  raw source), prune (consolidate as the checkpointed pass, remove/reduce
  by selection). Ids minted from a meta counter, never reused;
  supersession SET NULL cleared by prune in the same transaction; FTS and
  trigram rows written in code, no orphans; `AutoReflect` shipped for
  compaction to wire. `memories.source` defaults to the calling session id,
  free text allowed.
- **store/state** (SPEC_STATE): the session transcript as rows. `sessions`,
  `messages`, `tool_calls`, `usage`, `files`, `faults` in a workspace-shared
  sqlite file; a recorder on the Frontend and middleware seams lands every
  completed row inside its own short transaction, so a killed worker leaves
  its autopsy. `core.Session.ID` minted at `NewSession` and shared by the
  loop and the transcript.
- **store/**: the substrate under all of the above. lift-generated domain
  and DDL from hand-written four-tag metadata (`gen.json` with the runtime
  field; lift gained it, and portable `IN`-list batch getters, in the
  process), `sqlx` and `lazy` copied verbatim, `Open` with pragmas riding
  the DSN (`_txlock=immediate`, WAL, busy_timeout, foreign_keys on every
  pooled connection), schema version check, corruption quarantined aside,
  a generation-drift test per store. `modernc.org/sqlite` is the one
  require line.

- **tool/todo** (roadmap deliverable 2): the task-queue store: pane's
  semantics, Go over the generated substrate. Claim semantics (start
  claims, foreign complete/start refuses and names the claimer, fail
  frees); completion gated by dependencies with the blocker named, cycles
  refused with the path, blocked tasks skipped by next; move via
  minted-position events; auto-compaction past the threshold, the
  snapshot first; the stale footer; workspace isolation per working
  directory. Concurrent writers serialize at the database level:
  up-front write locks and per-connection pragmas carried in the DSN. The
  generated domain/DDL runs under the drift guard; extra.sql applied via
  `Statements() = DDL + the extra`.

- **tool/fs** (roadmap deliverable 1): named `ls`, `find`, `grep` beside
  bash/read/write/edit. Stdlib only; `.git` and binary skips; unreadable
  entries counted and named (`[skipped: N unreadable]`), an unreadable root
  stays a loud error; result caps named in the output, matched-line text
  capped at 512 bytes; bare find patterns match by name, the find -name
  reading; ctx honored at the walk boundary.
- **`Description` on the wire**: `core.Tool.Description()` +
  `ToolSpec.Description`, carried by the OpenAI adapter as
  `function.description`; descriptions take the house voice. The default
  allow-list grows to the seven-tool set.

## [0.1.0]: initial release

Shipped through the stacked-review process: each slice landed on its parent,
gated on `go test ./...` / `go vet ./...` / `gofmt -l`, merged down-stack.

- **core + loop**: the typed seams (`Provider`, `Tool`, `Frontend`,
  `ContextPolicy`, `ToolMiddleware`), wire types, the streaming-event
  vocabulary, versioned session JSON with provenance and exec records, the
  composition kernel, and the concrete turn runtime: faults (provider,
  transport, *and* a failing context assembly) surface through `Notify`,
  abort the turn, and leave the session intact; cancelled steps surface
  cleanly; a stream closed without Done or Fault fails loudly.
- **provider/openai**: the SSE streaming adapter over `net/http`: finish-
  marker gated delivery, tool-call accumulation by choice index, tool schemas
  transmitted as JSON objects, non-2xx and truncation surfaced as faults.
- **tool/bash**: `bash(1)` execution with induced-work bounds: 256 KiB
  output cap (truncation named), `WaitDelay` so background children cannot
  hold the turn, and process-group teardown on cancellation.
- **tool/file**: read/write/edit with `FileState` provenance, path-
  normalized drift checking (external modification named and refused;
  ambiguity refused, never guessed at), and a 1 MiB read cap.
- **middleware**: `policy` passthrough (day-one `ContextPolicy`); `perm`
  allow-list, deny-by-default at the boundary with attributed denials;
  `guard.Bound`; every call executes exactly once; the model's re-issuance
  of an identical failing call is counted across turns (name plus args
  digest), refused without executing at the bound, and cleared on success.
- **frontend/cli**: the stdin/stdout REPL over the `Frontend` seam: plain,
  greppable rendering; blank lines no-op; Ctrl-C cancels the turn at its next
  boundary and the session survives.
- **cmd/looper**: the composition root: `looper.New(...)` wires every seam
  in one call; configuration via flags or `LOOPER_*` env, no config files;
  `--version` reports the release.
