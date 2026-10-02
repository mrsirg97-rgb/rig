# rig: the workers (the fleet is the resident model)

Every slot count rig kept — `workers.json`'s `slots`, `models.json`'s
`concurrency`, the delegate's `Slots`, the swarm's `count`, `busy`'s
`force` — restated one fact that was never rig's to store: the resident
server's `-np`, which llama-swap owns and exposes live (`GET
/upstream/<model>/slots`: a bare JSON array, per slot `is_processing`).
A slot is held per request, so an idle session holds none. The evidence
was in the stores: every delegate refusal read `the GPU is held by
<model>` — a worker asked for the fleet model while another was
resident. This spec retires the counts and the fleet file: a worker
runs on the resident model, in a free slot read from `/slots` at claim
time. Time and money stay; counts go.

## goals

- One model resolution, one gate, three spawn sites: the delegate (a
  turn's sub-worker), the fire (`run-job`), the swarm's task worker.
  All read the swap at claim time; nothing caches a count.
- `models.json` loses `concurrency`; delegate loses `Slots`; swarm
  loses `count`; busy loses `force`; `workers.json` goes away. The
  retired file and the retired key are read, ignored, and named once
  at start.
- Timeout, stall, and budget stay: they bound a fire, not the fleet.

**Amended in 2.6.0**: the slot read goes. A second request queues at
the llama-server itself — the evidence is the queue on a one-slot
box: a live session's turn holds the slot while it streams, and a
delegate or a scheduled fire still runs the moment it is sent. Every
spawn site sends and waits on the server's queue: the delegate, the
fire, the swarm's task worker. The one read at dispatch refuses only
a model that is not resident, naming the holder; `this turn holds
the only one` is gone. The gate shapes collapse to one: nothing
resident, another resident (refuse), own resident (send and wait).
The wiring follows: `delegate` and the swarm are wired wherever the
worker tools are on and the swap is readable at start — one slot
hosts the pair, an unreadable swap wires them off, and a remote
model row never consults the swap. The stall kill retires with the
slot gate: a worker that is only queued writes nothing, so silence
no longer implies a hang, and the kill would shoot healthy workers.
The delegate's `Stall` input and the delegate tool's `stallMs` are
gone; the scheduler tool's `busy` and `stall` keys are gone (the
fire waits on nothing and is not shot for silence); the store keeps
the columns and replays historical rows as written. The timeouts
stay — they are the spend ceilings, not liveness guesses.

## decisions

### 1. The model resolves at claim time

A worker's model is, in order: the named one (the tool arg, the job
row, the swarm's `model=`), else the resident model resolved to the
models-table row whose id is the resident id or one of its llama-swap
aliases (the swap's alias map already carries them), else the
session's own default — the resolved `RIG_MODEL` > `settings.json`
`model` chain, the same default a session uses. Resolution happens
inside `sched.Delegate` (it holds the fetch seam) so all three sites
share it; the runner's unnamed job rows resolve the same way at fire
time. The gate keeps the canonical id; the row id rides the argv's
`-model`, the run record, and the fire log — the worker process
resolves rows by the id it is handed, so an unresolvable id burned a
run row for a spawn that died in milliseconds. A resident with no row
never spawns: the fire records a skip naming it and the known rows,
the delegate refuses the same way. An unreachable swap is not that
case: the delegate falls to the session's default as it always did (a
remote-row worker never needed the swap), the fire skips as a failed
check.

The retired surfaces are read and named, not parsed:

- `workers.json`, if present, is read and ignored whole, named once at
  start: `workers.json retired: the fleet is the resident model`. Its
  content is never interpreted; deleting the file silences the line.
- `models.json`'s `concurrency` key is read per row, ignored, and
  named once at start the same way. `RIG_MODEL_CONCURRENCY` goes with
  the field.
- The legacy `defaultJobModel` key in `settings.json` is named once at
  start (`defaultJobModel moved to model — the fleet is the resident
  model; delete the key`) and ignored: nothing mints, nothing
  migrates.

### 2. The gate: one shape, three sites

At claim time, against the resolved model:

- A failed check fails closed, naming the check (uncertain GPU state
  never spawns).
- Nothing resident → proceed. The worker's first request loads the
  model; that is the only swap ask, and it is safe because it evicts
  nothing. A job that names a model asks for a swap only here.
- Another model resident → refuse (skip, for a fire) naming the
  holder. Eviction is the operator's act; rig never waits for one and
  never performs one. `busy: force` is refused at create/update
  (`force is retired: eviction is the operator's act`); historical
  rows replay as the one policy.
- The model resident → the free-slot read (`GET /upstream/<model>/
  slots`, `is_processing: false` per slot; a bare array, the
  llama-server default endpoint):
  - **A fire waits.** The fire polls the read every second up to its
    own timeout; a free slot spawns the worker, an expired timeout
    skips naming the holder and the wait.
  - **A delegate inside a turn refuses.** One read, no wait: the turn
    is interactive and the refusal is actionable now. On a one-slot
    model the voice is `delegate: no free slot; this turn holds the
    only one`; on more, `delegate: no free slot (all N slots are
    processing)`.
  - **The swarm's spawn waits** like a fire (its `WaitBusy` policy,
    the 2h spend ceiling). The swarm's own count is the slot read, so
    the wait is a backstop, not the shape.

