# swarm

The drain-worker controller (SPEC_SWARM): a router that owns the queue
protocol and the workers that run the tasks. The command owns the
vocabulary (`command.Env.Swarm`); this package owns the worker
goroutines and the supervisor's in-memory truth. Since 2.11.0 that
truth lives on the event loop and speaks in a room: every state change
is a closure posted at `rig.PriorityFleet`, there is no mutex, and
everything the supervisor says is a `broadcast` message.

- `Controller` / `New(Opts)`: one drain-worker swarm; `Opts.Engine` and
  `Opts.Room` are constructor arguments (the controller panics without
  them, as a bound does). The controller is the room's supervisor member
  (`SupervisorID` 0); each worker is a member by its id. `Start(ctx,
  in)` begins `StartOpts.Count` workers (1..`MaxWorkers` 16, the induced
  work cap); the architect is the session the command threads — the
  swarm works its bound queue, and the supervisor's doors attribute to
  it. Against a running swarm a start adds (the roles mix), and the
  reply is one phrasing either way: `swarm: added N agents (role X ·
  model M)`. `StartOpts.Budget` (dollars) caps the router's claims — the
  spend is summed from each delegate result's cost and at the cap the
  router stops claiming with the notice `swarm: budget reached — $X.XX /
  $Y.YY — the swarm stops claiming`. `List()` reads a snapshot the loop
  refreshes after every change; `Stop()` cancels the context (the
  in-flight spawns die with it), waits for the workers, releases their
  claims through the todo store's Reap door, removes the members, and
  clears the rows — the reply is `swarm: stopped N agents`.
- The router is the only queue reader and it is a posted closure:
  `Wake` (the start, the todo tool's callback, a worker finishing) posts
  one dispatch if none is pending. Each pass claims a ready task (a
  reviewer claims `status=review`) for each idle worker, hands it over,
  and stops when every idle worker has been tried. Workers never read
  the queue; each waits on the router's handoff and returns to it when
  the task ends.
- One worker owns one identity (a minted session id, stable for its
  life) and one goroutine, because the spawn waits on the world: build
  the brief from `todo.Task`, spawn a one-shot `rig -p` through the
  delegate seam, finish the task itself (workers `complete` in worker
  mode, reviewers take the `core.Verdict` the worker's `verdict` tool
  sent through the pipe and `accept`/`reject`), then post the settle to
  the loop and wait for it. A reviewer's allow list gains `verdict`.
  Each worker is a room member by its id and the spawn carries that
  member (`DelegateInput.Member`): the child heartbeats on the fleet
  pipe and `Delegate` publishes each frame as the worker, so the
  supervisor's handler on the loop stamps the worker and emits a status
  without reading a byte. No stream file is written: the run log is the
  worker's bytes.
- `Opts.Delegate` is the spawn seam (default `sched.Delegate`): the
  tests drive the controller with a fake; the wiring passes nothing.
  The spawn carries no stall and no timeout; the worker's context (the
  swarm's) is the only bound; every abnormal spawn end is recorded on
  the run (`canceled` / `killed by signal N`).
- A dead worker's claim is released (Reap) and retried once, keyed by
  task across the swarm; a second death fails the task (workers) or
  rejects it with the reason (reviewers). The reject doors are capped
  per task: the third rejection fails it with a note instead of
  returning it to the workers. A dispatch refused because the worker's
  model is not resident (`sched.ErrNotResident`) releases the claim,
  stops that worker with a notice naming the holder, and fails nothing.
- What the supervisor says, it publishes to the room as `Notice` with
  source `swarm`, through one `say`: the four decision-worthy lines (a
  task failed with its note, a reviewer rejected with the reason, a
  worker died and was restarted or exited, the swarm exited) and the
  error lines that were stderr (`loud`, skipped once the worker's
  context is gone); beside them `SwarmStatus` snapshots on claim,
  heartbeat, finish and stop. A status is a `core.Snapshot`, so the loop transport
  keeps one pending per sender with the latest value: a streaming
  worker's forty heartbeats are a handful of frames, no clock. The root
  is a member too: it subscribes once and hands each event to the
  current recorder, so a session swap routes to the new one and a
  panicking frontend is recovered into a stderr line.
- Pure supervisor side: stdlib plus core, evt, broadcast, models,
  store/todo, store/scheduler, the kernel's priorities. No command. The
  worker model resolves at spawn time (the resident model, else the
  session's default — the roster shows `resident` unless `model=` names
  one), and the spawn sends and waits on the server's queue: one slot
  hosts the pair. A remote row's delegate skips the swap gate entirely.
