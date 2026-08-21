# rig: the event loop (libevt, Go-centric)

rig's turn loop is sequential by construction: a turn's tool calls run
one after another, each awaited, the events emitted in that order, and
every participant (the recorder, the guard, the approval gate, the
compaction anchor) leans on that order. That is the right property —
one thread touches the session — and the wrong mechanism for the day the
agent wants five `read`s at once, or an operator on the phone steering
while a delegated worker reports back, or a timer firing into a live
turn.

The fix is not to make the turn loop concurrent. It is to make it an
**event loop**: one consumer, many producers, work arrives as closures
ordered by priority then arrival, and the consumer is still the only
thing that touches the session. Parallel tool calls are goroutines that
*post their completion*; the loop applies completions in call order;
nothing needs a lock except the queue.

That loop already exists, in C, in `~/Projects/libtrdr` (`libevt`): a
4-ary max-heap queue of events ordered `(priority desc, timestamp
asc)`, an engine that pops and executes one closure at a time under a
CAS busy flag with multi-producer adds and an idle poll, a context that
is a scope plus a function, and a scheduler that is the thread harness.
This spec takes that **exact shape** and makes it Go-centric: the five
parts keep their names and contracts; the mechanism becomes what Go has
for it. Phase 1 ships the package and its tests. Phase 2a (decision 6) puts
the batch in the turn loop — concurrent reads, ordered emission — the
named reopening of the frozen loop. Phase 2b (decision 7) makes the
loop the engine's consumer: every step of a turn is an event on one
goroutine; everything that waits on the world posts.

## goals

- A leaf package, `evt`, stdlib-only, with libevt's five parts as Go
  types of the same names: `Context`, `Event`, `Queue`, `Engine`,
  `Scheduler` — plus the `Clock` that mints ids.
- The queue's order, verbatim: priority descending, then id ascending
  (earlier arrival first). A 4-ary max-heap, as in C.
- The engine's contract, verbatim: a single consumer pops and executes
  one event at a time; adds are safe from any goroutine; executing
  happens outside the queue's lock (an event may add events).
- The scheduler's contract, verbatim: start spawns the loop, schedule
  is the multi-producer door, stop stops and joins; the C return codes
  become named errors.
- One addition, named: `Update(id, priority)` — reprioritize a pending
  event in place (the "latest steer wins" rule the TUI keeps by hand).
- libevt's tests, by name, in Go: basic order, empty pop, multithread,
  stress, perf; plus the Go-only cases (cancel, re-entrancy, update).

## non-goals (phase 1, named)

- **No loop change in phase 1.** `core/` and `loop/` were untouched;
  phases 2a and 2b are the named reopenings of `loop/` (`core/` stays
  frozen throughout).
- **No timers inside the engine.** libevt has none: a timer is a
  producer (a goroutine that posts at fire time), never engine state.
  Keeping the engine timer-free keeps it one thing.
- **No result channel.** Events are closures; a result flows back by
  the closure capturing where it goes (phase 2's completion events carry
  the tool result that way). The engine never learns what an event
  returns.
- **No fairness or starvation guard.** Priority means priority: a
  stream of high-priority events starves low ones, as in C. Phase 2
  assigns the priorities; it owns that consequence.
- **No drain on stop.** libevt's destroy runs one pending event (an
  accident of the C lifecycle); here stop runs nothing further, and the
  pending set stays observable (`Pending`).

## layout

```
evt/
  context.go    Context (the closure), Func
  event.go      Event, NewEvent, Execute
  clock.go      Clock, Counter (default), Monotonic
  queue.go      Queue, NewQueue: the 4-ary max-heap with the position map
  engine.go     Engine, NewEngine, options: WithClock, WithTick
  scheduler.go  Scheduler, NewScheduler, ErrNoEngine, ErrStarted
  PACKAGE.md
```

## the five parts, C to Go

