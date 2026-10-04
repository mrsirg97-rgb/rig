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
  caller's context.
- `room.go`: `Room`, built with the transport its members speak
  through (`NewRoom(id, func(origin) Transport)`): `Add`, `Remove`,
  sorted `Members`, and `Broadcast`, a fan-out to every other member
  collecting one ack each; the error names the members that missed it.
  Safe for many goroutines; the loop is still the one consumer.
- `encode.go`: the JSON `Encoder` for a transport that crosses a
  process: the frame is origin, ok, kind, payload, and the kind names
  the `core` event (`notice`, `swarm_notice`, `swarm_status`); an event
  with no kind refuses to cross, an unknown kind refuses to land.

## How it is consumed

Not yet wired. The next commits: the kernel owns the engine and hands it
to the loop and the room (the named reopening); the swarm's roster,
heartbeat, status snapshots and notices become members and messages;
the frontends subscribe; the delegate and the review fire cross a pipe
transport with the encoder.

## Gotchas

- A send acks when posted, not when delivered: delivery is the
  consumer's turn, at the room's priority, below the operator's input
  and the turn's own events. A busy session delays the fleet's chatter
  and loses nothing, since the stores hold the facts.
- The loop transport's `Recv` never blocks: it registers the callback
  and returns; the context's end is the unsubscribe.
