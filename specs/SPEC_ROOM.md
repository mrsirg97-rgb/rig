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

The room crosses machines. One protocol, two modes. `ipc` is 3.0.0 and stays
the default: the instances of one machine over unix sockets, a claim final
when every live member has acknowledged it. `mesh` is a setting: peers on
more than one machine, ordered by verifiable time, converging by gossip.
Rig's code above the seams does not know which mode it is in. `mesh` ships
whole and tested and is off until the operator turns it on. This note is
step 1 of 7; steps 2-7 start on the operator's word, in a later session.

The inter-system half exists: `~/Projects/lift/broadcast`, left out of 3.0.0
on purpose — the VDF clock (Wesolowski proofs), the sequencer's freshness
horizon and reorg path, the quorum commit and recovery, the TCP transport
with pooled connections and quorum-counted acks, gossip by set difference
over fixed-width summaries, the WAL. 2,619 lines without tests, 1,364 of
tests, standard library only; its storm test runs 7 peers under shuffled,
contended, duplicated delivery with zero divergence, and its capstone runs
three peers on real TCP sockets with keys minted from a live VDF clock. Lift
remains the reference for meaning; the port conforms to evt's shapes.

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
mode branch in the claimant's body. mesh's rule goes behind the same
constructor, and nothing constructs it by default. The evt engine does not
change; the fold, the guard and the signals do not change shape.

### what mesh puts behind the three seams

**Finality: a quorum and a horizon, as lift's.** ipc's rule cannot cross
machines: on one machine a closed socket is a certain death, so
acknowledgement from every live member is safe; across machines a closed
connection may be a partition, and two sides that each believe the other left
would each admit a holder. The mesh's rule is lift's: an act is final when a
quorum of the room's machines accepted it **and** its tick is past the
freshness horizon — past the window inside which an earlier tick can still
arrive. Admission is provisional until then, the price lift names. The two
brakes are independent and both are load-bearing: the quorum is safety
against a partition (a side below quorum admits nothing, so a split never
holds twice), and the horizon is safety against a late earlier tick (an act
past the horizon is immutable — the fold's finalize — so what is acted on
cannot be evicted). **A claim is acted on — the request sent, the child
operation run — only past the horizon, never before.**

**The wait, as a number.** A claim minted at tick t is acted on when the
room's highest verified tick reaches t + window: the wait is about
(window + 1) tick periods, one for the phase of the mint, window for the
horizon. The tick period is the VDF's computation, measured on
battlestation today: 1M iterations cost **1.6 s** a tick on the 1536-bit
prime and **2.5 s** on RSA-2048, and a proof verifies in **0.5-0.6 ms** on
either modulus. The evidence's "about 18 s" figure is the MacBook's
computation (its verify anchor matches); the mesh's tempo follows the peers
that compute the chain, and the followers verify — so the GPU machine is the
natural pacer and the MacBook's slower arithmetic widens the room's wait only
if it paces. With window 2 the wait is about three tick periods: about 5 s in
front of every model request that mints a claim at the measured 1.6 s, about
54 s if the MacBook paces. Both numbers are the operator's to set: the
window and the iteration count are construction values in the mesh setting,
never literals, and they do not exist in ipc.

**The clock: the VDF.** The clock seam is evt's `Clock`
(`NewMinter(member, clock)`), and mesh puts the VDF clock behind it. The
chain is deterministic from the seed, so every peer computes the same
timeline and nobody can forge ahead of it: a tick is elapsed computation, and
any peer verifies it for half a millisecond. **The seed is the room's name**
hashed to the clock's zero — the room's name is its epoch, and two meshes on
one tailnet refuse each other's ticks, which makes the name the namespace
too. The iteration count is the delay per tick and the operator's knob. The
mint's step is the tick index (the ordering stays priority, then tick, then
member, then nonce — one `opLess` for both modes), and the act's envelope
carries its tick's proof, which every receiver verifies before it folds —
an unverified tick is refused, never trusted. The frontier (tick index →
output) is what a peer keeps to verify the next proof and what gossip
carries with the ops. The chain is computed off the room's engine: a producer
goroutine computes ticks and posts each finished one; the room engine never
spends 1.6 s holding an acknowledgement.

