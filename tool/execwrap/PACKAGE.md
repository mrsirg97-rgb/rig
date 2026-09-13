# tool/execwrap

## What it is

The landlock subprocess seam: `Args` prepends the `RIG_EXEC_WRAPPER`
helper (`rig -exec <argv>`) to a tool's argv when the env names one.
The helper is a fresh single-threaded process that restricts itself
from `RIG_LANDLOCK` and execs the command, so the landlock domain rides
the tool's subprocess (bash, the python kernel, bootstrap steps)
instead of depending on the Go worker's per-thread creds. The env is
set only by the landlock runner; the jailed and off profiles never
carry it, so those tools exec exactly as before.

## What it includes

- `Args`: wrap-or-pass, one function.

## How it is consumed

- `tool/bash`: the command argv goes through `Args`.
- `tool/python`: the kernel host and the bootstrap steps go through
  `Args`.
- `store/scheduler`: the landlock spawn puts `RIG_EXEC_WRAPPER=<the
  worker binary>` on the named env list.
