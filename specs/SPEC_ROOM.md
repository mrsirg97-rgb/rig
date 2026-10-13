# rig: the room (3.0.0: no center)

Every rig instance on the machine is a member of one room over unix sockets,
and no process is the center. Each instance keeps its own loop and two
engines — the turn loop's, as today, and the room's; the room is the thing
the room engines agree on. The model is a guarded key in that
room: an instance sends its own request to the model, itself, when its claim
is final. What is loaded, a swap and an update are operations in that
same order. llama-swap leaves the stack and nothing takes its place as a
router. This release is one machine; more than one machine is 3.1.0.

The measured reason (battlestation, 2026-10-11, huihui-alpha-s on the Maya
engine behind llama-swap): the server takes requests in arrival order, so the
operator's turn waits behind whichever of six `rig -p` workers arrived first.
With two conversations decoding at once the total rises (about 90 tok/s
against 64 alone) but a single overlapped stream is a lottery: of 111
overlapped replies the median ran at 45 tok/s, one in ten under 32, the
slowest at 12; alone the slowest was 55. The engine's fair slice lets a
waiting request cut into a long answer, but only a request that is waiting at
the engine — so the fix is not a faster router, it is deciding who is waiting
at the engine. A model swap takes about 50 s and ends whatever is in flight,
and nothing orders it today. The room orders it.

The primitive is `github.com/mrsirg97-rgb/evt`'s `broadcast` at v0.3.0, the
ordering half rig's own `broadcast` (lifted from `~/Projects/lift` at 2.11.0)
left behind: "one slot needs no quorum". Lift remains the reference for
meaning, not for style.

## the room as evt v0.3.0 makes it

**The order is in the operation, not in a process.** An operation carries its
mint (tick, member, nonce), its key and its priority; every member sorts the
same fields the same way (priority first, then tick, then member, then nonce)
and keeps its fold's acts in that order. A late act takes its rightful place
when it arrives. A duplicate is refused by its mint identity while it is
still waiting; after the pin no transport delivers it again, so the fold
never sees it twice.

**The version chain with compare-and-swap.** An admission carries the version
it branches from and the version it becomes; the guard admits it only from
the head it names, and two admissions branching from one version race one
swap. Exactly one wins; the loser is withdrawn and made again when the key
moves. The act keeps its mint and its place in the order, so claims at one
priority enter in the order they were minted.

**An act moves in three rounds.**

1. **Publish and acknowledge.** The owner publishes the act to every live
   member; each member's room engine posts it to its fold and answers an
   acknowledgement — a claim only once it is first in that member's order for
   the key. The act is final when every live member has acknowledged it.
2. **Propose and vote.** The owner proposes an admission at the key's head;
   every member votes, and a member holding a better proposal for the same
   version refuses, so at most one admission per version can gather every
   vote. A refused proposal is withdrawn and made again when the key moves.