The read is best-effort by construction: it races the worker's first
request, and an over-subscribed spawn queues at llama-swap rather than
failing. The gate keeps polite parallelism; the server owns the truth.

### 3. The delegate loses `Slots`

The per-session slot flocks (`delegate:<session>:<i>`) and the
row-token flocks (`delegate:model:<id>:<n>`) go with the count. A
fan-out of delegate calls in one turn is bounded by the claim-time
read: each call refuses or proceeds on its own read, and the
one-slot-model refusal is the fan-out bound. The delegate's
description names the resident model as the default and keeps
workspace, timeout, stall, and the no-recursion guard verbatim.

### 4. The swarm drains with the free slots

`/swarm` takes no count. Start reads the slots and begins one drain
worker per free slot, at least one, at most `MaxWorkers` (16, the
induced-work cap); nothing resident begins one, whose delegate loads
the session's default. A tick on the poll loop re-reads the slots and
starts one more worker when the free-slot read exceeds the live
workers and tasks remain — the swarm grows as slots free (the
operator's requests releasing theirs) and never past the cap. A
worker exits after three empty claims as before; growth never
respawns into an empty queue. `/swarm start [role=…] [model=…]
[budget=…]` is the start gesture (the bare `/swarm` still lists) and
`/swarm <n>` refuses naming the retirement (`a count is not taken:
the free slots are the count`). The roster shows the swarm's model
as `resident` when it resolves per task, the `model=` override when
one is named.

### 5. The drain pair is a capability, read once at wire time

`scheduler` is wired everywhere: a fire waits and skips by itself, on
any machine. `delegate` and the swarm are wired only where a second
request can run: the session's model row is remote (the gate never
consults the local swap), or the resident server reports more than
one slot — one live read of the same `/upstream/<model>/slots` the
gate reads, made once at wire time, nothing stored. Nothing resident
at start wires the pair on: the model that loads is the session's
default, and the claim-time gate already refuses on one slot — the
read cannot know better than the gate. Only an unreadable swap, one
resident slot, or `"workers": false` wires it off: the wire happens
once, at start, an unreadable swap fails closed, and nothing turns
the pair on where the slots are not (`"workers": true`, or absent,
still waits for the read). The menu says nothing about what is
absent; `/swarm` names the reason when it refuses. The tool-menu
budget returns to 14000: the recorded golden is a one-slot wire.

### 6. What stays

Timeout, stall, budget, the ad-hoc run record, the log path, the
resumable transcript, the state-store bind, the jail and proxy, the
no-recursion marker, the reviewer verdict protocol, the run streams
and notices — verbatim. Remote rows never consult the swap (no gate,
no flock): the endpoint's own 429 retry is their backpressure
(SPEC_HOSTED). `core/` and `loop/` are byte-identical; the store
schema is unchanged.

## testing

Failing first, against a fake `/slots` (the scripted fetch gains
`/upstream/<model>/slots` fixtures beside `/v1/models` and
`/running`):

- **The delegate**: a one-slot model with its only slot processing
  refuses with the pinned voice and spawns nothing; a free slot
  spawns; an unnamed model spawns the resident model resolved to its
  row (the alias case fires the row id, a resident id that is itself
  a row is unchanged), the session's default when nothing is resident,
  and a resident with no row refuses naming it and the known rows; a
  named model while another is resident refuses naming the holder; a
  failed check fails closed; the flocks are gone (two concurrent
  delegates on a two-slot model both spawn).
- **The fire**: waits for a free slot up to its timeout (the fixture
  frees one mid-wait and the worker spawns), skips naming the holder
  and the wait when it expires, skips immediately when another model
  is resident, and `create`/`update` refuse `busy: force`.
- **The swarm**: starts one worker per free slot (two free → two
  workers), grows one when the read exceeds the live workers with
  tasks remaining, never past the cap, never into an empty queue;
  `/swarm 3` refuses; the roster shows `resident`.
- **The config**: `workers.json` present is named once and ignored
  (a model row it names need not exist); `models.json`
  `concurrency` is named once and ignored; the allow default always
  carries the worker tools; the `defaultJobModel` legacy key is named
  and ignored.

## scope

- `specs/SPEC_WORKERS.md` (this file); `SPEC_DELEGATE`,
  `SPEC_HOSTED`, `SPEC_SWARM`, `SPEC_CONFIG` amended to point here.
- `models`: `Concurrency` and its env overlay go.
- `config`: the workers retirement, the modelsfile key, the notices.
- `store/scheduler`: `busy.go` becomes the resident set plus the slot
  read and the gate; `delegate.go` loses `Slots`/`Concurrency`, gains
  `DefaultModel` and the `Models` seam; `runner.go` loses the force
  branch, gains the wait.
- `tool/delegate`, `tool/scheduler`: descriptions, schema, the
  retired options.
- `swarm`: the count in, the growth loop, the per-task resolution.
- `command`, `cmd/rig`, `frontend/tui`, `frontend/web`: the wiring,
  the refusals, the startup line.
- `docs/USAGE.md`, `docs/SETUP.md`, `CHANGELOG.md`; version 2.4.0.