| libevt | Go | the mechanism that changed |
|---|---|---|
| `Context { scope, future_fn }`, `context_resolve` | `Context` interface: `Resolve(ctx)`; `Func` adapts a `func(ctx)` | a closure is a closure; the `scope` is captured, not carried |
| `Event { id, priority, ctx }`, `event_execute`, `event_update_priority` | `Event` interface: `ID`, `Priority`, `Context`, `UpdatePriority`; `Execute(e, ctx)` | unchanged |
| `Queue` 4-ary max-heap, `push/pop/peek/view` | `Queue` interface: `Push`, `Pop`, `Peek`, `View`, `Len`, `Update` | the heap is the same; `View` drains a clone (sorted); `Update` is the addition, via a position map |
| `Engine { q, tick_fn, busy, running }`, CAS spin on `busy`, `engine_start` loop, `engine_add_event`, `engine_stop` | `Engine` interface: `Start(ctx)`, `Add`, `Update`, `Stop`, `Pending`; `sync.Mutex` + `sync.Cond` | the CAS spin is a mutex; the idle poll (`tick_fn` or `usleep(0)`) is `cond.Wait` unless `WithTick` names a hook; `Start(ctx)` also stops on `ctx.Done` |
| `Scheduler { engine, engine_th, spawn_mu }`, `start_loop` (-1/-2/-3), `schedule_evt`, `stop_loop` (join) | `Scheduler` interface: `Start() error`, `Schedule`, `Stop`, `Done`; `ErrNoEngine`, `ErrStarted` | the pthread is a goroutine; the codes are errors; `Stop` joins through `Done` |
| `clock_step()` = monotonic ns | `Clock` interface: `Step() uint64`; `Counter()` (default), `Monotonic()` | see decision 2 |

## decisions

### 1. The exact shape; the mechanism is Go's.

The five names, the five contracts, the order, the single consumer and
the multi-producer door are libevt's, so the design survives the
language and the C stays the reference. What does not survive is the
machinery Go already provides: the CAS `busy` spin (a `sync.Mutex` with
a `sync.Cond` for the consumer's wait), the idle poll (`cond.Wait`,
woken by `Add` and `Stop`), the pthread harness (`go engine.Start(ctx)`
with a done channel), the `scope` pointer (a captured variable), the
malloc/free lifecycle (the GC). Rejected, named: porting the spin (a
spin in Go burns a P and the scheduler already parks goroutines);
porting `engine_destroy`'s one-event drain (an accident, not a
contract); five interfaces with five implementations behind DI seams
(lift's law — the package is consumed as `NewEngine` and `Add`; the
interfaces exist so phase 2's loop can be faked, not for swapping).

### 2. The order is (priority desc, id asc); the id is a counter.

libevt mints the id from `CLOCK_MONOTONIC` nanoseconds, which makes the
id both the arrival order and a timestamp. In Go the default is a
strictly increasing atomic counter (`Counter()`): unique by
construction, arrival-ordered, and never colliding the way two adds in
the same nanosecond would (the C accepts the collision; the tie then
falls to heap position, which is not arrival order). `Monotonic()` is
offered for the case that wants a real time on the id, named with the
collision caveat. The engine clamps a negative priority to 0, as C does.

### 3. Update is the one addition.

A pending event's priority can change in place (`Queue.Update`,
`Engine.Update`): the heap keeps a position map (id → index) so the fix
is `O(log n)` and a miss is a named `false`. This is the "latest steer
wins" and "an interrupt outranks what it interrupts" rule phase 2
needs, and libevt lacks only because its events are fire-and-forget.
The map costs one entry per pending event; `View` and the heap
invariants are unchanged.

### 4. Execution happens outside the lock.

The consumer pops under the lock and executes with it released, so an
event may `Add` (a tool completion scheduling the next step), may block
on I/O, and may run long without holding producers out. libevt does the
same (the `busy` flag is released before `engine_process_event`). A
panic in an event is not recovered: the closure's author owns it, as in
C where a bad `future_fn` takes the process. Phase 2's completion
closures are the loop's own code, so this is the loop's discipline, not
the engine's.

### 5. Stop is quiet; cancel is stop.

`Stop` sets running false and wakes the consumer; the event in flight
finishes, nothing further runs, and `Pending` shows what was left
(sorted, a snapshot). `Start(ctx)` returns when `Stop` is called or ctx
is done — the ctx is the Go-centric stop, so a scheduler's `Stop` is a
cancel plus a join on `Done`. Named: a second `Start` on a running
engine refuses (`ErrStarted` at the scheduler; the engine itself is
single-consumer by contract and does not guard against two `Start`s —
the scheduler is the door that does).

### 6. Phase 2a, the batch — built.

