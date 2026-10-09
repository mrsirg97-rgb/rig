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
spawn site sends and lets the server's queue wait: the delegate (since
2.14.0 its worker waits there, not the turn), the fire, the swarm's task
worker. The one read at dispatch refuses only
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
- The model resident → send. The gate is the same one read of the
  resident set at all three sites (`gateOnce`): the free-slot read,
  the fire's per-second poll, the delegate's `no free slot` voices
  and the swarm's `WaitBusy` policy are gone with the 2.6.0
  amendment — the llama-server's queue is the wait, and rig does not
  count slots.

The gate is best-effort by construction: it races the arrival of
other requests, and an over-subscribed send queues at llama-swap
rather than failing. The gate keeps polite refusals; the server owns
the truth.

### 3. The delegate loses `Slots`

The per-session slot flocks (`delegate:<session>:<i>`) and the
row-token flocks (`delegate:model:<id>:<n>`) go with the count. A
fan-out of delegate calls in one turn is bounded by the loop's
`Parallel`, not by a slot read: the calls share one resident fact,
each passes the gate or refuses naming the holder, and the
llama-server queues what it is given. The delegate's schema is
`task`, `workspace` and `model`: `stallMs` went with the slot gate
(2.6.0) and `timeoutMs` with the clock (2.12.7: a delegate has no
clock), and the no-recursion guard stands verbatim.

### 4. The swarm drains on the count it is given

`/swarm` takes a count (2.6.0 restores it: the per-slot growth of
2.4.0 is gone). `/swarm start <count> [role=…] [model=…] [budget=…]`
starts exactly that many drain workers, at most `MaxWorkers` (16, the
induced-work cap); nothing resident, the workers' delegate loads the
session default. There is no growth tick: one router attached to the queue is
the only reader, and it hands a ready task to an idle worker on
events — the start, a task created or completed, a worker finishing —
so idle workers wait, they do not exit after empty claims. A bare
`/swarm` lists and `/swarm <n>` refuses naming the grammar (`the
count rides start`). The roster shows the swarm's model as `resident`
when it resolves per task, the `model=` override when one is named.

### 5. The drain pair is a capability, read once at wire time

`scheduler` is wired everywhere: a fire skips by itself, on any
machine. `delegate` and the swarm are wired where the pair can run:
workers on and (the session's model row remote — the gate never
consults the local swap — or the swap readable at start). The wire
happens once, `fleetWiring` at start, and an unreadable swap fails
closed; `"workers": false` wires it off naming the settings key. The
slot count is not consulted: one slot hosts the pair, and the
resident set says nothing about the wire. The menu says nothing about
what is absent; `/swarm` names the reason when it refuses. The
tool-menu budget is 15,000 characters (2.11.12), a guideline with a
15,500 wall since 2.12.3.

Since 2.14.9 settings `maxWorkers` bounds how many workers run at once,
the session's delegates and its swarm together: one counter in the
session process, held from spawn to exit. A delegate past the cap hands
back `delegate: worker #n queued · N workers run at once (settings
maxWorkers); it starts when one returns` and starts when a slot frees —
the call never refuses for the cap, and a queued worker stopped before
it starts returns its stop. A swarm worker waits before its spawn. The
cap is the operator's cache budget, not the server's `-np`: a
llama-server prompt cache holds a few workers' prefixes, and a rotation
wider than it re-reads every prompt from scratch.

### 6. What stays

Timeout, stall, budget, the ad-hoc run record, the log path, the
resumable transcript, the state-store bind, the jail and proxy, the
no-recursion marker, the reviewer verdict protocol, the run streams
and notices — verbatim. Remote rows never consult the swap (no gate,
no flock): the endpoint's own 429 retry is their backpressure
(SPEC_HOSTED). `core/` and `loop/` are byte-identical; the store
schema is unchanged.

### 7. A delegated worker's wire keeps the operator's verbs off it (2.14.1)

A worker session is a `rig -p` process: the delegate's worker, a swarm
drain worker, a scheduled fire. It runs with an allow list per tool
name, and until 2.14.1 the verbs inside a tool were all the worker's:
the menu offered `todo prune` beside `todo read`, `scheduler remove`
beside `scheduler list`, `plugin delete` beside `plugin run`, and
nothing but the tool's own switch stood between the verb and the
store. The rule the versions since the swarm have been converging on
is one sentence: **a worker does, the session decides.** The verbs
that judge or delete shared state belong to the session that owns the
state; the verbs that observe, create inside a task, or do the work
belong to whoever is doing it.

- **The registry marks them.** A tool's `registry.json` entry gains
  `operator`: the verbs the session keeps. One list per tool, written
  once — `todo` prune, accept, reject, move (the architect's, SPEC_SWARM's
  rule, now enforced); `scheduler` remove; `plugin` delete. `rem` has no
  key: memory is observation, and pruning a stale fact is a worker's job
  too (SPEC_DELEGATE, the doing set).
- **The gate is the delegate marker, not the frontend.** A scheduled
  fire is a `rig -p` session too, but it is the operator's own: it
  answers to nobody's board but the one it was given, and the hedge
  optimizer prunes and accepts on the queue it keeps. What marks a
  *delegated* worker — a worker spawned by a session's delegate, the
  swarm's drain workers beside it — is `RIG_DELEGATE`
  (`sched.DelegateEnv`), the same marker the no-recursion guard reads.
  The root gates on that marker: a delegated worker's menu drops the
  operator verbs from the `action` enum and from the verb words in the
  description and the schema, and a session without the marker — the
  interactive frontends and every scheduled fire — sends the registry's
  words byte for byte, so the wire job's artifacts do not move. Since
  2.14.7 the marker narrows the whole table: a delegated worker's
  `toolset.Carry` filters the wire's menu to exactly the names its
  `-allow` resolves to (allow-none sends none), so the model never sees
  a tool its execution path refuses; a headless run without the marker
  keeps the full menu.
- **A delegated worker cannot run them.** `policy/operator` is a
  middleware in the delegated worker's wire only: a call whose tool and
  verb are in the registry's operator list is refused before the tool
  runs, with one named refusal — `todo prune: the session's verb — a
  worker does not judge or delete the board; leave it for the session` —
  returned as the tool result (a teaching refusal, never a fault). The
  tools' switches are untouched: the worker-mode board gate
  (`tool/todo`) keeps its own voice, and the middleware is the one layer
  that reads the registry. The delegate's doing set (SPEC_DELEGATE 6) is
  the same rule at the spawn — a delegate worker never holds the tools
  at all — and the middleware is what makes the swarm's wider list
  honest.

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
- **The operator's verbs (2.14.1)**: a delegate spawn's `-allow` is the
  intersection (a session allowing everything yields the eight; one
  allowing only `bash,todo` yields `bash`; one allowing only
  `delegate` runs allow-none); a swarm spawn's allow is unchanged and a
  reviewer's still adds `verdict`; a delegated worker's `todo` menu
  lacks prune, accept, reject and move and its description does not say
  them; a headless run without the marker keeps the four on the menu
  and a `todo prune` runs through the middleware (the store changes);
  the middleware refuses each listed pair by name and passes every
  other verb, `rem prune` included; a delegated call to `todo prune`
  gets the refusal in its tool result and the store is unchanged.

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
