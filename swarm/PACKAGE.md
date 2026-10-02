# swarm

The drain-worker controller (SPEC_SWARM): a router that owns the queue
protocol and the workers that run the tasks. The command owns the
vocabulary (`command.Env.Swarm`); this package owns the goroutines and
the supervisor's in-memory truth.

- `Controller` / `New`: one drain-worker swarm. `Start(ctx, in)` begins
  `StartOpts.Count` workers (1..`MaxWorkers` 16, the induced work cap);
  the architect is the session the command threads — the swarm works its
  bound queue, and the supervisor's doors attribute to it. Against a
  running swarm a start adds (the roles mix), and the reply is one
  phrasing either way: `swarm: added N agents (role X · model M)`. The
  controller's context derives from the start command's session context,
  so a session teardown cancels the in-flight spawns with it.
  `StartOpts.Budget` (dollars, `swarm start <n> budget=<dollars>`) caps
  the router's claims — the spend is summed from each delegate result's
  cost (the cost column) and at the cap the router stops claiming with
  the notice `swarm: budget reached — $X.XX / $Y.YY — the swarm stops
  claiming` (SPEC_HOSTED 5). `maxVerdictReason` caps the reviewer's
  reject reason at the store's note bound, so a long verdict cannot fail
  the review protocol. `List()` is the supervisor's read; `Stop()`
  cancels the context (the in-flight spawns die with it), waits for the
  workers, releases their claims through the todo store's Reap door, and
  clears the rows — the reply is `swarm: stopped N agents`.
- The router is the only queue reader. It runs on events: the start, a
  task created or completed (`Wake` — the todo tool's callback, wired at
  the root when the pair is on), a worker finishing. Each pass claims a
  ready task (a reviewer claims `status=review`) for each idle worker,
  hands it over, and stops when every idle worker has been tried. Workers
  never read the queue; each waits on the router's handoff and returns to
  it when the task ends.
- One worker owns one identity (a minted session id, stable for its
  life): build the brief from `todo.Task`, spawn a one-shot `rig -p`
  through the delegate seam, then finish the task itself — workers
  `complete` in worker mode, reviewers parse the worker's last
  `verdict:` line and `accept`/`reject`. The task worker never touches
  the queue protocol.
- `Opts.Delegate` is the spawn seam (default `sched.Delegate`): the tests
  drive the controller with a fake; the wiring passes nothing. The spawn
  carries no stall and no timeout — a worker queued at the server writes
  nothing, and the silence or spend kill would shoot it (SPEC_WORKERS
  2.6.0); the worker's context (the swarm's) is the only bound, and
  `Observe` streams the worker's stderr into
  `<scheduler home>/swarm/wN.stream`, the heartbeat read from it. The
  heartbeat resets on each spawn, so a restarted task shows a fresh age
  instead of the dead run's last beat; every abnormal spawn end is
  recorded on the run (`canceled` / `killed by signal N`).
- A dead worker's claim is released (Reap) and retried once, keyed by
  task across the swarm; a second death fails the task (workers) or
  rejects it with the reason (reviewers). The reject doors are capped
  per task: the third rejection fails it with a note instead of
  returning it to the workers.
- A dispatch refused because the worker's model is not resident is not
  a death: the gate's refusal (`sched.ErrNotResident`) releases the
  claim, stops that worker with a notice naming the holder, and fails
  nothing — the task stays pending for the resident fleet or for a
  later start on the right model.
- The optional `Frontend` seam is the transcript door (SPEC_SWARM 7),
  a resolver read on every notify (the root wires it once as
  `func() core.Frontend { return r.rec }`, so the recorder can appear
  after wiring and a session swap routes to the current one; a
  panicking frontend is recovered into a stderr line, the worker keeps
  working). The four decision-worthy events emit one-line `SwarmNotice`
  notices (a task failed with its note, a reviewer rejected with the
  reason, a worker died and was restarted or exited, the swarm exited)
  and nothing else; the controller emits `SwarmStatus` snapshots on
  claim, stream bytes, and finish, throttled to a few per second with
  the finish's frame always landing (Stop forces an empty-roster frame);
  the emitter builds the snapshot only when the frame is due, so the
  `Counts` fold never runs per stream chunk. The snapshot is the roster
  (`List`) plus the bound queue's fold counts (`todo.Counts`).
- Pure supervisor side: stdlib plus core, models, store/todo,
  store/scheduler, the `status` throttle leaf. No command. The worker
  model resolves at spawn time (the resident model, else the session's
  default — the roster shows `resident` unless `model=` names one), and
  the spawn sends and waits on the server's queue: one slot hosts the
  pair. A remote row's delegate skips the swap gate entirely.
