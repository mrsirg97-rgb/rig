# rig: the room (3.0.0: no center)

Every rig instance on the machine is a member of one room over unix sockets,
and no process is the center. Each instance keeps its own loop and two
engines — the turn loop's, as today, and the room's; the room is the thing
the room engines agree on. The model is a guarded key in that
room: an instance sends its own request to the model, itself, when its claim
is first in the order every member agrees on and every live member has
acknowledged it. What is loaded, a swap and an update are operations in that
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

The source of the primitive is `~/Projects/lift`'s `broadcast` (rig's own was
lifted from it at 2.11.0 and left the half behind: "one slot needs no
quorum"). Lift is the reference for meaning, not for style.

## what comes from lift, meaning unchanged

- **One total order, applied identically on every member, with no leader.**
  The sequencer applies operations in verified clock order, not arrival
  order; a late operation that belongs earlier takes its rightful place (the
  reorg path); staleness past the horizon is refused, never reordered.
- **The version chain and its compare-and-swap.** A write carries the parent
  version it branches from and the version it becomes; the guard admits it
  only if the parent matches the key's committed head, and atomically swaps
  the head forward. Two writers branching from the same version race one
  compare-and-swap: exactly one wins, the other is refused, never applied.
  The version chain is the authority; no lock is held by anyone.
- **Duplicates refused.** An operation already applied is a no-op, keyed by
  its mint identity. This is what makes recovery safe and duplicates
  harmless.
- **Recovery by any peer.** A stranded operation (proposed, acknowledged,
  abandoned by its proposer) is re-driven by any surviving member; the dedup
  makes concurrent recoveries land exactly once. `Reconcile`'s shape — replay
  a peer's log to converge — comes across for a member that joins late.
- **Keys minted as tick, member, nonce.** Identity is time plus who you are;
  both are already free.

What does not come across: the VDF clock, the quorum, the freshness window
and eventual finality, the TCP transport, gossip and its summaries, the WAL,
and a `mesh` setting — 3.1.0's concerns. Neither do lift's comments or its
mutexes.

## the three seams

Finality, the clock and the transport are seams, because 3.1.0 puts lift's
other choices behind them.

- **Finality** is the acknowledged claim (below). Lift's eventual finality —
  a tick is past the freshness window, so no earlier tick can still arrive —
  is replaced by every live member having acknowledged the operation.
- **The clock** is the machine's clock, wall-anchored and monotone within
  the process (the order names the read). Nothing above the clock may
  assume it is the kernel's; 3.1.0's VDF is a clock swap, not a rewrite.
- **The transport** is a unix socket. Nothing above the transport may ask for
  a file descriptor, an address family or a listener; the `Transport`
  interface (`Send` with an ack, `Recv` with a callback, `Close`) already
  holds the shape, and the unix transport lands beside the pipe.

## membership: the directory is the registry

- The room's home is `~/.rig/room/`. It holds one unix socket per member,
  named by the member's id and the process id. **The listing is the
  membership.** There is no registry process; a directory is not a process.
- A member's id is `MemberOf` over the hash of the machine's boot id, the
  process id and the process start time, at **62 bits** (the top bit clear
  keeps it positive in a signed int64). Lift's 12 bits do not survive the
  arithmetic: with eight members live, one join in about 512 collides
  (8/4096), and rig spawns hundreds of short-lived workers a day, so exiting
  on a collision would be an operational event. At 62 bits a joining member
  collides with one of eight live members with odds of 8/2⁶³ ≈ 10⁻¹⁸, and a
  thousand joins across a boot still leave it near 10⁻¹⁵. A collision with a
  live member re-derives in process — the hash takes a counter — until the
  id is free among the directory's members; the instance does not exit. The
  room id is the rig home's: one machine, one home, one room.
- **Joining**: an instance creates and listens on its own socket **before it
  reads the directory**, so of two instances starting together at least one
  sees the other. It then reads the directory once, dials every socket on
  it, and announces itself; the accepted ends dial back, so every pair holds
  one connection. A joiner does not mint until it has connected to every
  listed member and received each one's snapshot, and a member counts toward
  finality from the moment its join is complete.