3. **Pin and tell.** The owner pins (the guard's swap), tells the room
   (`Pinned`), and acts. The owner's `posted` callback fires at the pin; the
   other members learn of it a moment later, when the tell reaches them, and
   fold the same admission. A member that pins before its own fold knows a
   later tell is coming reads its holders only after it agrees with the room.

**The fold is what is waiting, not what has happened.** It holds heads, holder
sets and the acts still waiting. A pinned act leaves it: request 5,000 costs
what request 50 does. A checkpoint is a key's head and holder set —
finality's line, not a log.

**What a joiner is given.** A member that joins receives each survivor's
checkpoints (heads and holder sets) and the acts still waiting, never the
acts pinned before it came. What those acts changed it reads from the world:
the table's rows from the models file, what is resident from the row's port.
The joiner mints nothing until `Settle`, called after the last dial is
handed to `Subscribe` and every dialed member has answered.

**What a leave does.** The connection's end is the leave. The gone member's
waiting acts are dropped; each survivor sends the others its checkpoints and
a `Left`, and no member pins until it has heard every survivor, so a pin the
gone member told only some of the room is known to all of it or to none. What
the gone member held is released for by whoever sees it, and the duplicate
releases delete nothing and pin as nothing.

**Liveness is the connection.** A member is gone only when its connection
ends; a member that is connected and silent holds the room — every act waits
for its acknowledgement, and only its connection ending frees the others.

**One priority, arrival order.** The room's messages are handled in the order
they arrive, all at the claimant's own priority. An act's priority decides
its place in the order of admission and the priority its callback arrives at;
it does not let one message pass another on the way in.

## the three seams

Finality, the clock and the transport are seams, because 3.1.0 puts lift's
other choices behind them.

- **Finality** is the acknowledged claim (above). Lift's eventual finality —
  a tick is past the freshness window, so no earlier tick can still arrive —
  is replaced by every live member having acknowledged the operation.
- **The clock** is the machine's: the wall clock taken once at the process's
  start plus the monotonic time elapsed since, comparable across processes
  and never backwards within one (evt's `MachineClock`). Nothing above the
  clock may assume it is the kernel's; 3.1.0's VDF is a clock swap, not a
  rewrite.
- **The transport** is a unix socket. Nothing above the transport may ask for
  a file descriptor, an address family or a listener; the `Transport`
  interface (`Send` with an ack, `Recv` with a callback, `Close`) holds the
  shape, and the unix transport landed beside the pipe in evt v0.3.0.

## membership: the directory is the registry

- The room's home is `~/.rig/room/`. It holds one unix socket per member,
  named by the member's id and the process id. **The listing is the
  membership.** There is no registry process; a directory is not a process.
- A member's id is `MemberOf` over the hash of the machine's boot id, the
  process id and the process start time, at **62 bits** (the top bit clear
  keeps it positive in a signed int64). At 62 bits a joining member collides
  with one of eight live members with odds of 8/2⁶³ ≈ 10⁻¹⁸. A collision
  with a live member re-derives in process — the hash takes a counter — until
  the id is free among the directory's members; the instance does not exit.
  The room id is the rig home's: one machine, one home, one room.
- **Joining**: an instance listens on its own socket **before it reads the
  directory**, so of two instances starting together at least one sees the
  other, and the dial makes it mutual: the accepted end answers, so both
  count each other live. It then reads the directory once, dials what it
  read, and announces itself. **A peer gone at the dial is skipped, not an
  error**: the directory was read a moment ago and the peer's own gone-check
  runs again at the dial; a bind without a listener refuses too. A joiner
  does not mint until it has connected to every listed member and received
  each one's answer, and a member counts toward finality from the moment its
  join is complete.
- **A stale file is judged by the process, never by a refused dial alone.**
  The file's name carries the process id, so the scan removes a file whose
  process is gone and keeps one whose process lives. A process that has bound
  its socket and not yet begun listening refuses a dial as well, which is why
  the dial's refusal skips the peer instead of ending the start.
- **Liveness is the connection.** A member is gone when its connection
  closes; the close is an event on each member's room engine. A graceful
  exit removes its own socket file; a killed process's file lingers until a
  later scan judges its process gone.

## two engines: the room's and the loop's

Each instance runs two engines. The turn loop's is today's engine,
unchanged. The room's is a second engine whose consumer does nothing but the
room's work and never waits on a tool, a model or the disk. The second engine
is not a luxury: the loop's engine blocks — a write runs inline (2.17.0),
`Assemble` and a compaction hold the consumer — and a member in a ten-minute
bash call could acknowledge nothing, so no claim in the room would become
final, the operator's turn in another session included. Arrivals land on the
room engine at the operation class's priority, and the instance's own room
operations are events beside them. The room posts to the loop what the loop
needs — a claim is final, a release is due, a member left — at the kernel's
priorities; the loop's turn waits on that post and never on the room's
internals.

All room state — the fold's heads, holder sets and waiting acts, the live
members — is touched with no mutex: the room engine is the one consumer
(SPEC_EVT's contract), so one goroutine touches it at a time. A mutex around
state that only the consumer touches is machinery for its own sake. The
consequence is stated plainly: a structure the room holds is touched only by
the room engine's consumer, and the structures that cross goroutines are the
ones that already do (each engine's queue, the transport's wire).

## the model as a guarded key

- A model is **one counted key**. The row says how many places the model
  serves at once (two with two sequences, three for the `-np 3` child); the
  holder set is the guarded state. Per-place keys would invent identity the
  server does not have: the child serves N interchangeable sequences, and the
  dashboard's question — who holds each place — is answered by the holder
  set, not by a key per place.
- **Before a request the instance claims**; when the claim is final, the
  compare-and-swap took the version slot and the admission folded, it sends
  its own request to the child's port; **its completion is the release**
  (the stream's terminal event mints the release, the next version on the
  chain).
- A proposal that lost the compare-and-swap is withdrawn and made again when
  the key moves; the act keeps its mint, so claims at one priority enter in
  the order they were minted.
- **A strictly higher-priority claim may enter while the places are full.**
  The entry is a holder beyond the places: at most one, and only a claim
  strictly higher in priority than every holder. The reason: the entry
  exists so the request waits at the engine and the fair slice can serve it;
  a second beyond-places holder would stack decodes with no priority reason
  (a same-priority claim queues for places; a lower one waits). Entry, never
  eviction: nothing kills a request mid-flight. The invariant, named
  exactly: never more holders than the places and its one entry, and the
  entry is strictly higher than every holder.
- **Within one priority, the claim that continues the conversation already in
  the model goes first**, then the order. The holder set records the
  conversation each place serves; a claim naming the same conversation is the
  continuation. The reason is the measured cost of not doing it: a dropped
  reply costs a full prompt re-read (175,260 tokens in 70 s; 198,266 in
  78 s), and a slot's conversation state is the one thing the engine keeps.
- `maxWorkers` keeps its meaning: it is the delegate's hand-off cap on rig's
  own workers, and it is not the model's places. Places is the row's field.

## the priority table across instances

What arrives over a socket lands on the receiving member's room engine at the
priority the operation's class carries. The kernel's numbers are reused where
the meaning is the same; two classes are new and one takes the engine's own
floor, so nothing here is a magic number.

| class | priority | source | reason |
|---|---|---|---|
| the operator's turn | 90 | `PriorityInput` | the measured evidence: the operator's turn waits for no worker |
| a delegate's return | 70 | `PriorityDelegate` | 2.15.2's finding, unchanged |
| a fleet worker | 30 | `PriorityFleet` | the swarm's claims and chatter |
| a scheduled job | 20 | new | below the fleet: a cron job is unsupervised batch work, nothing interactive waits on it, and it must never delay a worker an active turn needs |
| model child ops and table changes | 20 | new | start, stop, swap and table edits are background work nobody interactive waits on |
| an outside client | 0 | the engine's clamp floor | the room's own work outranks external callers; the floor is the engine's own lowest, not a new number |

- The update is an operation at 90 (the operator's turn — it is the
  operator's command).
- **A request for a resident model outranks a swap** as an admission rule,
  not a priority: a swap's admission waits while claims for the model it
  would unload are sendable. A tie at 20 (a scheduled job's claim against a
  swap) resolves the same way. The operator's turn can force one: a forced
  swap is an operation at 90 that still acts only at the in-flight request's
  end.
- The in-process fleet (the frontend, delegate, graph and decision members,
  the negative ids) does not cross a socket. The instance speaks to the
  machine room through its socket member on the room engine and to its own
  fleet through loop transports on the loop engine; the two scopes share the
  `Room` type and never share ids (the mint is 62-bit positive; the
  in-process ids are negative).

## a holder that dies

- Its process's sockets close on every member — the kernel closes the fds —
  and the close is the event. The members drop it from live; what it held is
  released for by whoever sees it, and the duplicates are harmless (several
  members may mint the same release; each deletes the same hold, so the rest
  delete nothing and pin as nothing).
- A holder that is connected but stuck is not given a timeout here. What ends
  it is named: the server's own watchdog (the provider's idle bound closing
  the stream, which ends the hold the same way a completion does) and the
  operator's interrupt. The hold ends with the request, whatever ends the
  request.

## the model table

- **The file is `~/.rig/models.json`** — the models rows file that exists,
  extended, not a new file beside it. One table, one file, one shape per
  concept.
- The row gains: **command** (the child's command line, `${PORT}`
  substituted), **environment** (the child's env), **port** (the child's
  fixed port, chosen at creation from the range the import carries in),
  **aliases** (the names that resolve to the row), **stop grace** (seconds
  the SIGTERM waits), and **places** (how many the model serves at once).
  The provider half of the row (window, maxTokens, reserve, effort, prices)
  is unchanged; the row gains the child half.
- The provider reads the row: the holder's request goes to
  `http://127.0.0.1:<port>/v1` directly. llama-swap retires as a router;
  `settings.json`'s `swapUrl` retires with it. (The OpenAI-wire front's own
  reads and writes — what it serves — is out of scope; the provider half is
  this release's.)

## the child operations

- **Start, stop and swap are guarded operations** on the model's key. The
  act's owner starts or stops the child, **detached** (a new session: it
  outlives that instance). Two instances racing to start a model both mint
  the start operation; one admission pins first and its start's child takes
  the row's port, the other start finds the port answering or loses the bind
  and its child dies — one child runs, and the pid file names it.
- **An act's effect is done once and by one member — the act's owner.** The
  fold offers the effect to every member in the key's version order; the
  children answer for the act's owner and leave the act to the others. The
  room's engine stays free while a child starts or stops: the start and the
  stop run on the children's own engine (the third consumer), because a
  start waits the child's port for as long as the child lives and a stop waits the
  row's grace — a member that acknowledges nothing is exactly the disease
  this release removes, and the loop's engine must not inherit it.
- **The others learn the outcome from the world, not from a message.** What
  is resident is state read from one place: the row's fixed port answering.
  The owner writes the child's pid and its start time to
  `~/.rig/logs/<model id>.pid` at the spawn (a start that finds a living
  child there waits on it and spawns nothing; a pid whose start time
  differs is another process and names nobody), so any
  member's stop reaches the child through the pid file, and the child's
  output lands at `~/.rig/logs/<model id>.log` — a detached child needs
  somewhere durable to speak and the dashboard streams it. No timeout enters
  the room for it: the probe and the grace wait on the children's engine,
  and the room's acts never wait behind them.
- **Residency is recoverable from the world, and by one path.** When the last
  rig exits, the detached child keeps running and the next instance joins a
  room of one with nobody to ask — so the row's fixed port answering is the
  proof. A joiner that finds no members probes each row's port once, before
  it may start anything: a port that answers is a resident model, and the
  joiner starts nothing. No state file beside it: one path, or the two
  disagree.
- **A claim that finds its row's port silent starts the child before it
  sends.** It holds its place, publishes the start on its own key, and sends
  once the port answers and the child's `/health` no longer says 503. When
  another row's child is resident it publishes the swap on that row's key
  instead: the swap waits for that row's holders, the resident child stops,
  then the incoming one starts. One child at a time, the rule the router
  had; a row that should share the machine with another is a later release.
- **Stop sends SIGTERM and waits the model's grace.** Maya saves its
  conversation on SIGTERM and needs up to about 90 s; the row's stop grace
  carries that number, not a constant in code.
- **A change to the table is an operation in the order, never a kill
  mid-reply.** The winner writes the models file and publishes the change;
  every member applies it to its own table. The change's admission waits for
  a boundary, like a swap. The file is the world the joiner reads, so the
  change is in it before the act is published.

## the update

- `rig -update` is an operation in the order. **Fetch and verify the new
  binary first** (the flow rig has: latest tag, minisign against the pinned
  key, the checksum). Then publish the update at 90.
- **Every member stops claiming at its turn boundary and leaves.** A turn
  boundary is an operation in the order: the member publishes its drain when
  its turn ends; no poll, no timer.
- **When the updater is the only member left and no model request is in
  flight**, every rig process is ended and the binary is renamed into place.
  It is lossy on running work and the operator's choice: no resume machinery.
- **A member that left refuses, by name.** Its next request and its
  dashboard reads return at once with the reason; nothing waits on a room it
  is no longer in. (Ending the process is still the operator's restart.)
- **The old binary is kept for rollback** when the new one does not start.
  An update run from inside a session detaches from it first.

## serve

- `rig serve` is **a member like any other** — its room engine, its loop
  engine, its socket — with two extra jobs: the dashboard (loopback 7777,
  tailscale serve is the way out, nothing binds 0.0.0.0) and an
  **OpenAI-wire HTTP front on loopback that claims on behalf of anything
  that is not rig**: `/v1/chat/completions`, `/v1/models` with the rows'
  aliases, and what replaces llama-swap's `/running` and `/unload` — the
  resident set, and a guarded stop.
- **Sessions do not depend on it**: a rig alone is a room of one, and
  starting a rig does not start serve. Serve is its own instance (the
  dashboard binds a port only one process can hold, and the room's work must
  not wait on a web server).

## the dashboard's three pages

The dashboard keeps its pages and gains three, kept simple:

- **the room** — members, claims by priority, who holds each place, what is
  resident or loading, and the last requests served from each model (the
  class, the prompt and its cached share, the output, the decode and prompt
  speed), read from `~/.rig/logs/<model id>.served`, which the instance that
  made the request appends to (3.0.2);
- **model logs** — each child's output, streamed from the log file while the
  page is connected;
- **system stats** — GPU utilisation, temperature, power and memory; CPU use,
  RAM, load average, network; CPU and board temperatures.

Sources are pure Go and need no packages: `/proc`, `/sys/class/hwmon`, and
`nvidia-smi --query-gpu` as CSV. Sampling runs only while a page is
connected; a source that is missing does not render (the box has three GPUs;
a box with none must not render a GPU row).

## the module and the branch mechanics

- rig's module path is `github.com/mrsirg97-rgb/rig/v3` (2.18.0 → 3.0.0).
- evt v0.3.0 is merged and tagged; rig's `go.mod` requires it and the
  untracked `go.work` left the worktree. No `replace`.
- The engine's six files (`clock.go`, `closure.go`, `engine.go`, `event.go`,
  `queue.go`, `scheduler.go`) do not change; the room needs nothing from
  them. `loop/loop.go`'s 2.17.1 cancel branch is out; `loop/batch.go` and the
  kernel's priority table stay closed. `specs/FREEZE.txt`'s reopen line for
  it left with it.

## non-goals (named)

- More than one machine (3.1.0): lift's VDF clock, its quorum and freshness
  horizon, its TCP transport, gossip and its summaries, the WAL, and a `mesh`
  setting. **Build none of it and leave nothing switched off in the tree** —
  the three seams are all this release owes 3.1.0.
- No central process: if a step needs one instance to be running for another
  to work, stop and report it. The directory is not a process; serve is not
  required; the model child is detached.
- No locks, timeouts or polls: a claim is final by acknowledgement, a member
  is gone when its connection closes, readiness and draining are operations
  in the order.
- Tuning any model's engine settings. Changing what the API half of
  `rig serve` reads and writes.
- The transcript stays the one record: no member adds to, removes from or
  rewrites another's messages. The room never writes another instance's
  store.

## layout

```
evt/broadcast v0.3.0 (the ordering primitive, merged and tagged)
  message.go     Message: origin, ok, signal
  operation.go   Operation: mint, key, parent, version, priority
  mint.go        MemberOf, Minter, MachineClock
  guard.go       Guard: per-key compare-and-swap over the version chain
  fold.go        Fold: heads, holders, the acts still waiting; Effects.Next
  claim.go       Claimant: the three rounds, liveness, checkpoints
  signal.go      the protocol's signals: Claim, Release, Admission,
                 Acknowledgement, Pinned, Withdrawn, Checkpoint, Left,
                 Joined, Seated
  encode.go      the wire: one JSON frame per line, the registry
  pipe.go        the pipe transport
  socket.go      the unix socket transport: Listen, Scan, Dial
  room.go        Room, Member, the loop transport
rig (3.0.0, on feat/3.0.0-no-center)
  room/          the instance's member: join, rig's own signals, the table
  models rows    command, environment, port, aliases, stop grace, places
  cmd/rig        children.go: the child ops' owner and engine; claim.go:
                 the claim before a request; update.go: the drain
  provider       the row's port is the endpoint; the terminal event on cancel
  loop/loop.go   the 2.17.1 branch is out; the loop appends on Done only
  frontend/web   the wire front and the dashboard's pages
```

Every exported type is an interface over a private struct and a constructor
returns the interface, as `Engine`, `Queue`, `Room`, `Cap` and `Registry`
are; only what an application must build or embed stays concrete, as `Mark`
and `Notice` do. The payload is `evt.Signal`, never a new word for it, and
**evt owns the protocol's messages**: the claim, the release, the
acknowledgement and the admission are evt's signal types; rig declares only
its own — the child operations, the table change, the update — declared in
the wire's registry, each embedding `Mark`. No comments in Go code; no new
dependencies; no cgo.

## tests

evt's own hold the primitive: the guard's races, the churn suite (members
dying and joining under load never overfill the places and never wedge), and
the wire's round-trips.

Rig's, against a fake model server and several members over real sockets:
a member alone claims and holds; two members started together end in one
room; priority outranks arrival; never more holders than the places and its
one entry, and the entry is strictly higher than every holder; a member that
dies releases for itself; a holder's hold is seen by a member that joins
after the fact through the world, not through a replay; an act's effect runs
once, on the act's owner, with the room's engine free while a child starts;
a joiner after a table change and a start agrees with the first member's
view; the start waits the port and lands the log and the pid where the note
names them; a second instance stops the child through the pid file; the
update drains the room; two instances race to start a model and one child
runs. The provider's terminal event on cancel keeps 2.17.1's tests passing
with the branch gone.

## for the operator (the judgment calls this note makes)

1. **A counted key, not one key per place** — the holder set is the guarded
   state; per-place keys would invent identity the server does not have.
2. **The entry is a holder beyond the places, at most one, strictly higher
   than every holder** — the alternative was making it wait for a place,
   which leaves the operator's turn behind two in-flight decodes, the
   measured problem unsolved.
3. **The outside client at the engine's clamp floor (0)** — below scheduled
   jobs. If the operator's own scripts deserve better, this is the row to
   move.
4. **A scheduled job at 20, below the fleet (30)** — a cron job must never
   delay a worker an active turn needs.
5. **The act's effect is the owner's, on the children's engine; the others
   read the world** — the alternative was every member running the effect
   (two children racing one pid file) or the room's engine waiting out a
   port probe (the draft's wedge). Residency has one shape:
   the row's port.
6. **The model logs land in `~/.rig/logs/<model id>.log`** — a detached
   child needs somewhere durable to speak; the dashboard streams the file.
7. **`settings.json`'s `swapUrl` retires** with llama-swap; the provider's
   endpoint comes from the row's port.

## 3.1.0: the mesh (the design note)

The room crosses machines. One protocol, two modes. `ipc` is 3.0.4 and stays
the default: the instances of one machine over unix sockets, a claim final
when every live member has acknowledged it. `mesh` is a setting: the
machines' orders joined across more than one machine, ordered by verifiable
time, converging by gossip. The operator's steer rules three things: the mesh
has no quorum; it joins machines, not instances; and the VDF stays its clock.
An instance never knows which mode it is in: its room is the machine's unix
room in both modes, and 3.0.4's code runs unchanged on it. The door is where
the modes meet: a member of the machine's unix room and the machine's one
mesh member, carrying that room across. `mesh` ships whole and tested and is
off until the operator turns it on. This note is step 1 of 7; steps 2-7 start
on the operator's word, in a later session.

The inter-system half exists: `~/Projects/lift/broadcast`, left out of 3.0.0
on purpose — the VDF clock (Wesolowski proofs), the sequencer's freshness
horizon and reorg path, the TCP transport with pooled connections, gossip by
set difference over fixed-width summaries, the WAL — and with them the quorum
commit and its recovery round and the guarded version CAS, which stay in
lift. 2,619 lines without tests, 1,364 of tests, standard library only; its
gossip core says of itself that a write is purely local and convergence is
anti-entropy, and its storm test runs 7 peers under shuffled, contended,
duplicated delivery with zero divergence. Lift remains the reference for
meaning; the port conforms to evt's shapes.

### finality becomes a seam in evt

evt v0.3.0 carries two of the three seams. The clock is one
(`NewMinter(member, clock)`, `MachineClock()`), the transport is one
(`Transport`, the pipe and the unix socket). Finality is not: the rule —
final when every live member acknowledged, admitted when every live member
voted — is written into the claimant (`broadcast/claim.go` `mark`, `tally`,
`left`). evt v0.4.0 lifts that rule behind one interface the claimant
consults, and the claimant keeps its three rounds unchanged. The seam owns
three answers:

- **when the act is final** — what `mark` asks;
- **when the admission is decided** — what `tally` asks;
- **what a closed connection means** — what `left` does.

The default implementation is ipc's rule, verbatim; every v0.3.0 test passes
untouched with it behind the claimant. The seam is a constructor argument
(`NewClaimant(..., finality)`) — constructed whole, never a global, never a
mode branch in the claimant's body. mesh's rule goes behind the same seam,
and mesh's member is lift's gossip core, not the claimant's three rounds: a
write is local, the horizon decides what is acted on, and the log converges
by gossip. The evt engine does not change; the fold, the guard and the
signals do not change shape.

### what mesh puts behind the three seams

**Finality: the horizon alone.** The mesh has no quorum. An act is acted on
once its tick is past the freshness horizon in the acting member's view —
past the window inside which an earlier tick can still arrive — and the log
converges by gossip. The quorum existed to stop two sides of a partition from
each acting on the same key; rig does not need it, because every key has one
home machine: the model's row runs on one machine, and a request has to
arrive there to be served. The check at that door is the authority: a peer
cut off from the home machine can hold the key in its own view and still
cannot reach the model. **A key's home machine is the authority for that
key, enforced where the resource is.** The limit is named: this holds while
every key has one home machine. A second machine serving the same model
makes two homes, and a quorum — two sides that each believe the other left
must not each admit a holder — comes back; 3.1.0 does not build it.

**The wait, as a number.** The wait is about (window + 1) tick periods — one
for the phase of the mint, window for the horizon — and it is paid where the
claim crosses machines: a local request pays none beyond today's, a remote
one pays the horizon at the front. The tick period is the VDF's computation,
measured on battlestation: 1M iterations cost **1.45-1.68 s** a tick across
idle and decoding runs on the 1536-bit prime and **2.5 s** on RSA-2048, and a
proof verifies in **0.48-0.65 ms** on either modulus. The evidence's "about
18 s" figure for 1M iterations is not measured on this box; if the MacBook
paces, its band is that number's, and the wait scales with the pacer's tick.
With window 2 a remote request waits about 5 s at the front; a turn on the
remote machine mints one claim per model call, so a 40-step turn pays it 40
times — the price of ordering cross-machine claims, set by the window and
the iteration count, both construction values that do not exist in ipc.

**The clock: the VDF.** The clock seam is evt's `Clock`
(`NewMinter(member, clock)`), and mesh puts the VDF clock behind it. The
chain is deterministic from the seed, so every door computes the same
timeline and nobody can forge ahead of it: a tick is elapsed computation, and
any door verifies it for half a millisecond. **The seed is the room's name**
hashed to the clock's zero — the room's name is its epoch, and two meshes on
one tailnet refuse each other's ticks, which makes the name the namespace
too. The iteration count is the delay per tick and the operator's knob. The
mint's step is the tick index (the ordering stays priority, then tick, then
member, then nonce — one `opLess` for both rooms), and the act's envelope
carries its tick's proof, which every receiver verifies before it folds —
an unverified tick is refused, never trusted. The frontier (tick index →
output) is what a door keeps to verify the next proof and what gossip
carries with the ops. **The chain runs only while more than one machine is
in the room**: a machine alone in `mesh` computes nothing, its unix room
never uses the VDF, and the pacer is one goroutine on one core, continuously,
only for the window the room spans machines. The GPU machine is the natural
pacer — its arithmetic is the fastest measured here — and the other doors
verify and follow. **What the pacer costs the decode: measured, nothing we
can see.** With ox-alpha-s resident and decoding a 900-token prompt at 52
tok/s, the tick's compute ran 1.57-1.68 s against 1.45-1.60 s idle and the
decodes with the tick running measured 51.1 and 52.1 tok/s against 52.3
without — the cost is below the noise of identical decodes on this box (the
first decode after idle measured 37-51 tok/s across warm-up: the band, not a
tick cost).

**The transport: TCP and gossip.** The transport seam is evt's `Transport`,
and mesh puts lift's TCP transport behind it: length-prefixed frames, one
dial per peer with pooled connections, a dial timeout that is a construction
value — mesh's named bend, absent in ipc. One wire, three verbs: a push
carries acts, a pull carries a watermark and returns a log, a gossip carries
a summary and returns the difference; the acks are receipts, not votes —
**the mesh's write is lift's gossip core: purely local, appended to the op
set, and convergence is anti-entropy.** **Gossip reconciles by set difference
over fixed-width summaries**: 4096 buckets, one hash each, the width constant
no matter how large the log grows, so divergence in recent ticks lands in a
few adjacent buckets and the transfer tracks the diff, not the log. The round
is paced by the tick: each finished tick drives one round — the VDF's cadence
is the interval, and no timer of its own is built. **The WAL is the log each
door keeps**: Append, Replay, Compact at the checkpoint boundary. It is what
a joiner is given (sync past a watermark, then gossip until the summaries
match), what a partition heals from (Sync, and Reconcile for the door that
fell further behind), and what makes every write exactly-once under
duplicates: the dedup and the idempotent fold make a redelivered act land
once.

**The two orders and the door between them.** Each machine's unix room
orders its own instances as 3.0.4 does, by the machine's clock; the mesh
orders the machines' doors by verified tick. The door composes them with two
mirrors. Looking out: the door is a member of the unix room, and when its
room's fold shows a hold on a key whose row names another machine's front,
it posts that hold into the mesh — the claim's mint, key, priority and
machine. Looking in: when the mesh's log shows a remote claim whose tick is
past the freshness horizon in this door's view, the door reposts it into its
unix room as a fresh act at the remote claim's priority, holding the key
itself and carrying the remote claim's mint in the act. **The home machine's
room decides between a local claim and a remote one for the same key**: both
are acts in its order, one clock, the rules 3.0.4 already has — priority
first, then the mint; places, the entry, the continuation. The remote claim
inside the home machine's room is the door's hold: the door member holds it,
at the remote claim's priority, and what releases it is an act — the remote
machine's own release carried by its door, or the release the home door
mints when the remote machine goes quiet. The mesh's fold is the doors'
bookkeeping: which machine holds which key, the mirror's source, and the
horizon's order.

**One liveness rule, not two.** A member is gone when its connection ends —
the mesh's door-to-door connection and the unix room's socket speak the same
rule. The release of a gone member's holds is an act in the order that every
member folds the same way: the door mints it, the gossip carries it, and a
duplicate release deletes nothing and pins as nothing. The mesh adds no
second rule: a partition's close is the connection's end, and the horizon,
not a liveness guess, decides what the gone member's acts can still do.

**What does not come across.** Every structure is touched only on the
consumer: lift's mutexes on the sequencer and the gossip core do not join —
the room's engine is the one consumer, so one goroutine touches that state at
a time. The transports keep their own connection bookkeeping, as transports
do today (they cross goroutines in both modes). No new mutex in either mode;
the evt engine does not change. The port is the VDF clock, the sequencer's
horizon and reorg path, the TCP transport, gossip and its summaries, and the
WAL; **the quorum commit, its recovery round and the guarded version CAS
stay in lift** — the version chain lives where the resource is, in the unix
room's guard.

### the mode is chosen by a setting

The setting is `settings.json`'s `mesh`. Absent, nothing changes: no mesh
piece is constructed, every instance joins the machine's unix room as 3.0.4
does, and 3.0.4's wire is byte-for-byte with or without the setting — the
instances never join the mesh, so the unix room's wire cannot change. Set,
it names the room and the machine's door:

```json
"mesh": {
  "room": "operator",
  "listen": "100.118.31.66:7780",
  "peers": ["100.120.230.71:7780"],
  "iterations": 1000000,
  "window": 2
}
```

- `room` seeds the clock (the epoch) and namespaces it.
- `listen` is this machine's door: the address the door-holder binds.
- `peers` is the other machines' doors. The list is per-machine config, not
  per-instance: the instances never dial it, the door does.
- `iterations` and `window` are the clock's and finality's numbers above.

The door is `rig serve`'s instance — the process the wire front already
lives in and the one long-lived instance rig runs — and a machine that runs
no serve has no door: the mesh does not carry it, its unix room is 3.0.4,
and its requests cost today's price. The mesh's config is the door's, and
the door reads the same settings.json the instances read. In evt, the mode
is what is constructed, never a global: the door builds the mesh's clock,
transport, sequencer, WAL and gossip in its constructor, the way an instance
builds the unix room's pipe transports today. The instances' code never
changes shape: `cmd/rig`'s claim path, the children, the wire front and the
updater run on the unix room's member, which is 3.0.4's member in both
modes.

### what a mesh peer and an ipc room do when they meet

They are nested, not disjoint. The door is a member of both rooms at once:
it holds the machine's mesh member and a seat in the machine's unix room,
and the two orders meet in its two mirrors. The concrete meetings:

- **A machine with no door.** A machine that runs no serve has no door: the
  mesh does not carry it, its unix room is 3.0.4 untouched, and a `peers`
  entry that names it refuses at the dial — the door that dialed joins with
  who it reached, and the gossip round re-dials.
- **The door's absence.** The machine's unix room runs as 3.0.4 while its
  door is down: its order never depends on the door for its own members, so
  local requests cost what they cost today. The mesh room runs on the
  surviving doors; the doorless machine's holds are released by the peers as
  acts, and a remote request to its front refuses until serve returns.
- **The room's own subprocesses.** A worker, a `run-job`, an update — every
  rig process joins the machine's unix room as it does today, claims as it
  does today, and pays today's price; when the key it claims names another
  machine's front, the door carries that hold across the mesh. There is no
  third mode and no instance ever dials TCP.

### how peers on other machines are found and trusted

The trust boundary is the tailnet: a door binds the tailnet address only,
never 0.0.0.0, so an off-tailnet dial cannot reach it. Inside the boundary,
the order's integrity is the VDF: a door cannot forge the timeline, an
unverified tick is refused, and the room's name keeps two meshes from
joining each other. The join carries ipc's order over TCP: **the listener
binds before the first dial**, the joiner dials a door, the dial makes it
mutual, and the `Seated` answer introduces the rest of the doors. The member
id is the same 62-bit `MemberOf` over the machine's identity, the process id
and the start time, re-derived on a collision; the door-holder's id churns
with its process, and the machine's identity in the mesh is its door address,
which the setting holds.

**Discovery without a hole.** When a machine's door-holder exits and its
other members live on, a joiner from the other machine still finds the room,
by two mechanisms and no poll. First, the door-holder's exit is an event the
machine's own instances see — the door is a member of their unix room, and
its leave is the connection's end 3.0.4 already delivers — and the instances
that saw it attempt the door's bind, first bind wins, the losers stand down;
the machine's door address is held again at once by a living member. Second,
the gossip round re-dials: a refused door costs nothing and is tried again
each round, paced by the tick, so a door that joins inside the hole connects
on a later round. The hole is about one tick period, and the mesh's order
never waited on it: the home machine's room runs as 3.0.4 through the whole
thing.

**Liveness is the connection, in both rooms.** A member is gone when its
connection ends — the unix room's socket and the mesh's door-to-door
connection speak one rule. What the gone member held is released as an act
in the order that every member folds the same way, and a member that returns
re-claims: its stale acts are refused past the horizon-window, its fresh
ones take their rightful place.

### how a remote peer reaches the model

A remote machine's request crosses three rooms, and each one's rule is
unchanged where it runs. On the remote machine, the instance that needs the
model claims it in its own unix room — today's rule, today's cost, the local
places and the local priorities — and the row on that machine is a remote
row: `baseURL` names the home machine's wire front, the port is 0, there is
no child half, and the provider's endpoint rule already sends the request
there. The machine's door sees that hold in its unix room's fold and posts
it into the mesh: the claim's mint, key, priority and machine. On the home
machine, the door folds the remote claim, and once its tick is past the
freshness horizon in that door's view, the door reposts it into the home
machine's unix room as a fresh act at the remote claim's priority — the home
machine's room decides, by the rules 3.0.4 already has, whether that hold
takes a place. The request the instance sent to the front carries its mint,
and **the front waits**: a request carrying a mint is served when the front's
own fold shows that mint holding the key — the fold of the home machine's
room, where the door's repost carries the remote claim's mint — and it waits
until it does, because a remote peer can be admitted in its own view before
the home machine has the act. The wait ends three ways, named: the mint's
hold appears (the repost landed, the request is served), the caller's
context ends (the remote peer gives up), or the release act lands — the
remote machine gone quiet releases the hold as an act in the order, the fold
no longer shows it, and the front's wait ends refusing. The horizon bounds
the repost, so the wait is at most about (window + 1) tick periods.

**The front does not claim a second time for a caller that already holds the
claim.** The gate exists for the measured failure, not for ceremony: before
3.0.2 a worker claimed the model, sent its request to the front, and the
front claimed the same key behind the worker's own hold — the job waited on
itself for 27 minutes. The mint is not a cryptographic identity; the tailnet
is the boundary and the home machine's fold is the authority.

**The front on the tailnet.** With the mesh setting on, the front binds the
tailnet address beside loopback; the dashboard and its token gate stay
loopback. A request with no mint from a non-loopback address is a caller the
tailnet vouches for, and it claims at the floor as today's loopback callers
do. `/unload` stays loopback: an unload is the operator's gesture, and a
remote machine's stop is its own release carried by the order — the tailnet
front serves the model wire and the resident read, not the stop.

A remote machine's requests are recorded by the front — the instance that
served them — so they appear in the served table of the machine that holds
the model, as step 4 requires.

### what a remote peer reads

Nothing. A remote machine reads none of the home machine's ports or files,
and it needs none of them: it sends to the front, and the front already
starts a cold child by the home machine's own rules. The projections the
note once carried — residency from the log, places from the table change —
are dropped, and the evidence is why: `TableChange` is posted by no
production code in 3.0.4, only by tests, so there was nothing to project
from. What is resident is the home machine's world-read (the row's port,
one path), places is the home machine's unix-room rule (the row's machine's
own table), and the mesh carries claims and releases between machines —
never tables. The remote machine's own requests land in its own machine's
served file, and the home machine's front records the ones it serves, so
each machine's table shows what that machine served.