The turn loop's L5 ("for each call, in order: start, execute, result,
append") becomes the batch (`loop/batch.go`): the kernel carries a
predicate, `Concurrent(call) bool`, and a bound, `Parallel`. Walking the
calls in order, a run of consecutive admitted calls is dispatched as
goroutines (at most `Parallel` in flight, default 8); a refused call is
a barrier — everything before it has been awaited, it runs alone on the
loop goroutine, and the calls after it wait. Emission never changes
shape: for call *i* the loop emits `ToolStart`, waits for *i*'s result,
emits `ToolResult` with *i*'s own duration, appends *i*'s tool message.
Results land in the order the model asked, whatever order they finished;
the bracket per call is the same bytes the CLI reference already has, so
no frontend, recorder, or golden changes. A nil predicate is the loop of
0.11, byte-for-byte.

The root's predicate is **narrower than "not mutating"**: the pure reads
— `read`, `ls`, `find`, `grep`, `web_search`, `web_fetch`, `diff` — and
nothing else. `todo` and `rem` write SQLite (serialized transactions
would collide inside one batch), `python` and every plugin share one
kernel, `bash`/`write`/`edit`/`scheduler`/`delegate` have effects whose
order the model chose. Rejected, named: reordering execution (a `bash`
that ran, ran — only emission is ordered); `!isMutating` as the
predicate (the approval gate's notion, not a concurrency-safety one);
a settings key for the bound in this phase (the default is the loop's;
a key is a config round when lived use asks).

What sequential delivery used to buy, and who now pays: `guard.Bound`
keeps its maps under a mutex (the duplicates in one run may all execute
— each passed the check before any had failed; they are not retries,
and the bound strikes the re-issuance after, a named case); the file
tool's `Session.Files` writes go through a package mutex (the session
type stays frozen; the tool that writes it locks); `toolset` was already
an RWMutex; `perm` is stateless; the approval gate only ever asks for
barriers, so asks stay one at a time while reads run. The recorder and
every `Notify` stay on the loop goroutine.

Not the engine. The batch needs an indexed wait (call order is known
before dispatch), not a priority queue; `evt` is phase 2b's spine, and
using it here would be machinery for its own sake.

The gate: `frontend/tui/freeze_test.go` carried a named `reopened`
clause for `loop/` and `kernel.go` under this deliverable; the re-freeze
PR after the merge deleted the clause, and the gate measures the new
bytes against the next fork point. Not the `-refactor` branch bypass.
The form, for every future reopening: open by name in the PR that
changes the loop, close by name in the PR right after its merge.

### 7. Phase 2b, the loop as the consumer — built.

`loop.Run` is the engine's consumer: it wires the tools and the chain
as before, posts the first prompt, and blocks in `engine.Start`. Every
step of a turn is a closure executed on the loop goroutine; everything
that waits on the world is a producer goroutine that posts:

- **The prompt.** `prompt` mints the turn (its context, its interrupt
  handle) and spawns a producer that calls `Frontend.Input` once and
  posts the line (priority 90). The prompt is only ever posted between
  turns, so the Frontend's contract is unchanged: one `Input` per turn,
  the steering slot and the interrupt handle exactly as SPEC_CORE has
  them.
