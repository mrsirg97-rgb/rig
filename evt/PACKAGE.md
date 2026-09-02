# evt

## What it is

The event loop (SPEC_EVT): libevt's shape (`~/Projects/libtrdr`,
`src/libevt`) made Go-centric. One consumer, many producers; work
arrives as closures ordered by priority then arrival; the consumer
executes one at a time outside the queue's lock. A leaf, stdlib only;
the turn loop is its consumer (SPEC_EVT phase 2): every step of a turn
is a closure on the loop goroutine, and `loop` is the one package that
imports this one.

## What it includes

- `Closure`: the unit of work: `Resolve(ctx)`; `Func` adapts a
  `func(context.Context)`. (libevt's `Context`; renamed, since in Go
  that word is `context.Context`.)
- `Event`: `ID`, `Priority`, `Closure`, `UpdatePriority`; `NewEvent`;
  `Execute(e, ctx)` resolves its closure.
- `Clock`: `Step() uint64` mints ids: `Counter()` (the default, a
  strictly increasing atomic) and `Monotonic()` (wall nanoseconds, as
  libevt's `clock_step`, but never repeating: a same-nanosecond step
  takes last+1, so a push is never dropped as a duplicate id).
- `Queue`: the 4-ary max-heap ordered `(priority desc, id asc)` with a
  position map: `Push`, `Pop`, `Peek`, `Update` (the one addition:
  reprioritize in place), `View` (a sorted snapshot drained from a
  clone), `Len`. Not goroutine-safe by itself, as in C; the engine is
  the lock.
- `Engine`: `Start(ctx)` (blocks; the consumer), `Add` (any goroutine),
  `Update`, `Stop`, `Pending`; options `WithClock`, `WithTick` (the idle
  hook, libevt's `tick_fn`; absent, the consumer waits on a cond),
  `WithCapacity`.
- `Scheduler`: the harness: `Start() error` (`ErrNoEngine`,
  `ErrStarted`), `Schedule`, `Stop` (cancel, stop, join), `Done`.

## How it is consumed

- `e := evt.NewEngine(): s := evt.NewScheduler(e); s.Start();
  s.Schedule(evt.Func(func(ctx) { … }), priority)`; `s.Stop()` joins.
- Or drive the engine on a goroutine of your own: `go e.Start(ctx)`;
  cancel the ctx or call `e.Stop()`.
- A higher priority runs first: equal priorities run in arrival order;
  a negative priority clamps to 0.

## Gotchas

- The queue alone is not safe for concurrent use (libevt's contract);
  go through the engine, which holds the mutex around every queue op.
- Execution is outside the lock: an event may `Add`, may block, may run
  long; a panic in an event is the closure's, not recovered.
- `Stop` runs nothing further: the event in flight finishes; `Pending`
  shows the rest, sorted. Cancelling `Start`'s ctx is a `Stop`.
- Two `Start`s on one engine are the caller's bug: the scheduler is
  the door that refuses (`ErrStarted`).
- `Update` on an id that already popped is `false`, never an error.
- `Push` of a duplicate id is ignored (the position map is the identity).
- Priority starves: a stream of high events never yields to low ones.
  The consumer that assigns priorities owns that (SPEC_EVT phase 2).
