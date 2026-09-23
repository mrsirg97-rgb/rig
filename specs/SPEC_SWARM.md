# rig: the swarm (the session is the architect, workers drain the queue)

The shared board (1.3.9) made the queue the place sessions talk about shared
work; this spec adds the workers that drain it. The session stays the
architect: it reads the queue, the notes and the review verdicts, and never
a diff. `/swarm <n>` starts n drain workers; each one pulls the next task
from the session's bound queue and runs it through the delegate spawn path
(a sandboxed `rig -p` worker, the session's binding), then submits the
result for review. The session supervises: the bare `/swarm` lists the
workers (role, model, current task, last heartbeat from the run stream,
tasks done/failed), and `/swarm stop` ends them.

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

## goals

- One command, `/swarm`: start n drain workers, list the live ones, stop.
  One file in `command/`, testable with a fake seam.
- A drain loop (supervisor-side): claim → spawn a one-shot worker through
  the delegate path → complete (workers) or take the verdict (reviewers) →
  repeat; three consecutive empty claims end the worker.
- The delegate path verbatim: the jail, the socket proxy, the worker
  command, the GPU busy rule, the state-store bind, the recorded run. Two
  amendments, named below: a swarm spawn waits at llama-swap for a GPU
  slot instead of refusing, and it streams the worker's stderr to the run
  stream so the supervisor sees the heartbeat.
- The supervisor's truth is in memory: each worker's role, model, current
  task, last heartbeat, and done/failed counters, rendered by the bare
  `/swarm`.
- Fail closed: a worker that dies mid-task has its claim released and the
  task retried once; a second death fails the task (workers) or rejects it
  with the reason (reviewers). No task is ever left held by a dead identity.

## decisions

### 1. The drain worker is supervisor-side; the task worker is one-shot

`/swarm <n>` starts n drain-worker goroutines in the session's process.
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
`RIG_DELEGATE=1`. The delegate input gains three fields, all defaulted to
today's behavior:

- `WaitBusy` (default false): with it, a busy GPU is waited on instead of
  refused — the busy check polls `busyState` on a short interval until the
  model runs or the context ends. This is the swarm's parallelism: the GPU
  slots, not the worker count. A busy-check failure still fails closed.
- `Observe` (default nil): the spawn's byte observer, so the swarm streams
  the worker's stderr (the oneshot liveness stream: `rig: heartbeat`, tool
  start/end) into the worker's run stream and reads the heartbeat from it.
  The delegate's interactive calls stay unobserved.
- `SpawnCtx` (default Background): the base context the spawn timeout
  wraps, so `/swarm stop` kills the in-flight task worker's process tree
  instead of leaving it to its timeout.

The swarm's spawn passes the drain worker's identity as the delegate's
`Session` with `Slots: 1`, so the per-session slot flock is a no-op: one
task worker in flight per drain worker is the loop's own shape. The
fleet's `slots` gate stays the delegate's; the swarm's gate is the GPU.

### 3. The reviewer verdict is a protocol line

A reviewer drain worker's brief ends with a directive naming the verdict
shape: the worker's last line must be `verdict: accept`, or
`verdict: reject <reason>` (the reason rides the reject note, bounded by
`MaxNoteLen`). The drain worker takes the last line with the `verdict:`
prefix; a worker that returns no verdict is treated as a dead worker
(release, retry once; a second no-verdict rejects with
`reviewer gave no verdict`). The verdict line is the one contract between
the swarm and its reviewer worker, and the task text and notes are in the
brief so the reviewer needs no queue parse. The rejections are capped per
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
  configured, else the fleet's.
- **List** renders the workers in start order: `w1 worker qwen3.8-workers ·
  task t3 · heartbeat 2s ago · done 1 failed 0`; an idle worker says
  `task none · heartbeat —`; a finished one says `exited`. Exited workers
  stay listed with their counters until the next Start or Stop, so the
  architect can read the swarm's summary after the drain.
- **Stop** cancels the swarm's context (the in-flight spawns die with it),
  waits for the drain workers, releases whatever claims they held (the
  Reap door again), and clears the rows. The reply is `swarm: stopped N
  workers`; a stop with no swarm refuses by name.
- **The run stream**: `<scheduler home>/swarm/wN.stream`, one file per
  drain worker, appended across its life with task markers; the heartbeat
  the list shows is the supervisor's in-memory read of that stream. The
  stream file is the audit; the heartbeat is the liveness.

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

## testing

Named cases, failing first. The controller tests use a real todo store and
a real scheduler store in `t.TempDir()`, a fake `Spawn` (the delegate's DI
seam) and the scheduler's scripted busy fixtures; the command tests use a
fake `Swarm` seam.

- `TestSwarmDrainsAThreeTaskQueueWithTwoWorkers`: three tasks, two drain
  workers, the fake spawn returns a clean exit per task: all three tasks
  land in review, the spawn seam saw three calls (one brief per task),
  and both workers exited (each after three empty claims).