- **A stale file is judged by the process, never by a refused dial alone.**
  The file's name carries the process id, so a joiner checks whether that
  process is gone: a process that has bound its socket and not yet begun
  listening refuses a dial too, and removing a live member's file hides it
  from later joiners — two halves of the room could then each grant the same
  place. The joiner removes a file only when its process is gone.
- **Liveness is the connection.** A member is gone when its connection
  closes; the close is an event on each member's room engine. A graceful
  exit removes its own socket file; a killed process's file lingers until a
  joiner judges its process gone.

## the order

The order is **priority first, then tick, then member**:

```
(priority desc, tick asc, member asc, nonce asc)
```

- Priority is the room's table (below). Tick is the machine's clock read as
  the wall clock taken once at process start (`UnixNano`), plus the
  monotonic time elapsed since that read. Go's monotonic reading counts from
  each process's own start and the standard library exposes only differences
  of it, so an epoch anchored at start is not comparable across processes: a
  process up for a second would mint smaller ticks than one up for an hour,
  and within a priority the newest process would always sort first while a
  long-lived member waited behind every new one. The wall-anchored sum never
  goes backwards within a process, it is comparable across processes to
  within the wall clock's accuracy at their starts, and it is the standard
  library on both platforms — the runtime reads
  `clock_gettime(CLOCK_MONOTONIC)` through the vDSO on Linux and
  `mach_absolute_time` scaled by `mach_timebase_info` on macOS for the
  elapsed part. **The clock seam returns this tick.** The tick travels in
  the operation, so the order is agreed whatever the clocks say: every
  member sorts by the tick the operation carries. What the axis owes the
  wall clock is fairness only: a wall-clock step between two processes'
  starts can misorder their claims within one priority by the size of the
  step — fairness, never safety — and a tie at one nanosecond is broken by
  member and nonce, so the order is total regardless. Member is the 62-bit
  member id; nonce separates a member's mints within a tick.
- Lift orders `(tick asc, id asc)` where lift's `Priority` *is* the tick; the
  meaning — earliest verified time wins within an axis — is unchanged: the
  mint's id sorts by tick, then member, then nonce, and priority leads. One
  axis was added; nothing else moved.
- The tick is nanoseconds, so lift's 63-bit packing cannot hold it. The
  operation carries the mint as its fields; the packing is lift's
  implementation detail, not its meaning.
- The operation is minted and published in one engine step: the closure that
  publishes mints the tick. An operation exists in the room exactly when it
  exists at all — there is no minted-but-unpublished window, which is what
  makes the acknowledgements honest.
- Duplicates are refused by the full mint identity (tick, member, nonce).
- The fold's checkpoint is finality's line, not a tick window. Priority leads
  the order, so a future mint can precede older operations and no tick-age
  rule can bound the provisional tail; only finality pins (below). The fold
  checkpoints the pinned prefix and refolds the operations still awaiting
  acknowledgement.

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

All room state — the ordered log, the guarded heads, the holder sets, the
resident set, the live members — is touched with no mutex. The room engine is
the one consumer (SPEC_EVT's contract), so one goroutine touches the fold at
a time. A mutex around state that only the consumer touches is machinery for
its own sake, and lift's mutexes do not come across. The consequence is
stated plainly: a structure the room holds is touched only by the room
engine's consumer, and the structures that cross goroutines are the ones
that already do (each engine's queue, the transport's wire).

## the acknowledged claim

- A member's room engine processes each arriving operation and answers, on
  the same connection, an **acknowledgement**: the operation is first in the
  receiver's order for the operation's key among the operations the receiver
  holds — with two withholds. A member whose own claim on that key is final
  or in flight does not acknowledge a **conflicting** claim — one racing the
  same version slot — until its release; a member whose own claim precedes
  the arrival does not acknowledge it either. That is what makes two
  conflicting claims unable to both collect every acknowledgement. A holder
  acknowledges a claim for the key's next slot freely, which is what lets a
  strictly higher claim enter while one is in flight. Withheld
  acknowledgements re-evaluate on arrivals — a re-evaluation is caused by an
  operation landing, never by a timer or a poll.
- **A claim is final when every live member has acknowledged it.** The
  acknowledgement set follows liveness: a member that leaves mid-claim
  shrinks the set; a member that joins mid-claim is in it (it receives the
  operation in its join snapshot and acknowledges from its room engine).
- **What the members fold is the admission, not the claim.** A claim alone
  does not put its member in the holder set. When a claim is final and its
  compare-and-swap took the version slot, the claimant publishes its
  **admission** — an operation like any other — and every member folds
  admissions in the same order, so every member's holder set is the same.
  The request follows the admission.
- **A final operation's admission is pinned.** The fold never moves a final
  operation's admission. This is the one deliberate change from lift's fold:
  lift's retroactive eviction (a late earlier-tick write evicts the winner
  that only won by arriving first) is replaced by refusal-and-re-mint. The
  price: a late operation whose version parent went stale loses its race and
  mints again from the advanced head — one more operation in the order, no
  timer. The gain is the invariant this release owes: **nothing is ever
  acted on and then reordered.** An in-flight request's holder cannot be
  evicted by an arrival.
