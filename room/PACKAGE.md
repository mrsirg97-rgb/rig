# room

## What it is

The machine room (SPEC_ROOM): every rig instance on the machine is a
member of one room over unix sockets, and no process is the center. The
ordering primitive is `evt`'s `broadcast`; this package holds only what
rig adds: the member's join, the model key's name, the port probe, and
the five signals the instances exchange beside the protocol's own.

## What it includes

- `Member`: `Id`, `Seated`, `Close`, `Gone`, `Ask`, `Claim`, `Release`,
  `Act`, `Holders`, `Waiting`, `Live` — the verbs the instance and
  the dashboard use. `Gone` closes when the member has left; `Ask` and
  the reads return at once then (false, or nothing) instead of waiting
  on an engine that stopped. A claim is final by acknowledgement; a member is
  gone only when its connection ends; nothing in the room polls.
- `New(home, places, effects, told)`: joins the room. The socket binds
  before the directory is read, so two instances starting together end
  in one room; the candidate id is the hash of the boot id, the process
  id and the start time at 62 bits and re-derives with a counter on a
  collision; the counter walk is bounded at `joinAttempts` 64 — the
  socket's name carries the process's own pid, so an address-in-use is
  almost always a listener this process leaked at that counter step,
  and past the bound the join refuses loud (naming the bound) instead
  of spinning. The start time's read fails loud too: a join whose
  `/proc/self/stat` cannot be read has no identity to sit under. The
  scan after the listen skips the peer whose dial refuses.
  Nothing mints until `Settle`, called after the last `Subscribe`. The
  accept loop classifies: a closed listener or a gone member ends it;
  any other error — the fd table full, the transient — is said loud
  through `told` at error level (naming the member and the error) and
  retried after `acceptBackoff`, one mesh period, the only duration the
  operator's row owns: a busy loop must not spin, and a momentary
  exhaustion must not stop a member its socket and sidecar still
  advertise.