- `TestSwarmReviewerRejectsAndAWorkerPicksItUp`: one task, a worker and a
  reviewer: the worker submits it, the reviewer's fake worker returns
  `verdict: reject <reason>` (the task returns to pending with the reason
  as a note), and the worker drains it again (in review, the rejection
  picked up).
- `TestSwarmReviewerAccepts`: the accept verdict lands the task done.
- `TestSwarmDeadWorkerClaimReleasedAndRestartedOnce`: the fake spawn
  returns a dead worker on the first call for the task: the claim is
  released (the task is pending again, the release event on the log), the
  spawn seam is called a second time for the same task, and the task ends
  in review with the worker's done count at one.
- `TestSwarmSecondDeathFailsTheTask`: two dead spawns: the task is failed
  (workers) or rejected with the reason (reviewers), and the retry was
  not a third spawn.
- `TestSwarmExitsAfterThreeEmptyClaims`: an empty queue: no spawn, the
  worker exits, and the `nothing to do` claims are the only store events.
- `TestSwarmListsAndStops`: two workers, the fake spawn blocks and emits a
  heartbeat line: the list shows role, model, the current task, a recent
  heartbeat, and the counters; `Stop` cancels the spawns (the fake spawn's
  context ends), the workers stop, and the rows clear.
- `TestSwarmBusyWaitsAtLlamaSwap`: the busy fixture reports another model
  resident, then run: the swarm's spawn waits (the fetch is called more
  than once) and runs once the GPU frees; a busy-check failure still fails
  closed.
- `TestSwarmReviewerModelDefault`: the configured `reviewer` model is the
  reviewer's default, `model=` overrides it, and an unknown `model=` is
  refused by name.
- `TestSwarmStartRefusals`: a second start, a count outside 1..16, an
  unknown role, and an unknown model each refuse by name.
- `TestWorkerModeRefusesStartCompleteFailOnUnheld`: the worker-mode store
  doors — `start`/`complete`/`fail` refuse a task the worker does not
  hold (pending and foreign), the voice names no takeover, the
  supervisor's claim is neither released nor failed, and the interactive
  auto-start still lands solo.
- `TestSwarmReviewerNoVerdictCappedAtTwoRejectsThenFails`: a reviewer
  that never returns a verdict: the task is rejected twice, the third
  rejection fails it with the note — the no-verdict cycle cannot spin.
- `TestSwarmRetriesAreKeyedByTaskAcrossWorkers`: a worker's death and a
  reviewer's death share the one retry budget — the reviewer's death is
  not retried, and the task fails with the note after the reject cap.
- `TestTodoTaskRead`: `Task` returns the text and the notes in order with
  their sessions; an unknown id uses the store's voice; the read is
  read-only.
- `TestDelegateBusyWait` / `TestDelegateObserve` / `TestDelegateSpawnCtx`
  (store/scheduler): the wait-policy, the observer, and the spawn context
  each pinned at the delegate seam; the default paths (skip, nil observer,
  background context) are unchanged.
- The command's `TestSwarm...` cases: the parse (`/swarm 3`, `role=`,
  `model=`), the bare list's exact lines, `/swarm stop`, the usage
  refusals, and the no-fleet voice (`swarm: no workers configured (…)`).

The suite is green on a box with no model loaded: every case is a fake
spawn, a scripted busy fixture, or a real store in a temp dir.

## freeze

- `core/` and `loop/`: zero diff. The loop never learns the swarm.
- The todo store gains one read (`Task`) and nothing else: the claim
  filter, the review gate, and the release doors are the 1.3.9 surface.
- The delegate path gains three defaulted fields and no new process
  topology: the jail, the proxy, the record, the worker command, the
  state-store bind are verbatim.
- `command/` gains one file and one registration line (the standard set's
  thirteenth command); the TUI's `Sub()` door is the swarm's vocabulary.

## scope

- `specs/SPEC_SWARM.md` (this file).
- `swarm/`: the controller (Start/List/Stop, the drain loop, the brief, the
  verdict protocol, the run stream) and its `PACKAGE.md`.
- `store/todo`: `Task` (the structured read) and its test.
- `store/scheduler`: the delegate input's `WaitBusy`/`Observe`/`SpawnCtx`.
- `command`: the `Swarm` seam, the `/swarm` command, the registration.
- `config`: `workers.json` gains the optional `reviewer` model.
- `cmd/rig`: the wiring (the controller, the env seam).
- `specs/SPEC_COMMANDS.md`, `specs/SPEC_CONFIG.md`, `specs/SPEC_STATE.md`,
  `specs/SPEC_DELEGATE.md`: the amendments above.
- `docs/USAGE.md`, `docs/SETUP.md`, `CHANGELOG.md`: the command, the fleet
  key, the release notes.