- **The model.** `model` runs `Assemble` and opens the stream on the
  loop goroutine (a compaction's summary call blocks here, as before);
  a producer drains the stream channel and posts each event (priority
  50, arrival order — the stream stays in order), then a `streamEnd`.
- **The tools.** `streamEnd` appends the assistant message and builds
  the batch (2a); `advance` walks the cursor — `ToolStart` when the
  cursor reaches a call, its run dispatched then, `ToolResult` and the
  tool message when its completion has landed — and each tool goroutine
  posts its completion (priority 50). When the cursor passes the last
  call, `model` again. A barrier runs in a goroutine too: the loop
  goroutine never blocks on a tool.
- **The end.** `end` cancels the turn context, emits `TurnEnd`, drops
  the turn, posts the prompt. The run ends only from a handler (`stop`):
  EOF or a dead run context at the prompt, the provider-closed-without-
  Done fault, a dead run context at a stream's end — the engine runs on
  a background context so the loop's own boundary rule decides, exactly
  as the sequential loop did.

Every event captures its turn and checks it is still the live one; a
stale completion or stream event (a turn ended while a producer was
still posting) is ignored by name, never misapplied. Every `Notify` and
every `Append` happens on the loop goroutine — the consumer is one
goroutine, which is the property the recorder, the guard's counting,
and the compaction anchor rely on (a named case pins it).

Priorities, fixed: input 90, stream 50, tool completion 50. Input
outranks the work of a turn, but is never pending during one (the
prompt is posted between turns), so today the numbers only order
arrivals; they are the seam the later producers use — a delegated
worker's async completion below tool completions, a timer below that,
the interrupt above input via `Update`. Those producers are the next
decisions, each one file plus a post.

What stays sequential: `Assemble` (and a compaction inside it) and the
`Stream` call run on the loop goroutine, blocking the consumer, as the
sequential loop blocked; the operator's interrupt still reaches them
through the turn context (the Frontend's handle), not through an event.
Making them producers is a later decision if a real case wants the
loop responsive while a summary call runs.

Rejected, named: calling `Input` continuously (steering as an event) —
it would move the steering slot from the Frontend into the loop and
reopen SPEC_COMMANDS 2 and every frontend; the engine stopping on the
run context (it would drop the drain of a torn stream and the boundary
rule the cases pin); a per-turn engine (the engine is the run's; the
turn is an object the events carry).

The gate: the `reopened` clause named 2b; the re-freeze PR after the
merge deleted it. `loop/` is frozen at 0.12.0's bytes: the event loop
work is closed end to end — phase 1 the engine, 2a the batch, 2b the
consumer — and the next change to the loop opens it by name again.

## testing

Phase 2b (`loop/consumer_test.go`): a concurrent run completing out of
order followed by a streaming call never overlaps two `Notify` calls
and never asks `Input` during a live turn (the consumer is one
goroutine; the prompt is between turns); a completion that lands before
the run ends is in the transcript, one that would land after is
ignored. Every pre-2b loop case — the thirty of the sequential loop and
2a's six — passes unchanged under `-race`: the contract is the same
bytes.

Phase 2a (`loop/batch_test.go`, `middleware/guard`, `tool/file`):
concurrent-eligible reads overlap and their results are emitted and
appended in call order with each result's own duration; a refused call
is a barrier (starts after the reads ahead of it end; the read after it
starts after it ends); no predicate is sequential (peak in flight 1);
`Parallel` bounds a run (peak exactly the bound); a run-context cancel
inside a run drains the batch into the transcript whole; the chain sees
every call of a run; the bound is race-free under a concurrent run and
refuses the re-issuance after; concurrent reads record their file state
without a race. Every pre-2a loop case passes unchanged under `-race`.

libevt's cases, by name, in Go (`evt/queue_test.go`):

- **basic**: the five events of `test_queue_basic.c` pop as
  `(5,80) (5,90) (4,60) (3,100) (1,50)` — priority descending, id
  ascending.
- **empty pop**: `Pop` and `Peek` on an empty queue report absent,
  never a zero event.
- **multithread**: four producers, two thousand events each, pushed
  under an external mutex (the queue is not itself safe, as in C), then
  drained: all delivered, priority never increases, id never decreases
  within a priority.
- **stress**: a hundred thousand interleaved pushes and pops with
  random priorities; the drained order is sorted; the position map and
  the heap agree after every operation (an invariant check in the test).
- **perf**: the push/pop benchmark, so a regression is a number.

The Go cases (`evt/queue_test.go`, `engine_test.go`,
`scheduler_test.go`):

- `Update` reprioritizes in place (the lowest becomes the first to pop);
  an unknown id is `false`; `View` is a sorted snapshot that leaves the
  queue unchanged.
- The engine executes by priority then arrival for events added before
  `Start`; multi-producer adds while running are all executed; an event
  may add an event (re-entrancy, no deadlock); `WithTick` runs the hook
  when idle and the default blocks without spinning; `Stop` leaves the
  pending set visible; `Start(ctx)` returns on cancel; a negative
  priority clamps to 0.
- The scheduler: `Start` twice is `ErrStarted`; a nil engine is
  `ErrNoEngine`; `Stop` joins (`Done` is closed); `Schedule` before
  `Start` is queued and runs after.

## scope

- `evt/` (new), `evt/PACKAGE.md`, this spec, the freeze allowlist line,
  the CHANGELOG entry, AGENTS.md's package list. `core/` and `loop/`
  byte-frozen.
- Prior art, credited: `~/Projects/libtrdr/src/libevt` (queue, event,
  context, engine, scheduler, clock) — the C is the reference
  implementation; a divergence is a named decision above.