### who may own a child operation

The home machine's members, exactly as 3.0.4 has it. A child operation is an
act in the home machine's unix room, its owner is its origin, and the origin
sits on the machine the row's command runs on because only that machine's
members have the row with a command and a port in their own table — the mesh
carries no child operations at all. The door's repost is a hold on the key,
never a start: when the home machine's room admits it and the row's port is
silent, the front's own ready path publishes the start — 3.0.4's cold-claim
rule, run by the home machine's member. Two members of the home machine may
both mint a start; the world reconciles them exactly as 3.0.4's two-instance
race does — one child runs, and the pid file names it.

### the second machine's /proc fallbacks

rig builds for darwin (verified: `GOOS=darwin` for arm64 and amd64). Two
things 3.0.4 reads come from `/proc`:

- **The member id's boot id and start time** (`room/member.go` falls back to
  the hostname and a start time of zero): **enough**. The id's job is
  uniqueness among the room's live members and across a restart; the
  hostname is unique on the tailnet (battlestation, mrsirg-macbook) and the
  process id separates the instances of one machine and changes across
  boots. A hostname macOS renames only moves the id across boots — a new
  member, which the room absorbs by its own rules.
- **A child's start time in the pid file** (`children.go` `startOf` returns
  zero): **the rule is the pid file is believed while the pid is alive.** A
  loading child's port is silent for exactly the window the file must be
  believed — 3.0.4's double-start fix writes the file at the spawn and waits
  on the living child, and a rule that disbelieves the file on a silent port
  disbelieves it while the child loads. The recycled-pid guard keeps its
  second brake: the start time, and a start-time source exists on darwin —
  `golang.org/x/sys/unix`'s `SysctlKinfoProc` over `kern.proc.pid`, the
  process's start time as the kernel records it, a dependency rig already
  carries and no cgo (`ps -o lstart=` is the subprocess-shaped way to the
  same number). The row's port stays the residency proof; the start time
  stays the pid file's identity check.

