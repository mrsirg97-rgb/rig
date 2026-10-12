# room

## What it is

The machine room (SPEC_ROOM): every rig instance on the machine is a
member of one room over unix sockets, and no process is the center. The
ordering primitive is `evt`'s `broadcast`; this package holds only what
rig adds: the member's join, the model key's name, the port probe, and
the five signals the instances exchange beside the protocol's own.

## What it includes

- `Member`: `Id`, `Seated`, `Close`, `Gone`, `Ask`, `Claim`, `Release`,
  `Act`, `Holders`, `Waiting`, `Live`, `Say` — the verbs the instance and
  the dashboard use. `Gone` closes when the member has left; `Ask` and
  the reads return at once then (false, or nothing) instead of waiting
  on an engine that stopped. A claim is final by acknowledgement; a member is
  gone only when its connection ends; nothing in the room polls.
- `New(home, places, effects, told)`: joins the room. The socket binds
  before the directory is read, so two instances starting together end
  in one room; the candidate id is the hash of the boot id, the process
  id and the start time at 62 bits and re-derives with a counter on a
  collision; the scan after the listen skips the peer whose dial refuses.
  Nothing mints until `Settle`, called after the last `Subscribe`.
- `KeyOf(model string)` and `TableKey()`: the keys the claims and the
  child operations ride, hashed from the model's id and the table's
  name.
- `Answers(port int)`: the row's port probed once, at join when the room
  is empty — what is resident is read from the world, never replayed.
- `Loading(port int)`: whether the child's `/health` says 503 — the port
  answers before the model does.
- The signals: `StartChild`, `StopChild`, `SwapChild` (`Swap() bool`),
  `TableChange` (one row's raw JSON) and `Update`, with `Codec` and
  `Registry` declaring rig's five kinds beside `broadcast.Protocol()`'s.
- The priorities: outside 0, child 20, fleet 30, delegate's return 70,
  the operator's turn 90.

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
