# swarm/status

The throttled `SwarmStatus` door (SPEC_SWARM 7): the one emitter both
the swarm controller and the delegate tool's Observe share. `New` takes
the frontend's `Notify` (nil = no emitter, today's silent behavior);
`Emit` takes the snapshot builder and invokes it only when the frame is
due — at most one per 250 ms window (a few per second), so a streaming
worker's per-chunk emits never fold the store; `Force` always delivers —
the exit's last frame must land or the band lingers. Stdlib plus core
only.
