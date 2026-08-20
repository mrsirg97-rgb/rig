# plugins

## What it is

The python plugin surface (SPEC_PLUGINS): one file under the rig home's
plugins/ directory, one tool per file, the name the filename stem.
Discovery runs through the shared python kernel — the same persistent
kernel as tool/python, one process, the namespace shared — and the loaded
tools register on the existing Tool seam, indistinguishable from a native
tool on the wire. Stdlib plus core and tool/python (the kernel seam),
nothing else: the leaf discovers and wraps; the root (cmd/rig) wires.

## What it includes

- `Discover` — imports every file through the kernel and reports each, in
  file order.
- `Report` — one plugin file's discovery outcome.
- `Tool` — one loaded plugin on the Tool seam.
- `Reload` + `NewReload` — the `plugins_reload` native tool (SPEC_PLUGINS
  8): re-discovery over the home's plugins/ and the hand-off to the root's
  swap.
- `List` — the home's plugin listing (top-level `*.py`).
- `Check` — the collision refusal (a loaded plugin named like a native).
- `Kernel` — the shared-kernel seam (one code cell, the host's raw reply).

## How it is consumed

- `Discover` runs at startup and on reload through `Kernel` (`Run(code,
  timeoutMs)`); tool/python's `Tool` implements it; tests stand in with a
  fake, no python required.
- `New` wraps one discovery report as a `core.Tool`; the root registers it
  alongside native tools.
- `NewReload` is wired as a native tool; `Reload.Exec` lists, discovers,
  checks collisions, and hands off to the root's swap.
- The root's swap takes effect on the next turn, never mid-turn (the
  root's table, the loop's per-turn reads).

## Gotchas

- `defaultTimeoutMs` (120000) starts only after the kernel slot is taken:
  a queued call is never charged queue time.
- A kernel-level failure (the call gave up, the kernel died, the report is
  not the JSON list) is the error; a per-file failure is a skipped report,
  never the error — a broken plugin must not brick the harness.
- The discovery cell keeps modules in the user namespace
  (`__rig_plugins__`) and `sys.modules` under the stem, so the python
  tool's imports reach the loaded plugins (the shared namespace).
- `compactJSON` re-marshals args compactly so the embedded literal is
  total (`pyLiteral`); the args must parse as JSON.
- `pyLiteral` escapes backslash and single-quote; JSON text has no raw
  newlines and no double-quote collisions.
- `errorTail` prefers the exception's type+message, else stderr, else a
  named gap.
- `List` skips the pending zone, subdirectories, and non-`.py` files; a
  missing or empty plugins/ dir is a no-op that never starts the kernel.
- `Check` ignores skipped reports (they are not tools); the startup's
  refusal and the reload's are this one rule.
- On a reload, a discovery failure leaves the table and the wire untouched
  (the swap never ran).
