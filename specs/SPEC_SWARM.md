# rig: the swarm (the session is the architect, workers drain the queue)

The shared board (1.3.9) made the queue the place sessions talk about shared
work; this spec adds the workers that drain it. The session stays the
architect: it reads the queue, the notes and the review verdicts, and never
a diff. `/swarm start <n>` starts n drain workers; each one pulls the next
task from the queue at the session's scope and runs it through the delegate
spawn path (a jailed `rig -p` worker, the job's cwd; SPEC_SANDBOX), then submits the
result for review. The session supervises: the bare `/swarm` lists the
workers (role, model, current task, last heartbeat from the run stream,
tasks done/failed), and `/swarm stop` ends them.

**Amended by SPEC_WORKERS (2.4.0)**: the count `n` is retired — the
swarm starts one drain worker per free slot read live from the swap
and grows as slots free (decision 2's `WaitBusy` waits on that same
read); `workers.json` and its `reviewer` key are gone, and the
worker model resolves per task (the resident model, else the
session's default).

**Amended in 2.6.0 (the router)**: the count rides the command again —
`/swarm start <n>` starts n workers (1..`MaxWorkers`), stored nowhere.
One router goroutine, attached to the session's todo queue, is the only
queue reader: it claims a ready task for each idle worker, running on
events — the start, a task created or completed (the todo tool calls a
wake callback wired at the root), a worker finishing. Workers never
read the queue; each waits on the router's handoff and returns to it
when the task ends. Gone with the grow loop: the claim poll (`Poll`),
the empty-claim exit count, the "board emptied" notice (idle workers
wait; they do not exit), and the swarm's own `Stall`/`Timeout` — a
worker queued at the server writes nothing, and the silence or spend
kill would shoot it; the caller's context is the only bound
(`DelegateInput.Timeout` negative means no wrapper deadline). The
delegate's `WaitBusy` field retires with the slot read (SPEC_WORKERS
2.6.0): the spawn sends and waits on the server's queue like every
other second request. The controller gains `Opts.Delegate` (the
injectable spawn seam, default `sched.Delegate`) and an exported
`Wake` (the todo tool's callback). The notices, the retry and reject
caps, the Reap release, and the verdict protocol are unchanged.

## what it is not (named)

- **Not a scheduler.** No crontab line, no once-fire, no cron run records:
  the swarm runs while the session lives and stops on `/swarm stop` or at
  process exit. Task workers still record delegate runs (the resumable
  transcript), because a worker's autopsy is the architect's review input.
- **Not a new loop.** The drain loop is supervisor-side Go: claim, delegate,
  complete, repeat. The agent loop is untouched; the task worker is a
  one-shot `rig -p`, exactly as the delegate's is.
- **Not nesting.** A swarm worker is spawned with `RIG_DELEGATE=1` (the
  delegate's own no-recursion marker), so it cannot delegate and cannot
  start another swarm.
- **Not the review.** The reviewer is a drain worker role: it claims
  `status=review` and decides accept or reject by verdict. The architect
  still accepts or rejects whatever a worker submits; the swarm never
  auto-accepts its own work.
- **The architect's verbs, enforced (2.14.1)**: the reviewer's verdict
  read the board and the worker's stdout; the accept, the reject, the
  reorder and the prune were the architect's by convention. Since
  2.14.1 they are by rule: `todo` accept, reject, move and prune are the
  registry's operator verbs (SPEC_WORKERS 7), off a delegated worker's
  menu and refused in its wire before the tool runs — a drain worker
  submits for review and notes findings; the session decides. The
  swarm's worker allow list itself is unchanged: draining the board is
  the workers' job, and the verbs they never see are the session's.

## goals

- One command, `/swarm`: start n drain workers, list the live ones, stop.
  One file in `command/`, testable with a fake seam.
- A drain loop (supervisor-side): claim → spawn a one-shot worker through
  the delegate path → complete (workers) or take the verdict (reviewers) →
  repeat; three consecutive empty claims end the worker.
- The delegate path verbatim: the jail, the socket proxy, the worker
  command, the GPU busy rule (skipped for a remote worker row: the swap
  is never consulted, and the row's `concurrency` token bound rides
  beside the per-session slots, SPEC_HOSTED 4), the state-store bind,
  the recorded run. A `budget=<dollars>` start caps the controller's
  claims: the spend is summed from the recorded run costs, and at the
  cap the worker stops claiming with the notice (SPEC_HOSTED 5). Two
  amendments, named below: a swarm spawn waits at llama-swap for a GPU
  slot instead of refusing, and it streams the worker's stderr to the run
  stream so the supervisor sees the heartbeat.
- The supervisor's truth is in memory: each worker's role, model, current
  task, last heartbeat, and done/failed counters, rendered by the bare
  `/swarm`.
- Fail closed: a worker that dies mid-task has its claim released and the
  task retried once; a second death fails the task (workers) or rejects it
  with the reason (reviewers). No task is ever left held by a dead identity.
  A dispatch the gate refuses because the worker's model is not resident is
  not a death: the claim is released, the worker stops with a notice naming
  the holder, and the task stays pending.

## decisions

### 1. The drain worker is supervisor-side; the task worker is one-shot

`/swarm start <n>` starts n drain-worker goroutines in the session's
process; the bare count has never been the grammar (2.11.x moved the
count behind the keyword so `swarm stop` and `swarm start` parse apart).
Each drain worker owns one identity (a minted session id, stable for its
life), and loops:

1. `todo claim` — the first pending task whose dependency is done; a
   reviewer claims `status=review` (the 1.3.9 filter) instead. The claim
   is attributed to the drain worker's identity, so the queue shows the
   in-flight task and its holder, and the architect's own claims never
   collide with the swarm's.
2. `nothing to do` three times in a row ends the worker; an empty claim
   while any other drain worker is mid-task does not count (a review is
   in flight, the queue is transiently empty). Between empty claims it
   sleeps a short poll (`Poll`, default 2s), so the workers do not wake
   together and do not spin on an empty queue.
3. Otherwise it spawns a one-shot worker through the delegate path for the
   claimed task: the task brief (id, text, notes) as the prompt, the
   drain worker's model, a fresh worker session id (the resumable
   transcript), `rig -p`, sandboxed, the session's cwd and the session's
   bound project.
4. On a clean exit it finishes the task itself — the worker never touches
   the queue protocol, so the agent cannot corrupt it: a worker drain
   calls `complete` (worker mode, submits for review), a reviewer drain
   parses the worker's verdict and calls `accept` or `reject`.
5. On a dead worker it releases the claim (the Reap door, the identity's
   session is over); the task returns to the queue and the worker's own
   next claim works it again — the first death is retried once, a second
   death of the same task fails it (workers) or rejects it with a reason
   (reviewers). The retry budget is keyed by task, not per worker: the
   controller's one map counts every death of the task across the swarm,
   so a worker's death and a reviewer's death share the single retry.
6. The task's brief says the supervisor owns the board entry: the claim
   is the supervisor's, findings go in the task's note and in rem, and
   the worker does not create tasks or start/complete/fail the board's
   entries.

Rejected, named: the drain loop inside the spawned agent (the prompt would
be the loop). The supervisor then knows nothing deterministic — the current
task and the verdict would be parsed from a natural-language stream, the
claim release would guess, and the queue protocol would ride the model's
turn discipline. The spawned agent is the work; the drain loop is the
queue protocol, and Go owns protocols.

### 2. The spawn is the delegate path, with two amendments

Each task worker spawns through `sched.Delegate` with the delegate's
`DelegateInput` verbatim: the jail (the sandbox profile, fail closed), the
socket proxy, `WorkerCmd`, the state-store bind, the explicit worker
session id, the ad-hoc run record (`scheduler runs` shows each task
worker beside cron runs), the allow-list minus `delegate`, and
`RIG_DELEGATE=1`. The delegate input gains four fields, all defaulted to
today's behavior:

- `WaitBusy` (default false): with it, a busy GPU is waited on instead of
  refused — the busy check polls `busyState` on a short interval until the
  model runs or the context ends. This is the swarm's parallelism: the GPU
  slots, not the worker count. A busy-check failure still fails closed.
- `Member` (default nil): the worker's member in the session's room
  (2.11.0, replacing the `Observe` byte observer): the child heartbeats
  on the fleet pipe (fd 3, `RIG_FLEET`) and `Delegate` publishes each
  frame as that member, so the supervisor reads the heartbeat from the
  room and never from the worker's bytes.
- `SpawnCtx` (default Background): the base context the spawn timeout
  wraps, so `/swarm stop` kills the in-flight task worker's process tree
  instead of leaving it to its timeout.
- `Stall` (default 0) with the swarm's `Timeout` 2h: the silence window,
  the scheduler's stall kill at the delegate seam. A worker writing
  nothing for 10 minutes is killed as hung; one still writing keeps its
  slot for the full 2h spend ceiling, never killed at the old 30-minute
  wall. The interactive delegate stays unset (today's plain timeout)
  unless the caller sets `stallMs`.

The swarm's spawn passes the drain worker's identity as the delegate's
`Session` with `Slots: 1`, so the per-session slot flock is a no-op: one
task worker in flight per drain worker is the loop's own shape. The
fleet's `slots` gate stays the delegate's; the swarm's gate is the GPU.

Every abnormal end of a spawned worker is recorded as the run's reason,
never only as a log marker: `killed after timeout` (the spend ceiling),
`killed after stall` (the silence window), `canceled` (the spawn's base
context ended — `/swarm stop`, the session teardown, or the caller's
own cancel), and `killed by signal N` (a signal the runner did not
send — the exit code alone cannot name it). A spawned worker's process
group also carries `Pdeathsig`, so a runner that dies hard does not
leave the worker running orphaned.

### 3. The reviewer verdict is a message

Through 2.10.x the verdict was a protocol line, `verdict: accept` or
`verdict: reject <reason>`, the last line of the worker's stdout, parsed
by the drain worker. Since 2.11.0 it is a message: the reviewer worker
runs with the `verdict` tool (the one tool added to its allow list), its
call crosses the fleet pipe as `core.Verdict` published as the worker's
member, and the supervisor stamps it on the worker on the loop; the drain
worker reads it after the spawn ends. A reject's reason rides the reject
note, bounded by `MaxNoteLen`; a reject without a reason is refused by
the tool before it crosses, so the model fixes it instead of dying. A
worker that ends without a verdict is treated as a dead worker (release,
retry once; a second no-verdict rejects with `reviewer gave no verdict`).
The verdict message is the one contract between the swarm and its
reviewer worker, and the task text and notes are in the brief so the
reviewer needs no queue parse. The rejections are capped per
task: the controller counts every reject door (the verdict reject, the
no-verdict fallback, the died fallback) and a third rejection fails the
task with a note (`rejected twice — the swarm failed it`) instead of
returning it to the workers — the reject → pending → worker → review →
no-verdict cycle cannot spin forever. Rejected, named: the reviewer
worker calling `accept`/`reject` itself — it does not hold the review
claim (the drain worker does), the foreign-hold refusal would fire, and
the queue protocol would leak back into the agent.

### 4. The supervisor

`Env.Swarm` is a seam like `Steer`: the command package defines the
interface (`Start`, `List`, `Stop` and the row types), the root builds the
controller, `cmd/rig` wires it once. The controller is a new leaf package
`swarm/`: stdlib plus core, models, store/todo, store/scheduler. The root
owns every concrete type; the command owns only the vocabulary.

- **Start** begins n more drain workers: against a running swarm it adds
  (the roles mix — a worker swarm gains a reviewer mid-drain), so the
  architect can spin up a reviewer when the first task lands in review.
  It bounds `n` (1..`MaxWorkers` 16, the induced work cap), validates
  the role vocabulary, and resolves an unknown `model=` against the
  runtime models table by name. With no `model=`, a worker uses the
  fleet's `model`; a reviewer uses `workers.json`'s `reviewer` when
  configured, else the fleet's. The reply is one phrasing whether the
  swarm was empty or running: `swarm: added N agents (role X · model
  M)` (`agent` for one, `agents` for more; never `started`). The
  controller's context derives from the start command's session context,
  so a session teardown cancels the in-flight spawns with it.
- **List** renders the workers in start order in the list shape
  (SPEC_COMMANDS 13): `2 workers · 1 running`, then `  w1 [~] worker
  qwen3.8-workers · task t3 · heartbeat 2s ago · done 1 failed 0`; an
  idle worker says `task none · heartbeat —`; a finished one is `[x]`
  and says `exited`. Exited workers
  stay listed with their counters until the next Start or Stop, so the
  architect can read the swarm's summary after the drain. A worker's
  heartbeat resets on each spawn, so a restarted task shows a fresh age
  instead of the dead run's last beat.
- **Stop** cancels the swarm's context (the in-flight spawns die with it),
  waits for the drain workers, releases whatever claims they held (the
  Reap door again), and clears the rows. The reply is `swarm: stopped N
  agents`; a stop with no swarm refuses by name. Session teardown stops
  the swarm the same way (`cmd/rig`'s exit path), so the in-flight
  spawns' deaths are recorded before the process ends.
- **The run stream** (through 2.10.x): `<scheduler home>/swarm/wN.stream`,
  one file per drain worker, appended across its life with task markers;
  the heartbeat the list shows was the supervisor's read of that stream.
  Since 2.11.0 no stream file is written: the run log is the audit and
  the heartbeat is a message on the fleet pipe.

### 5. The dead claim: release via Reap

A dead task worker's claim is the drain worker's own identity, and the
identity's "session" is over — the swarm calls `todo.Reap` with that
identity in the ended set and the architect's session as the caller, so
the store's exact arm frees the claim regardless of age (the task returns
to pending; a review claim stays review, unclaimed). No new todo verb:
Reap is the existing door, and the caller is the one who watched the
worker die. The same door releases on Stop.

### 6. The one todo read

The drain worker needs the task's text and notes for the brief, and the
claim echo carries only the id. `store/todo` gains one structured read:
`Task(ctx, db, p, id, session)` returns `Task{ID, Text, Notes}` (each note
with its session, in order), the unknown id in the store's voice, read-only.
Rejected, named: parsing the rendered `Read` reply — the brief would depend
on the render's words, and the render is the model's surface, not a parser
contract. The worker-mode door (the spawned `rig -p`'s todo tool) gains
the same refusal: `Start`/`Complete`/`Fail` take the worker flag and
refuse a task the worker does not hold, with no takeover hint — the
supervisor's board entry is not the worker's to take. `Fail` also accepts
the caller's own review claim (the swarm's capped fail), and the fold
replays that arm. The swarm's review release exposed one store bug: the `release`
event replayed only for `in_progress` claims, so a released review claim's
holder came back on the next fold. The fold now applies the release to a
`review` claim too (the status stays, the holder clears), with a replay
test.

### 7. The transcript notices and the status band

AMENDED 2.11.0 (SPEC_EVT 8): the `Frontend` seam, the status emitter and
its 250 ms throttle, the controller's mutex and the per-worker stream
files are gone. The controller is a member of the session's `broadcast`
room (`SupervisorID` 0, each worker a member by its id); it publishes
the notices and the status snapshots below, the root's frontend member
subscribes once and hands each event to the current recorder, and the
loop transport keeps one pending status per sender with the latest
value, which is the throttle without a clock. Every state change is a
closure posted at `rig.PriorityFleet`; the worker goroutines wait on the
world and post their settle. The run log is the worker's bytes; no
stream file is written. The text that follows describes the events,
which did not change.

The four decision-worthy events emit one-line
`core.Notice` transcript notices with source `swarm` (`SwarmNotice`
until 2.11.0), and nothing else does — the drain
loop's ordinary claim/complete/bytes stay out of the transcript (the run
log and the bare `/swarm` are their audit).

- **A task failed (with its note)**: `swarm: t1 failed — the worker died
  twice` (the worker's second death; the reason is noted on the task),
  `swarm: t1 failed — the reviewer rejected this twice; the swarm failed
  it` (the reject cap).
- **A reviewer rejected (with the reason)**: `swarm: t1 rejected — tests
  are missing` — every reject door names its reason (the verdict's reason,
  the no-verdict fallback, the died fallback).
- **A worker died and was restarted or exited**: `swarm: w2 died — t1
  restarted` (the first death: the claim released, the task retried),
  `swarm: w2 died — t1 exited` (the second: the retry budget spent, the
  worker's run of the task is over — the task's fate, failed or rejected,
  is the next notice).
- **The board emptied /swarm exited**: `swarm: the board emptied — all
  workers exited` (the natural drain after three empty claims), `swarm:
  /swarm exited — N workers stopped` (Stop).

The controller also emits `core.SwarmStatus` snapshots on claim,
heartbeat, tool call, verdict, and exit — and only then: `receive`
publishes when a message from one of its own workers moved a field the
snapshot carries, never for a message from another publisher (2.14.7).
An idle controller says nothing, so a delegate's own frames can no
longer be erased by the supervisor's empty echoes within the same
tick; the delegate tool's Observe emits the same shape for an
interactive delegate. The throttle owns the cadence:
`Emit` takes the snapshot builder and invokes it only when a frame is
due, so a streaming worker's per-chunk emits never fold the store (the
`Counts` read runs at most four times a second, the forced frames
besides). The snapshot carries the workers (the supervisor's List) and
the bound queue's fold counts (`todo.Counts`: pending and review), and
the TUI folds the latest into the footer below the existing status
rows, separated by a short dim rule, while a swarm runs; zero rows and
no rule when nothing runs:

```
····
workers 2 · +3 ✓5 ✕1 · w2 t388 12s
reviewer 1 · ⧗1 ✓1 ✕0 · w3 t386 4m
```

- `workers <n>` / `reviewer <n>`: the role's drain-worker count; `+<p>`
  / `⧗<r>`: the bound queue's pending/review fold; `✓<d>` / `✕<f>`: the
  role's summed counters. The role labels, the `+`/`⧗` markers and the
  separators are dim, the counts text, `✓` the success slot, `✕` the
  fault slot (the theme's glyph switch carries the ascii fallback).
- The tail is the role's busiest worker — in flight first, then the
  highest done+failed, ties by id — its current task (`—` when idle) and
  heartbeat age (`12s`, `4m`, `1h`; `—` when none), dim.
- A delegate's band is its own two rows (the head and the call row,
  SPEC_TUI 3a), not the role rows: its snapshot carries every worker,
  running and queued (2.14.9 queues the call past the `maxWorkers`
  cap), and the head names the running count with the queued count when
  one queues; the role-row tail and the `+`/`⧗` fold stay the swarm's
  own.

Rejected, named: the controller calling the frontend on every heartbeat
(the band's cadence is the throttle, not the stream's); a transcript
notice per task completion (completion is the ordinary path; the notices
exist for the decisions).

## testing

Named cases, failing first. The controller tests use a real todo store and
a real scheduler store in `t.TempDir()`, a fake `Spawn` (the delegate's DI
seam) and the scheduler's scripted busy fixtures; the command tests use a
fake `Swarm` seam.

- `TestSwarmDrainsAThreeTaskQueueWithTwoWorkers`: three tasks, two drain
  workers, the fake spawn returns a clean exit per task: all three tasks
  land in review, the spawn seam saw three calls (one brief per task).
  The workers stay idle on the queue afterwards: the empty-claim exit
  left with 2.6.0's router, so an idle worker is waiting, not gone.
- `TestSwarmReviewerRejectsAndAWorkerPicksItUp`: one task, a worker and a
  reviewer: the worker submits it, the reviewer's fake worker returns
  verdict (2.11.0: the worker calls the `verdict` tool with the reason —
  the `verdict:` stdout line and its parser are gone), the task returns
  to pending with the reason as a note, and the worker drains it again.
- `TestSwarmReviewerSecondDeathRejectsWithTheReason` and the store's own
  `TestAcceptMovesReviewToDone`: the accept path lands the task done, and
  a reviewer that dies twice rejects with the reason.
- `TestSwarmDeadWorkerTaskReleasedAndHandedToAnother`: the fake spawn
  returns a dead worker on the first call for the task: the claim is
  released (the task is pending again, the release event on the log), the
  spawn seam is called a second time for the same task, and the task ends
  in review with the worker's done count at one.
- `TestSwarmSecondDeathFailsTheTask`: two dead spawns: the task is failed
  (workers) or rejected with the reason (reviewers), and the retry was
  not a third spawn.
- `TestSwarmEmptyQueueStopRepliesNoSwarm` and
  `TestSwarmHandsOutOneTaskPerIdleWorker`: an empty queue spawns nothing
  and says so; one task goes to one idle worker, never two.
- `TestSwarmListsWorkersAndStops`: two workers, the fake spawn blocks and emits a
  heartbeat line: the list shows role, model, the current task, a recent
  heartbeat, and the counters; `Stop` cancels the spawns (the fake spawn's
  context ends), the workers stop, and the rows clear.
- `TestSwarmSpawnsWhileEverySlotIsProcessing`,
  `TestSwarmRemoteRowNeverConsultsTheSwap` and
  `TestHolderRefusalReleasesAndStopsTheWorker`: there is no wait at the
  gate (2.6.0) — a fire goes when the resident set allows it, a remote
  row never reads the swap at all, and a refusal names the holder,
  releases the claim and stops that worker rather than spinning.
- `TestSwarmModelDefaultsToResidentAndOverrideWins`: the resident model
  is the default on both roles, `model=` overrides it, and an unknown
  `model=` is refused by name (there is no configured reviewer model to
  fall back to — the file that carried one retired in 2.4.0).
- `TestSwarmStartRefusalsByName` and `TestSwarmCountBounds`: a count
  outside 1..`MaxWorkers`, an unknown role and an unknown model each
  refuse by name; the command adds that the count rides `start`
  (`TestSwarmCountRidesStart`).
- `TestWorkerModeRefusesUnclaimedStartCompleteFail` and
  `TestWorkerModeRefusesForeignStartCompleteFail`: the worker-mode store
  doors — `complete`/`fail` (and the store's `start`) refuse a task the worker does not
  hold, whether unclaimed or held by another — the voice names no
  takeover, and the interactive auto-start still lands solo
  (`TestSoloCompleteOnOwnPendingAutoStartsAndLandsDone`).
- `TestSwarmReviewerNoVerdictCappedAtTwoRejectsThenFails`: a reviewer
  that never returns a verdict: the task is rejected twice, the third
  rejection fails it with the note — the no-verdict cycle cannot spin.
- `TestSwarmRetriesAreKeyedByTaskAcrossWorkers`: a worker's death and a
  reviewer's death share the one retry budget — the reviewer's death is
  not retried, and the task fails with the note after the reject cap.
- `TestTodoCountsFromTheFold` and `TestTodoCountsEmptyQueue`: the counts
  the roster renders come from the fold, read-only, and an empty queue
  counts zero rather than refusing; the notes door answers in the store's
  own voice (`TestNotesOnAMissingTaskRefusesInTheStoreVoice`).
- `TestSwarmNoticesTaskFailed` / `TestSwarmNoticesReviewerRejected` /
  `TestSwarmNoticesStop`: each decision-worthy notice with the fake
  spawn — the failed task names its note, the reject names its reason,
  the stop names itself; nothing else is notified, and every one of them
  is a `core.Notice` with source `swarm` published to the room (2.11.0),
  not a second event type.
- `TestSwarmStatusFramesClaimHeartbeatAndFinish`: the controller
  publishes a `SwarmStatus` on claim, heartbeat, complete, verdict and
  exit. There is no throttle and no 250 ms clock: `SwarmStatus` is a
  `core.Snapshot`, and the transport keeps one pending per sender and
  replaces it before it runs — the newest truth is the whole truth
  (SPEC_EVT 8).
- `TestAForeignMessageNeverPublishesTheSwarm` (2.14.7): a delegate
  status and a foreign tool start arriving at an idle controller
  produce no swarm publish; a heartbeat from its own worker still
  does. The swarm speaks only about itself.
- `TestADelegatePutsNoDeadlineOnTheSpawnSoALongWorkerReturns`,
  `TestTheTurnsContextDoesNotRuleTheWorkerButTheSessionsDoes`,
  `TestTheIdleInterruptKillsTheWorkersProcessTree` and
  `TestDelegateSecondFanOutOnASingleSlotSendsAndWaits` (tool/delegate):
  the delegate carries no clock (2.12.7), and since 2.14.0 the bound is
  the session or the idle interrupt, never the turn;
  the byte observer (`Observe`) went to the room's heartbeat in 2.11.0.
  The remaining default paths (skip, nil observer,
  background context) are unchanged.
- The command's `TestSwarm…` cases: the parse (`swarm start 3`,
  `role=`, `model=`, `budget=`), that the count rides the keyword
  (`TestSwarmCountRidesStart`), the bare list's exact lines,
  `swarm stop`, the usage refusals, and the no-seam pair
  (`TestSwarmNoSeamListsEmptyAndStopRefuses`). There is no no-fleet
  refusal in the command: the wiring refuses upstream with its own why
  (SPEC_WORKERS 4).

The suite is green on a box with no model loaded: every case is a fake
spawn, a scripted busy fixture, or a real store in a temp dir.

## freeze

- `core/`: the event vocabulary gains two types (`SwarmNotice`,
  `SwarmStatus`) as pure additions — nothing else; `loop/`: zero diff.
  2.11.0 folded `SwarmNotice` into `Notice` (source `swarm`), one event
  for one idea.
  The loop never learns the swarm.
- The todo store gains the two structured reads (`Task`, `Counts`) and
  nothing else: the claim filter, the review gate, and the release doors
  are the 1.3.9 surface.
- The delegate path gains the three defaulted fields, the optional
  frontend seam, and no new process topology: the jail, the proxy, the
  record, the worker command, the state-store bind are verbatim.
- `command/` gains one file and one registration line (the standard set's
  thirteenth command); the TUI's `Sub()` door is the swarm's vocabulary.
- The TUI's live region protocol (`live.go`) is unchanged: the band is a
  status-string extension, and the region already handles its changing
  height.

## scope

- `specs/SPEC_SWARM.md` (this file).
- `swarm/`: the controller (Start/List/Stop, the drain loop, the brief, the
  verdict protocol, the run stream, the notices, the status) and its
  `PACKAGE.md`.
- `store/todo`: `Task` and `Counts` (the structured reads) and their tests.
- `store/scheduler`: the delegate input's `WaitBusy`/`Observe`/`SpawnCtx`.
- `command`: the `Swarm` seam, the `/swarm` command, the registration.
- `config`: `workers.json` gains the optional `reviewer` model.
- `cmd/rig`: the wiring (the controller, the env seam, the frontend door).
- `tool/delegate`: the `Notify` seam, the status `Observe`.
- `frontend/tui`: the band, the notice line, their tests.
- `specs/SPEC_COMMANDS.md`, `specs/SPEC_CONFIG.md`, `specs/SPEC_STATE.md`,
  `specs/SPEC_DELEGATE.md`, `specs/SPEC_TUI.md`: the amendments above.
- `docs/USAGE.md`, `docs/SETUP.md`, `docs/TUI_DESIGN.md`, `CHANGELOG.md`:
  the command, the fleet key, the band, the release notes.