**The transport: TCP and gossip.** The transport seam is evt's `Transport`,
and mesh puts lift's TCP transport behind it: length-prefixed frames, one
dial per peer with pooled connections, quorum-counted acks, fail-closed
serve (a decode error or a rejected act acks 0), a dial timeout that is a
construction value — mesh's named bend, absent in ipc. The push, the pull
and the gossip ride one wire: a push carries acts and expects an ack, a pull
carries a watermark and expects a log back, a gossip carries a summary and
expects the difference. **Gossip reconciles by set difference over
fixed-width summaries**: 4096 buckets, one hash each, the width constant no
matter how large the log grows, so divergence in recent ticks lands in a few
adjacent buckets and the transfer tracks the diff, not the log. The round is
paced by the tick: each finished tick drives one round — the VDF's cadence is
the interval, and no timer of its own is built. **The WAL is the log each
peer keeps**: Append, Replay, Compact at the checkpoint boundary. It is what
a joiner is given (sync past a watermark, then gossip until the summaries
match), what a partition heals from (Sync, and Reconcile for the peer that
fell further behind), and what recovery re-drives (a coordinator that
vanishes between propose and commit strands its peers as pending, and any
survivor runs Recover — re-confirm quorum, commit, resolve). The dedup and
the idempotent fold make concurrent recoveries land exactly once.

**What does not come across.** Every structure is touched only on the
consumer: lift's mutexes on the sequencer, the node and the gossip core do
not join — the room's engine is the one consumer, so one goroutine touches
that state at a time. The transports keep their own connection bookkeeping,
as transports do today (they cross goroutines in both modes). No new mutex
in either mode; the evt engine does not change.

### the mode is chosen by a setting

The setting is `settings.json`'s `mesh`. Absent, nothing changes: no mesh
piece is constructed, the instance joins the unix room as 3.0.0 does, and
3.0.0's tests and its wire are byte-for-byte (the step-5 test pins the wire).
Set, it names the room and the room's shape:

```json
"mesh": {
  "room": "operator",
  "listen": "100.118.31.66:7780",
  "peers": ["100.118.31.66:7780", "100.120.230.71:7780"],
  "iterations": 1000000,
  "window": 2
}
```

- `room` seeds the clock (the epoch) and namespaces it.
- `listen` is this machine's door: the address an instance binds when it is
  free; an instance whose door is taken binds an ephemeral one. The door's
  holder churns with the instances; the next instance to start reopens it.
- `peers` is the room's doors, the same list in every settings.json of the
  room. A join needs one working address: the dial makes it mutual and the
  `Seated` answer carries every member's address, machine and id, so the
  room's address set propagates and any one working door reaches all of it.
- `iterations` and `window` are the clock's and finality's numbers above.

In evt, the mode is what is constructed, never a global: the application
builds the mesh room's clock, transport, sequencer, WAL and gossip in its
constructor, the way it builds the ipc room's pipe transports today. Rig's
`room.Member` interface is the seam above: the mesh member implements it, and
`cmd/rig`'s claim path, the children, the wire front and the updater run
unchanged above it. No second code path above the seams.

### what a mesh peer and an ipc room do when they meet

They never share a room. One instance is a member of one room: with the
setting absent it is an ipc member and binds no TCP listener; with it set it
is a mesh peer and binds no unix socket. The concrete meetings:

- **A mesh setting naming an ipc-only machine.** The dial refuses; the
  joiner joins with who it reaches. The quorum counts the room's configured
  machines, so a joiner that cannot reach its peers does not act alone — the
  same stall a partition buys, never a silent second holder.
- **Two homes on one machine, one in each mode.** Disjoint rooms, disjoint
  transports; the member ids cannot collide inside a room because each
  room's members are its own processes (the id hashes the machine identity,
  the process id and the start time).