- `KeyOf(model string)` and `TableKey()`: the keys the claims and the
  child operations ride, hashed from the model's id and the table's
  name. `MemberLeftNotice` is the machine room's member-leave notice
  text — the one evt's claimant publishes when a peer's transport ends
  (evt broadcast's `left`) — named here so the instances' end-jobs
  drain matches the room's word instead of a copy of it.
- `Answers(port int)`: the row's port probed once, at join when the room
  is empty — what is resident is read from the world, never replayed.
- `Loading(port int)`: whether the child's `/health` says 503 — the port
  answers before the model does.
- `Served`, `Record(home, model, served)` and `Recent(home, model, n)`
  (3.0.2): one line per request a rig served, appended to
  `~/.rig/logs/<model id>.served` by the instance that made it and read
  from the file's last 64 KB by the dashboard — what a model is doing is
  read from the world, like its residency. The tail's first line is
  dropped only when the cut landed inside a record: one byte is peeked
  before the window, and a cut landing exactly on a record boundary
  keeps that record.
- The signals: `StartChild`, `StopChild`, `SwapChild` (`Swap() bool`),
  `TableChange` (one row's raw JSON), `Update` and `JobStatus` (3.2.0:
  a scheduled job's state crossing processes — the job's id, the
  prompt's first line, `running`, `done` or `failed`, the started and
  ended stamps, the failure's reason, the worker count, the worker's
  latest call with its stamp, and since 3.3.2 the run's summary — the
  job's last non-empty stdout line, stderr's when the run failed,
  omitted from the wire when empty so a 3.2.0 peer decodes the frame
  unchanged), with `Codec` and `Registry`
  declaring rig's six kinds beside `broadcast.Protocol()`'s. `New` takes
  the member's version and writes it to `<room dir>/<id>.version` beside
  the socket (`Close` removes it); `Admits(version)` reads every live
  peer's sidecar and answers whether the room's slowest member still
  declares at least it — a missing, empty or unreadable sidecar refuses,
  fail closed — which is the gate a poster of a new act checks before
  it posts (SPEC_ROOM, "the version a member declares"). The walk rides
  `Ask`, so a gone member refuses — past `Close` the read fails and the
  answer is false, never the vacuous true of a walk over nothing. The
  run-job teller waits `JobStatusPostWait` for the posted
  acknowledgement of a terminal job status — two mesh periods, the
  proposal round and the vote round a pin needs, derived from the
  settings' period (250 ms by default) — then proceeds loud: the runner
  goes on and the status may be lost.
- The priorities: outside 0, child 20, fleet 30, delegate's return 70,
  the operator's turn 90.
- The door (3.1.0): `NewDoor(member, cfg, remote, home)` — the machine's
  one mesh member beside the unix room, constructed only when
  settings.json's `mesh` is set. The mesh member's id is the machine's
  (`broadcast.MemberOf` over the door address), not the unix room
  member's process id. The config carries
  `broadcast.NewTickClock(period, window, the fleet priority)` — the
  machine's time counted in periods, no proof, nothing computed between
  ticks — and the mesh's order is advisory: the key's home machine is its
  authority. The door mirrors the two orders: a fold hold on a key whose
  row names another machine's front posts into the mesh, and a foreign
  claim past the freshness horizon (the window) reposts into the unix
  room at the remote claim's priority — a hold, never a start: child
  operations run only on the row's machine. `Holding(key, mint)` is the
  front's mirror read; `Refused(key, mint, since)` is the clock's proof —
  the fold holds no act for the mint and the horizon has passed the
  mint's window (`since + window + 2`: an act folds while the horizon is
  at most its tick plus the window, and its tick is at most the request's
  arrival plus the door's sweep delay) — so the claim can no longer land
  and the front answers 503 naming the clock. `Holding`, `Foreign` and
  `Refused` take the same escape `Ask` and the member's reads do: when
  the door is gone they return at once (false, or nothing), so a `Close`
  racing a caller cannot hang it. A door whose dial goes
  unanswered for `rounds` periods is dropped, and what it held leaves as
  acts.
- The door's sweep is polled on a 100 ms ticker, DELIBERATE: the mesh's
  horizon advances on its own tick clock, the `Mesh` interface exposes
  no horizon-change event (only `Horizon()` reads and `Told` for acts
  gone stale past the window), and the sweep's other two loops —
  posting and releasing the mirrored holds as the local holders change —
  surface only here; a horizon event would be a new `evt` release, not
  a fix. A repost the mesh released without its local admission leaves
  at the sweep, and a repost whose entry left still releases the home
  room's claim when its admission lands (the pin answers, the entry is
  gone, the claim ends at its own priority) — a forgotten repost never
  becomes a hold nobody releases.

## How it is consumed

- `cmd/rig` builds one member at start with the children's `places` (the
  row's `Places`) and `effects` (the child operations' `Next`), and the
  notices' voice into the fleet room; the scheduler's `run-job` builds
  its own; `rig -update` builds one to publish the update and drains.
- The claiming provider (`cmd/rig/claim.go`) wraps every model call: it
  claims the model's key at its class's priority, the turn waits the
  admission, the request goes to the row's port directly, and the
  stream's completion is the release. A request that finds the child
  dead re-publishes the start so the next claim reconciles against the
  world.
- The fold's `places` reads the row's `Places`. A row without the child
  half reads zero: the fold admits the first claim on an empty key
  regardless, and a same-priority claim past it waits — the rows the
  machine serves carry places.

## Gotchas

- The frames ride one line on the pipe's scanner: a table change carries
  a 95-byte envelope and a child swap a 151-byte one, so the line
  budgets a 65440-byte row and a 65384-byte child; a frame one byte over
  ends the connection (`TestTableChangeFrameFitsThePipeLine`). The
  scanner fails closed: the room reads the member as gone.
- The room's engine is a second consumer: it never waits on a tool, a
  model or the disk. The child operations answer on the children's own
  engine, so a start's port wait never holds an acknowledgement.
