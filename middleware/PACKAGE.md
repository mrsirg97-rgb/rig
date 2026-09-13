# middleware

The root's slice in `wire()` applies `Wrap` in order, so the first-listed
link is innermost and the last (`paths`) is outermost, matching the
per-package docs.