- **The room's own subprocesses.** The scheduler's `run-job` and
  `rig -update` build their members from the same home's config, so they
  join the room their home is in; there is no third mode.

### how peers on other machines are found and trusted

The trust boundary is the tailnet: a mesh listener binds the tailnet address
only, never 0.0.0.0, so an off-tailnet dial cannot reach it. Inside the
boundary, the order's integrity is the VDF: a peer cannot forge the
timeline, an unverified tick is refused, and the room's name keeps two meshes
from joining each other. The join carries ipc's order over TCP: **the
listener binds before the first dial**, so two instances starting together
end in one room; the joiner dials a door, the dial makes it mutual, and the
`Seated` answer introduces the rest. The member id is the same 62-bit
`MemberOf` over the machine's identity, the process id and the start time,
re-derived on a collision; a collision re-derives in process and the instance
does not exit.

**The quorum counts machines, not instances.** The instances churn (a
session, a job, an update), so a member-count quorum would be a moving
target; the machine is the fault domain the mesh exists to cross. The room's
machines are the distinct addresses in `peers`; an act is accepted by a
machine when one of its live instances acknowledges it (every instance folds
the act regardless; the ack is the machine's presence). With two machines a
quorum is both — **the price is named: a sleeping MacBook freezes the shared
keys until it wakes, because neither side is a majority.** A third machine
turns that into 2-of-3. The operator turns mesh on for daily use after this
ships; that choice is theirs, and the stall is what they are choosing.

**Liveness in mesh is quiet past the window, not the socket.** A member whose
connections closed and whose last seen tick sits more than the window behind
the horizon is treated as gone: its holds are released for by whoever sees
it, and if it returns its stale acts are refused and it claims again. A
member that is connected and silent holds the room, as ipc's does, until its
quietness passes the window.

### how a remote peer reaches the model

A remote peer that wins a claim sends its request to the GPU machine's wire
front (`/v1/chat/completions`): the row on the peer's machine is a remote row
(`baseURL` names the front, the port is 0, there is no child half), and the
provider's endpoint rule already sends it there. With the mesh setting on,
the front binds the tailnet address beside loopback (the dashboard and its
token gate stay loopback; the front was specified for loopback and now the
mesh's callers are the machines).

**The front does not claim a second time for a caller that already holds the
claim.** The caller's claim mint rides the request (one header); the front
resolves it against its own fold's holder set for the key: a mint that holds
the key is served as is, a mint that does not is refused, and a request with
no mint claims at the floor as today's loopback callers do. The gate exists
for the measured failure, not for ceremony: before 3.0.2 a worker claimed the
model, sent its request to the front, and the front claimed the same key
behind the worker's own hold — the job waited on itself for 27 minutes. The
mint is not a cryptographic identity; the tailnet is the boundary and the
holder set is the authority. `/running` and `/unload` stay the front's: the
resident set reads the local ports, and an unload's stop operation rides the
mesh's order like any act.

A remote peer's requests are recorded by the front — the instance that
served them — so they appear in the served table of the machine that holds
the model, as step 4 requires.

### what a remote peer reads

A peer on another machine can read none of the GPU machine's ports or files,
and its own requests land in its own machine's served file. What it needs it
reads from the room's own log:

- **What is resident**: the child operations pinned on the row's key in the
  WAL — the last start or swap with no stop after it. The log is the mesh's
  world-read; the row's port stays the residency proof on the machine that
  holds it, and the two never disagree because only that machine's members
  mint the child operations.
- **How many places a row serves**: the row's machine's answer, carried by
  the table change acts in the log. The fold's `places` in mesh reads that
  projection, not the local file — one answer per key, or the members
  diverge. The local file feeds only the local half (command, environment,
  port, grace) on the machine that runs it.

A joiner in mesh mode is given the same two things ipc gives it — the
checkpoints and the acts still waiting — plus the log to rebuild the table
projection from, which is the world it cannot walk to.

### who may own a child operation

The members on the machine the row's command runs on. 3.0.0's rule — the
act's owner is its origin — holds, and the mesh adds the one thing it must:
a child operation's origin sits on the row's machine. The claim's admission
is visible to every member in the order; the row's machine's members, and
only they, mint the start or the swap when the row's port is silent, because
only they can read their own port. The fold knows which members those are
from the member's own table: a row with a command and a port is a row this
machine runs, and the mesh's fold refuses a child operation whose origin is
off that machine (the check rides the same injected table the `places` func
reads — a constructor argument, never a global). Two members of the row's
machine may both mint a start; the world reconciles them exactly as 3.0.0's
two-instance race does — one child runs, and the pid file names it.

### the second machine's /proc fallbacks

rig builds for darwin (verified: `GOOS=darwin` for arm64 and amd64). Two
things 3.0.0 reads come from `/proc`:

- **The member id's boot id and start time** (`room/member.go` falls back to
  the hostname and a start time of zero): **enough**. The id's job is
  uniqueness among the room's live members and across a restart; the
  hostname is unique on the tailnet (battlestation, mrsirg-macbook) and the
  process id separates the instances of one machine and changes across
  boots. A hostname macOS renames only moves the id across boots — a new
  member, which the mesh absorbs by the log.
- **A child's start time in the pid file** (`children.go` `startOf` returns
  zero): **not enough by itself.** The check degenerates to alive-only, and a
  recycled pid that is alive but is not the child stops the spawn and spins
  the port probe — a wedge, reachable on darwin. The structural guard needs
  no start times: **the pid file is believed only while the row's port
  answers** — the one residency shape, applied as a second brake. That fix
  is one rule on both machines and it belongs in step 3.

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

