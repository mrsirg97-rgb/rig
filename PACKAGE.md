# rig (the root package)

## What it is

The composition kernel: the dependency bag the loop drives, assembled
from options. Every dependency is explicit in the `New` call; swapping
happens at registration with zero consumer changes; fields are written
through options only. The kernel carries interfaces and slices, so the
loop never names a concrete type.

## What it includes

- `Kernel`: `Provider`, `Frontend`, `Policy`, `Tools`, `Middleware`,
  `Session`, `Commands`, `Concurrent`, `Parallel`.
- `New(opts...)`: assembles the kernel; duplicate tool or command names
  are a wiring error and panic at startup, loud and early.
- `WithProvider`, `WithFrontend`, `WithPolicy`: required seams; the
  loop refuses loud without them.
- `WithTools`: zero tools is a valid boundary: the model answers in
  plain text only.
- `WithCommands`: the user-command registry (SPEC_COMMANDS 1): the
  loop ignores it; dispatch is the Frontend's business, exactly as the
  steering slot is. Zero commands is the compat boundary.
- `WithMiddleware`: the execution chain in listed order; first-listed
  composes innermost.
- `WithConcurrent`, `WithParallel`: the batch's predicate and bound
  (SPEC_EVT 6): a call the predicate admits runs beside its admitted
  neighbours; any other call is a barrier in call order; nil is the
  sequential loop, byte-identical; 0 is the loop's default bound.
- `SortedToolNames`: the registered names in stable order, for
  diagnostics and tests.

## Gotchas

- `Session` is owned by the wiring side: persistence (`Save`/`Load`)
  hangs off it and `Run` borrows it for the turn; a nil session is
  minted fresh by the loop.
