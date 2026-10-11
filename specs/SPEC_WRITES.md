# rig: a write outranks a read

In any async system where work is submitted whole and its results are
delivered whole, a write has priority 60 and a read has priority 50,
always. The priority belongs to the call, not to the batch: a mutating
tool call is a write at 60, every other call is a read at 50, and a
batch gains its order from that automatically, not the other way round.
So when an agent issues several calls, every write executes first,
serially, in the order written; then the reads execute in parallel and
react to the writes. A read is an observation of the outcome: the update
happens first and the select reacts to the update. A read that a write
depends on does not belong beside the write, it belongs inside it as the
write's own precondition (`edit`'s `old` string is exactly that).

The evidence is rig answering "is this call a write?" twice, by tool
name, and the answers disagreeing. `cmd/rig/reap.go` holds
`concurrentNatives` (read, view, web, delegate, decide), which the batch
uses to decide what may overlap, and `mutatingNatives` (bash, write,
edit, python, scheduler, plugin, delegate), which the approval gate
uses. `delegate` is in both; `todo`, `rem`, `sessions` and `verdict` are
in neither; `todo read` and `todo create` are the same thing to both.
Measured over the operator's session stores (52,747 assistant messages
with tool calls): 8,427 carry two or more calls, 1,023 mix reads and
writes; 541 place a read before a write (2 on a path the same batch then
edits); 509 place a read after a write (11 verify-after-edit, which this
order keeps true); 6 mixed batches include a delegate.

## what it is not (named)

- **Not a per-batch annotation.** The model does not mark calls; the
  registry does. A batch is a list of calls, and its order falls out of
  the calls' own priorities.
- **Not a lock.** Writes serialize because the batch dispatches one at
  a time, not because anyone waits on anyone. No locks, timeouts or
  polls anywhere.
- **Not a transcript change.** Results are appended in the order the
  model wrote the calls; only the execution order moves.
- **Not a new rule for background work.** A bounded write rides the
  delegate (SPEC_ASYNC), so it does not hold later reads and needs no
  rule of its own; its completion is a delegate return, delivered at 70,
  above every write and read.

## goals

- One predicate on the call: does this call mutate state. Declared in
  `tool/registry.json` beside `operator`, exposed beside
  `Operator(name)`; it replaces both lists in `cmd/rig/reap.go` and
  `isMutating`.
- Two named priorities in `kernel.go` beside the table: a write is 60, a
  read is 50. The call carries its priority; the loop's batch reads it
  and its order follows.
- The three consumers read the one predicate: ordering, overlap, and the
  approval gate.
- The transcript does not move; the rule is said once where the model
  reads it and once in the specs, in their own voices.

## decisions

### 1. The predicate: one answer, in the registry

`tool/registry.json` gains `mutates` beside `operator`: the list of
action names that mutate state, or `["*"]` when every call to the tool
does. `tool/registry.go` exposes `Mutates(name, action string) bool`
beside `Operator(name)`: an unknown name (a plugin tool) is a write —
fail closed, the same posture `isMutating` holds today; `["*"]` is a
write for any action; otherwise the action names itself. A tool the
registry does not know at all is a write.

The table, tool by tool:

- write, edit, bash, delegate, verdict: `["*"]` — every call mutates.
- python: `["code", "reset"]` — `vars` reads.
- scheduler: `["create", "update", "pause", "resume", "remove", "repair"]`
  — `list`, `show`, `runs` read.
- plugin: `["run", "create", "delete", "reload"]` — `list`, `schema` read.
- todo: `["create", "claim", "complete", "fail", "release", "retry",
  "move", "note", "prune", "accept", "reject"]` — `read`, `notes`,
  `finished` read.
- rem: `["learn", "reflect", "prune", "index"]` — `recall`, `pack` read.
- read, view, web, decide, sessions: `[]` — pure reads.

`concurrentNatives`, `mutatingNatives` and `isMutating` are gone; the
one predicate answers for the ordering, the overlap and the gate.

### 2. The two numbers, and where they act

`kernel.go` gains `PriorityWrite = 60` and `PriorityRead = 50` beside
the table (`PriorityInput` 90, `PriorityDelegate` 70, `PriorityStream`
and `PriorityTool` 50, `PriorityFleet` 30, `PriorityReview` 10). The
kernel's `Concurrent func(call) bool` becomes `Priority func(call) int`,
wired at the root from the registry predicate; the batch carries the
priorities it computes from it — the priority belongs to the call, and
rides the call's submission to the engine, not the call's wire struct,
so `core` does not change. No flag at the call site: a write is a call
whose priority is 60, a read is a call whose priority is 50.

The two numbers act in two places, one mechanism:

- **The batch's order.** `dispatch` loses its run-scanning. The batch
  submits every call to the engine at the call's own priority and
  nothing else: a write's closure runs its exec inline, so the writes
  serialize in the drain in index order and chain through their own
  completions; a read's closure hands its exec to a goroutine bounded by
  the semaphore, so the reads overlap. The engine's drain is already
  highest-first, so the sort alone makes the reads wait behind every
  write's closure and completion, on the state the writes left. The
  loop's cursor still appends the results in call order.