evt v0.4.0 ports lift's suites and keeps their claims: the reorg path (a
late arrival unwinds, splices and reapplies; the same-tick tiebreak decides
identically on every peer; staleness is refused, not reordered), recovery
(a stranded round resolves leaderlessly; concurrent recoveries land once),
the cluster (quorum commits, a bare majority commits, below quorum applies
nothing, exactly-once, the VDF-ordered replication), the frontier
(the coordinator that vanishes mid-round), the storm (7 peers, shuffled,
contended, duplicated delivery, zero divergence) and the capstone (three
peers on real TCP sockets, keys minted from a live VDF clock, converged
byte-identical). Two suites are new: **one runs the same scenario through
both modes and compares the order** — the admitted sequence must agree, not
the latency — and **one shows a partition healing without two holders**.

Rig's, across processes on loopback standing in for machines (step 5): two
"machines" with two instances each agree on one order; a claim is not acted
on before the horizon; a peer cut off and reconnected converges and no place
ever has two holders; a start owned by a member on the other "machine" runs
the child on the row's machine and nowhere else; and the setting absent is
byte-for-byte 3.0.0's behavior on the wire.

### the mechanics

`evt` gains lift's inter-system half as v0.4.0 on the branch `evt-0.4.0`
(main d7a18a9), finality first — every v0.3.0 test passing untouched — then
the port. Until the operator tags v0.4.0, rig builds against the local evt
through an untracked `go.work`, as 3.0.0 did; the `go.mod` requirement moves
to v0.4.0 and the `go.work` goes in the last commit. rig's work runs on
`feat/3.1.0-mesh` (this note is its first commit), a commit per step, pushed
as it lands, a PR in each repository for the operator at the end. evt imports
only the standard library; rig takes no new dependency and no cgo; no
comments in Go code; no Co-Authored-By or generated-with lines.

### what the mesh does not name

A WebRTC transport (its own module later, `rig serve` the rendezvous that
introduces two peers and steps out — the one future case where joining needs
another process, and what makes a browser or a peer outside the tailnet a
member). Byzantine peers. The unknown-order VDF group: RSA-2048 is wired and
measured (2.5 s a tick) and the 1536-bit prime stays the default. Turning
`mesh` on for daily use, which is the operator's step after this ships.