- So the acknowledgement is the safety gate and the pin is the guarantee:
  acting on a claim that is not final is unsafe (a later arrival could still
  take its slot in the fold); acting on a final claim is permanent, because
  the admission is published, folded everywhere, and pinned.

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
  (the terminal event of the stream — see step 6 — mints the release, the
  next version on the chain).
- A claim whose compare-and-swap is stale — it lost the version slot to an
  operation that arrived earlier in the order — is refused and re-mints from
  the advanced head.
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
  and the close is the event. The members drop it from live; its claims:
  **any member releases on its behalf**, and the guard makes the duplicates
  harmless (several members may mint the same release; the compare-and-swap
  admits exactly one per version slot; the rest are refused, no-ops).
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
  instance whose claim wins starts the child, **detached** (a new session:
  it outlives that instance), and **what is resident is state every member
  holds**, so any of them can stop it later.
- **Residency is recoverable from the world, and by one path.** When the
  last rig exits, the detached child keeps running and the next instance
  joins a room of one with nobody to ask — so the row's fixed port answering
  is the proof. A joiner that finds no members probes each row's port once,
  before it may start anything: a port that answers is a resident model, and
  the joiner starts nothing. The probe happens once, at join, and reads the
  same thing every member's holder set reads — the child's port. No state
  file beside it: one path, or the two disagree.
- Two instances racing to start a model: both mint the start operation; the
  compare-and-swap admits one child. The loser's claim re-mints from the
  advanced head and finds the model resident.
- **Stop sends SIGTERM and waits the model's grace.** Maya saves its
  conversation on SIGTERM and needs up to about 90 s; the row's stop grace
  carries that number, not a constant in code.
- **A change to the table is an operation in the order, never a kill
  mid-reply.** The winner writes the file and publishes the change; every
  member applies it to its own table. The change's admission waits for a
  boundary, like a swap.
- The child's output goes to `~/.rig/logs/<model id>.log` (the operator's
  user owns it — the root-owned files llama-swap left are the migration's
  finding, named in the changelog with the command to fix it), because a
  detached child needs somewhere durable to speak and the dashboard streams
  it.

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
  resident or loading;
- **model logs** — each child's output, streamed from the log file while the
  page is connected;
- **system stats** — GPU utilisation, temperature, power and memory; CPU use,
  RAM, load average, network; CPU and board temperatures.

Sources are pure Go and need no packages: `/proc`, `/sys/class/hwmon`, and
`nvidia-smi --query-gpu` as CSV. Sampling runs only while a page is
connected; a source that is missing does not render (the box has three GPUs;
a box with none must not render a GPU row).

## the module and the branch mechanics

- rig's module path becomes `github.com/mrsirg97-rgb/rig/v3` (2.18.0 →
  3.0.0): `go mod edit -module` plus the import rewrite across the tree; it
  rides the first rig commit so the branch is coherent from commit one.
- While evt 0.3.0 is unpublished, rig builds against the local module through
  an untracked `go.work` (it is in `.gitignore`). rig's `go.mod` moves to
  v0.3.0 once the tag exists, with no `replace` and no `go.work` committed.
