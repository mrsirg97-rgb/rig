# swarm

The drain-worker controller (SPEC_SWARM): supervisor-side claim/spawn/
complete loops over the session's bound queue. The command owns the
vocabulary (`command.Env.Swarm`); this package owns the goroutines and
the supervisor's in-memory truth.

- `Controller` / `New`: one drain-worker swarm. `Start(ctx, in)` begins n
  more drain workers; `StartOpts.Budget` (dollars, `swarm <n>
  budget=<dollars>`) caps the controller's claims — the spend is summed
  from each delegate result's cost (the cost column) and at the cap the
  worker stops claiming with the notice `swarm: budget reached —
  $X.XX / $Y.YY — the swarm stops claiming` (SPEC_HOSTED 5).
  more drain workers (against a running swarm it adds, so the roles mix);
  `List()` is the supervisor's read; `Stop()` cancels the context (the
  in-flight spawns die with it), waits for the drain workers, releases
  their claims through the todo store's Reap door, and clears the rows.
- One drain worker owns one identity (a minted session id) and loops:
  `todo claim` (a reviewer claims `status=review`), build the brief from
  `todo.Task`, spawn a one-shot `rig -p` through `sched.Delegate`, then
  finish the task itself — workers `complete` in worker mode, reviewers
  parse the worker's last `verdict:` line and `accept`/`reject`. Three
  consecutive empty claims end the worker; an empty claim while another
  worker is mid-task does not count.
- A dead worker's claim is released (Reap) and retried once; a second
  death fails the task (workers) or rejects it with the reason
  (reviewers). The task worker never touches the queue protocol.
- The delegate spawn passes `WaitBusy` (the GPU slots are the
  parallelism), `Observe` (the worker's stderr streams into
  `<scheduler home>/swarm/wN.stream`, the heartbeat read from it),
  `SpawnCtx` (the drain worker's context, so a stop kills the spawn),
  `Stall` 10m and `Timeout` 2h: a worker that keeps writing holds its
  slot for the full spend ceiling, a silent one is killed as hung.
- The optional `Frontend` seam is the transcript door (SPEC_SWARM 7):
  the four decision-worthy events emit one-line `SwarmNotice` notices
  (a task failed with its note, a reviewer rejected with the reason, a
  worker died and was restarted or exited, the board emptied or the
  swarm exited) and nothing else; the controller emits `SwarmStatus`
  snapshots on claim, stream bytes, complete, verdict, and exit,
  throttled to a few per second with the exit's last frame always
  landing; the emitter builds the snapshot only when the frame is due,
  so the `Counts` fold never runs per stream chunk. The snapshot is the
  roster (`List`) plus the bound queue's fold counts (`todo.Counts`).
- Pure supervisor side: stdlib plus core, models, store/todo,
  store/scheduler, the `status` throttle leaf. No command. A worker's
  model row resolves at spawn: a remote row's delegate skips the swap
  busy probe and carries the row's concurrency token bound.
