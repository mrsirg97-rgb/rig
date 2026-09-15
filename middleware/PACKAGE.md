# middleware

The root's canonical chain (`cmd/rig`'s `canonicalMiddleware`, the one
place the order is written down and tested) applies `Wrap` in order, so
the first-listed link is innermost and the last (`paths`) is outermost,
matching the per-package docs.
