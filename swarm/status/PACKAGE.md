# swarm/status

The throttled `SwarmStatus` door (SPEC_SWARM 7): the one emitter both
the swarm controller and the delegate tool's Observe share. `New` takes
the frontend's `Notify` (nil = no emitter, today's silent behavior);
`Emit` delivers at most one snapshot per 250 ms window (a few per
second), `Force` always delivers — the exit's last frame must land or
the band lingers. Stdlib plus core only.