- **The call's completion event.** Each call's completion returns to the
  engine at the call's own priority: a write's at 60, a read's at 50.
  Input (90) and a delegate's return (70) land between two writes; the
  turn's stream (50) and the fleet (30) wait until the writes are done.
  No floor: the writes chain through their own completions, so the drain
  never drops below 60 while a write is outstanding, and `evt` does not
  change.

What may still land between two writes: the operator's input, and a
delegate's return. Nothing else in the turn: not the stream, not the
fleet, not another call's completion.

### 3. The consumers, and what moves

One predicate, three readers: the batch's order (60 then 50), the
overlap (the reads may run together, the writes never), and the approval
gate (manual mode asks before a write, by call — the gate's predicate
widens from the tool's name to the call, because the action names
itself). The calls whose behavior moves:

- **Newly overlap** (serialized today, reads now): `todo read/notes/
  finished`, `rem recall/pack`, `scheduler list/show/runs`, `plugin
  list/schema`, `python vars`, `sessions`, `decide` (already overlapped).
- **Newly serial**: `delegate` — its starts drain one at a time in index
  order. The fan-out survives: a start returns at once (2.14.0), so the
  workers still run concurrently; only the starts serialize, and each
  is a recorded write.
- **Newly ask in manual mode** (silent today): every mutating call of
  `todo` (`create`, `claim`, `complete`, `fail`, `release`, `retry`,
  `move`, `note`, `prune`, `accept`, `reject`), of `rem` (`learn`,
  `reflect`, `prune`, `index`), and `verdict` — the gate never saw them.
- **Newly silent in manual mode**: `plugin list/schema`, `python vars`,
  `scheduler list/show/runs` — reads the gate used to stop.
- **`delegate` is a write** — it was in both lists; the one predicate
  says spawn-and-record is a mutation, and its start drains like one.

### 4. The background case, said plainly

A bounded write rides the delegate (SPEC_ASYNC): `bash` with
`timeoutMs`, a promoted python cell, a promoted fetch — each submits a
worker and answers at once, so it does not hold later reads and needs no
rule of its own. Its completion is a delegate return, delivered at 70,
above every write and read: the operator is 90, a return is 70, a write
is 60, a read is 50 — the priorities are the whole mechanism, and the
background case is what the 70 buys.

### 5. The transcript does not move

Results are appended in the order the model wrote the calls: the loop's
cursor walks the calls in index order and appends each result as it
lands, and the batch posts results by index, so a read placed before an
edit still reports after the edit's result — it only *executes* after
it. The rule is said once where the model reads it — a read in a message
sees the tree after that message's writes, and so does a delegate
started in it — and once in the specs, in their own voices. The menu
measured 16,436 against `ci.yml`'s wall of 16,500 at 2.16.0, about 64
characters of room: the sentence is paid for by trimming, and the wall
does not move.

### 6. The batch, rewritten

`newBatch` takes the priority function and the engine's poster; `dispatch`
loses the run-scanning and becomes a single pump: at the turn's first
gate it submits every call — the writes' closures at 60, the reads' at
50 — and the engine's sort does the rest. A write's closure runs its
exec inline and posts its own completion at 60, so the writes serialize
in index order with the drain's trips between them; a read's closure
hands its exec to a goroutine bounded by the semaphore, so the reads run
together. An interrupt mid-drain cancels the writes not yet started
(their execs return at once on the dead context, doing no work) and the
reads never do work; a failed write does not stop the ones after it
(the closures queue in index order, not in outcome order).

### 7. The engine sorts

`evt` does not change. The drain is highest-first and always was; the
batch feeds it closures at the calls' own priorities and the ordering
falls out: input (90), a delegate's return (70), the writes' chain
(60), the reads (50), the fleet (30). No floor, no hold, no locks.

## testing

Named cases, failing first:

- **The predicate's table**: every native tool and action, and an
  unknown plugin tool, answers `Mutates` exactly as the table above.
- **`[read a, edit a]`**: the read returns the post-edit bytes, and the
  results sit in call order.
- **`[edit a, read a]`**: the same, both orders.
- **`[edit, delegate]`**: the worker starts after the edit.
- **`[todo create, todo read]`**: the read shows the new task.
- **A read-only batch still overlaps**: two reads run together.
- **The writes run in index order** with none of the turn's own events
  between them; input and a delegate's return may land.
- **An interrupt mid-drain** cancels the writes not yet started: their
  execs return at once on the dead context and do no work, and the
  reads never do work.
- **A failed write does not stop the ones after it.**
- **Manual approval asks exactly for the writes**: a read passes silent,
  a write asks once.

## scope

- `specs/SPEC_WRITES.md` (this file); `specs/SPEC_EVT.md` and
  `specs/SPEC_CORE.md` carry their own named paragraphs; `specs/
  SPEC_DELEGATE.md` names the start-as-write rule.
- `tool/registry.json`, `tool/registry.go`: the `mutates` field and
  `Mutates`.
- `cmd/rig/reap.go` (the lists die), `cmd/rig/env.go` (`isMutating`),
  `cmd/rig/root.go` (the wiring), `cmd/rig/kernel.go` (the two numbers,
  the `Priority` field, `WithPriority`), `cmd/rig/middleware.go`.
- `loop/batch.go`: the pump. `loop/loop.go`: the wiring only.
- `tool/registry.json`'s menu sentence, paid for by trimming.
- `CHANGELOG.md`, the touched `PACKAGE.md`s, the version, the measured
  block in the last commit.