### the two open rules (the operator's)

Two rules of this note are not true of 3.0.0 as merged. Each is the
operator's to rule on; neither is on the room's engine.

1. **`room/member.go` holds a `sync.Mutex` over its transports.** The slice
   is appended by the accept loop and the join dials and read by `Close` —
   three goroutines, none of them the room engine, so the engine's contract
   is untouched. Keep it (my reading: it guards a genuinely multi-goroutine
   list and removing it restructures merged 3.0.0 code for style's sake), or
   rule it a lock above the seams and move the bookkeeping onto the room
   engine.
2. **The children's engine polls** (the port probe at 100 ms, the wait while
   `/health` says 503, the stop grace's 10 ms). The world's readiness has no
   event to wait on — a port answering, a 503 lifting, a child dying
   mid-grace are world facts with no signal — and the children's engine is
   the third consumer 3.0.0 built for exactly this waiting. Keep them (my
   reading: a poll on the children's engine is the honest mechanism and it
   never holds the room engine), or name the events and pay the machinery.

### the tests, named

evt v0.4.0 ports lift's suites and keeps their claims where the machinery
they ran survives: the reorg path (a late arrival unwinds, splices and
reapplies; the same-tick tiebreak decides identically on every peer;
staleness is refused, not reordered), the storm's convergence claim (7
peers, shuffled, contended, duplicated delivery, identical admitted order —
its version-slot claim leaves with the CAS), and the capstone's shape (three
peers on real TCP sockets, keys minted from a live VDF clock, converged
byte-identical order). **The quorum suites leave**: the cluster's five
invariants, the recovery round, the frontier's characterizations and the
guarded CAS cluster — their machinery stays in lift. Two suites are new:
**one runs the same scenario through both rooms and compares the order** —
the admitted sequence must agree, not the latency — and **one shows a peer
cut off from the home machine is never served**.

