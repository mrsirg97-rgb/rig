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
- The action vocabulary is closed in `Exec`, not in the host: `code` (or
  none) runs code, `vars`/`reset` go to the host as commands, anything
  else is refused by name before the kernel is touched. The host's own
  unknown-cmd fallthrough runs the code field — which is the empty string
  when the Go side forwards only a cmd — so an unguarded action is an ok
  reply that ran nothing (SPEC_PYTHON, amended).

- The kernel is born in the session's working directory (`SetCwd`, wired
  at the root before first start); relative paths in kernel code resolve
  against the project, not wherever the rig process happened to start.
