# middleware

The root's canonical chain (`cmd/rig`'s `canonicalMiddleware`, the one
place the order is written down and tested) applies `Wrap` in order, so
the first-listed link is innermost and the last (`paths`) is outermost,
matching the per-package docs. Since 2.9.0 the chain opens with
`index` (the code map's hook, SPEC_GRAPH) when a graph queue is wired:
innermost, so it sees the expanded path and runs only for a read, write
or edit that executed and returned without error; it hands the path to
the queue and never waits.
