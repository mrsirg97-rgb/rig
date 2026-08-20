# tool/python

## What it is

The persistent IPython kernel tool: one kernel per session, state across
calls, the JSON-lines wire protocol over stdio, no third-party client.
The shared kernel is also the plugin discovery/execution surface
(SPEC_PLUGINS): one process, the namespace shared.

## What it includes

- `Tool` — a `core.Tool` implementing the `plugins.Kernel` seam (`Run`).
- `kernel_host.py` — embedded host script (`//go:embed`).
- The JSON-lines wire protocol reader/writer over the subprocess stdio.

## How it is consumed

- Registered at the root as a native tool; the plugins leaf uses it as the
  `Kernel` seam for discovery and plugin calls.
- One kernel per session (per `Session.ID`); state persists across calls.

## Gotchas

- State lives in the kernel process (the namespace shared); a dead or
  un-writable kernel is a loud refusal.
- The wire protocol is JSON-lines over stdio; the timeout starts only
  after the kernel slot is taken (a queued call is never charged queue
  time).
- `kernel_host.py` is embedded and shipped; regenerating it changes the
  runtime.