- The engine's six files (`clock.go`, `closure.go`, `engine.go`, `event.go`,
  `queue.go`, `scheduler.go`) do not change; the room needs nothing from
  them. `loop/loop.go` is opened by name for step 6 only, to remove the
  2.17.1 branch; `loop/batch.go` and the kernel's priority table stay
  closed. `specs/FREEZE.txt`'s `reopen loop/loop.go 2.17.1` line leaves with
  it.

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
evt/broadcast (v0.3.0, the ordering primitive joins broadcast)
  mint.go        MintKey, KeyTick, KeyMember, KeyNonce, MemberOf, Minter
  sequencer.go   Sequencer: the total order, duplicates refused, reorg
  guard.go       Guard: per-key compare-and-swap over the version chain
  guarded.go     the guarded fold, with the version seam
  claim.go       the acknowledged claim: finality, pinning, liveness
  signal.go      the protocol's messages: claim, release, acknowledgement, admission
  room.go        the registry it is; the claim moves out of it
  socket.go      the unix socket transport, beside the pipe
rig (3.0.0, on feat/3.0.0-no-center)
  room/          the instance's member: join, rig's own signals, the table
  models rows    command, environment, port, aliases, stop grace, places
  child.go       start/stop/swap as guarded operations, detached children
  provider       the row's port is the endpoint; the terminal event on cancel
  loop/loop.go   the 2.17.1 branch leaves; the loop appends on Done only
  serve          the wire front: /v1/chat/completions, /v1/models, /running, /unload
  dashboard      the room, model logs, system stats pages
```

Every exported type is an interface over a private struct and a constructor
returns the interface, as `Engine`, `Queue`, `Room`, `Cap` and `Registry`
are; only what an application must build or embed stays concrete, as `Mark`
and `Notice` do. The payload is `evt.Signal`, never a new word for it, and
**evt owns the protocol's messages**: the claim, the release, the
acknowledgement and the admission are evt's signal types, because evt's own
tests need them and a second application must not have to invent them; rig
declares only its own — the resident snapshot, the child operations, the
table change, the update, the join — declared in the wire's registry, each
embedding `Mark`. No comments in Go code; no new dependencies; no cgo.

## tests

Ported from lift, keeping their claim: the guard's three (the conflicting
write refused; deterministic across replicas; independent keys), the guarded
suite's two (mutual exclusion; retroactive eviction — the primitive keeps
lift's fold, the pinning is the acknowledged-claim layer's), and the storm:
seven peers under shuffled, contended delivery with duplicates, zero
divergence in order, state and guard outcome — run against this release's
finality, over real unix sockets, with the peers' delivery shuffled the way
the wire shuffles it.

New to evt: a claim is never acted on before it is final; a member that
leaves mid-claim does not hold the others; priority outranks arrival; a 90
minted after a 30 became final on a full key is not admitted until the 30
releases; under shuffled delivery of two conflicting claims, every member
ends with the same single holder; two members started a known interval
apart mint in real-time order, not in order of process age.

Rig's own, against a fake model server and several members over real sockets:
three members claim at once and enter in priority order; never more holders
than the places and its one entry, and the entry is strictly higher than
every holder; a strictly higher claim passes the places' worth of holders as
that entry and lower ones wait for places; a member whose loop is blocked in
an inline write still acknowledges, and another member's claim becomes final
while it is blocked; a holder killed outright does not stop the next; a
member that joins late agrees with the rest; two instances starting at the
same instant end in one room; a member slow to listen is not removed; a
claim racing a join never yields two holders; two instances race to start a
model and one child runs; an instance that started a model exits and another
stops it; start a model, end every instance, start one instance: it finds
the model resident and starts nothing; an update drains to turn boundaries
before ending anything. The provider's terminal event on cancel keeps
2.17.1's tests passing with the branch gone.

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
5. **The pin at finality replaces lift's retroactive eviction** — a stale
   claim re-mints instead of evicting an in-flight holder. This is the one
   place the primitive's fold and this release's finality touch, and it is
   the sentence that makes "nothing is ever acted on and then reordered"
   true.
6. **The model logs land in `~/.rig/logs/<model id>.log`** — a detached
   child needs somewhere durable to speak; the dashboard streams the file.
7. **`settings.json`'s `swapUrl` retires** with llama-swap; the provider's
   endpoint comes from the row's port.
