# broadcast

## What it is

The fleet's message seams (SPEC_EVT's third phase, 2.11.0), lifted from
the operator's `broadcast` module and placed on the event loop: a `Room`
of `Member`s, each speaking through a `Transport`, exchanging `Message`s
that carry an origin, the member's health, and a `core.Event`. A message
with no event is a heartbeat. The consensus half of the original (the
transaction, the state machine, the WAL, the clock) stayed behind: rig's
durable truth is the todo log and the decision store, and one slot needs
no quorum. Imports `core` and `evt` only.

## What it includes

- `message.go`: `Message` (`Origin`, `Ok`, `Event`), `NewMessage`,
  `Heartbeat`.
- `transport.go`: `Transport` (`Send` with an ack callback, `Recv` with
  a messages callback, `Close`) and the loop transport,
  `NewLoopTransport(id, engine, priority)`: a send posts one closure at
  the transport's priority and acks on the post, since the queue is the
  durability and an event stays until the consumer runs it; the receive
  is the callback the closure resolves to on the loop's goroutine; one
  heartbeat waits per member, a second one before the first ran is not
  posted. A subscriber's context ending is reported to it once and ends
  the deliveries.
- `member.go`: `Member`, a `Transport` placed in a `Room`: `Forward` to
  itself, `Publish` to the room, `Subscribe` to what arrives, `Leave`
  (closes the transport, leaves the room). Every call threads the
  caller's context. `Say(member, source, text, level...)` publishes one
  `core.Notice`: the one voice every background subsystem speaks with
  (2.11.0; it replaced the `loud func(string)` closures); the level is
  chosen where the text is made (2.11.7): error when something failed
  or was refused, success when something the operator asked for
  completed, info otherwise.
- `room.go`: `Room`, built with the transport its members speak
  through (`NewRoom(id, func(origin) Transport)`): `Add`, `Remove`,
  sorted `Members`, and `Broadcast`, a fan-out to every other member
  collecting one ack each; the error names the members that missed it.
  Safe for many goroutines; the loop is still the one consumer.
- `pipe.go`: the pipe transport, `NewPipeTransport(id, rw, enc)`, for
  a member whose other end is another process: a send is one encoded
  frame per line on the writer, acked on the write; a receive reads
  frames off the reader on its own goroutine until the far end closes
  it or the context ends, and reports that end once. The parent holds
  the read end and publishes what arrives as the child's member; the
  child holds the write end and is a voice on the wire, never a member
  of a room. A frame that does not decode closes the transport: the
  child is rig itself, so a bad frame is a version mismatch, and the
  rule is to fail closed.
- `encode.go`: the JSON `Encoder` for a transport that crosses a
  process: the frame is origin, ok, kind, payload, and the kind names
  the `core` event (`notice`, `swarm_status`); an event with no kind
  refuses to cross, an unknown kind refuses to land.

## How it is consumed

The root builds the session's room over the loop transport at
`rig.PriorityFleet` and is its frontend member; the member ids are
named in the kernel (`rig.MemberFrontend`, `MemberDelegate`,
`MemberGraph`, `MemberDecision`; below `MemberMinted` the delegate
tool mints its workers'; the swarm's supervisor is 0 and its workers
count up from 1). The graph queue, the decision queue, the reviewer,
the pack scorer and the decision recorder hold a member and `Say`
their notices; the frontend member hands every event to the current
recorder, or prints a notice to stderr while there is none yet.
A spawned worker (`rig -p -`) gets the write end of a pipe as fd 3 and
its member id in `RIG_FLEET` (`store/scheduler`'s `Fleet` door); the
one-shot frontend heartbeats through a pipe transport on it, and the
parent's `Delegate` publishes every frame as the worker's member, so
the supervisor and the delegate tool stamp the heartbeat from the room,
never from the bytes. The run-job runner opens the same pipe and
touches its stall watch per frame.

## Gotchas

- A send acks when posted, not when delivered: delivery is the
  consumer's turn, at the room's priority, below the operator's input
  and the turn's own events. A busy session delays the fleet's chatter
  and loses nothing, since the stores hold the facts.
- The loop transport's `Recv` never blocks: it registers the callback
  and returns; the context's end is the unsubscribe.