Rig's, across processes on loopback standing in for machines (step 5): two
"machines", each with its unix room and its door, agree on one order for the
keys the mesh carries; a claim is not acted on before the horizon (the
door's repost waits it); a peer cut off from the home machine is never
served, and its holds are released as acts; a remote claim enters the home
machine's room as the door's act at the remote claim's priority; the front's
wait gets its test (a mint served when the fold shows it, ended by the
release act or the caller's context); the door handover leaves the room
findable (the re-bind and the gossip re-dial); and the unix room's wire is
byte-for-byte 3.0.4's with the setting on and off.

### the mechanics

`evt` gains lift's inter-system half as v0.4.0 on the branch `evt-0.4.0`
(main d7a18a9), finality first — every v0.3.0 test passing untouched — then
the port. Until the operator tags v0.4.0, rig builds against the local evt
through an untracked `go.work`, as 3.0.0 did; the `go.mod` requirement moves
to v0.4.0 and the `go.work` goes in the last commit. rig's work runs on
`feat/3.1.0-mesh` (the note and its amendment are its commits so far), a
commit per step, pushed as it lands, a PR in each repository for the
operator at the end. evt imports
only the standard library; rig takes no new dependency and no cgo; no
comments in Go code; no Co-Authored-By or generated-with lines.

### what the mesh does not name

A WebRTC transport (its own module later, `rig serve` the rendezvous that
introduces two peers and steps out — the one future case where joining needs
another process, and what makes a browser or a peer outside the tailnet a
member). Byzantine peers. The unknown-order VDF group: RSA-2048 is wired and
measured (2.5 s a tick) and the 1536-bit prime stays the default. Turning
`mesh` on for daily use, which is the operator's step after this ships.
