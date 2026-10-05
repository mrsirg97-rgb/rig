# tool/python

## What it is

The persistent IPython kernel tool: one kernel per session, state across
calls, the JSON-lines wire protocol over stdio, no third-party client.
The shared kernel is also the plugin discovery/execution surface
(SPEC_PLUGINS): one process, the namespace shared.

## What it includes

- `Python`: the tool as its interface (2.12.6): `tool.Definition`, `Exec`
  as the one JSON door — decode `action`/`code`/`timeoutMs`, apply the
  absent-field default, route, and refuse an unknown action with the words
  that name the actions — and one method per verb: `Code(ctx, code,
  timeoutMs)`, `Vars(ctx, timeoutMs)`, `Reset(ctx, timeoutMs)`. The
  blank-cell refusal and the `timeoutMs` bound live in the verb, so a Go
  caller and the model hit the same checks. The interface also carries the
  seams the root and plugin discovery hold: `Run(ctx, code, timeoutMs)`
  (the raw `Reply`, what `plugins.Kernel` asks for), `Host()` and
  `Close()`. `New`/`NewWith` return the interface; the struct is
  unexported.
- `kernel_host.py`: embedded host script (`//go:embed`).
- The JSON-lines wire protocol reader/writer over the subprocess stdio.

## How it is consumed

- Registered at the root as a native tool: the plugins leaf uses it as the
  `Kernel` seam for discovery and plugin calls.
- One kernel per session (per `Session.ID`): state persists across calls.

## Gotchas

- State lives in the kernel process (the namespace shared): a dead or
  un-writable kernel is a loud refusal.
- An interrupted call tears the kernel down (like a timeout): a cell
  that was mid-flight cannot keep running and poison the next call, so
  an interrupt costs the namespace but never leaves a busy kernel behind.
- The wire protocol is JSON-lines over stdio: the timeout starts only
  after the kernel slot is taken (a queued call is never charged queue
  time).
- `kernel_host.py` is embedded and shipped: regenerating it changes the
  runtime.
- The action vocabulary is closed in `Exec`, not in the host: `code` (or
  none) runs code, `vars`/`reset` go to the host as commands, anything
  else is refused by name before the kernel is touched. The host's own
  unknown-cmd fallthrough runs the code field, which is the empty string
  when the Go side forwards only a cmd, so an unguarded action is an ok
  reply that ran nothing (SPEC_PYTHON, amended).

- The kernel is born in the session's workspace (the constructor's
  `cwd`, wired at the root before first start); relative paths in kernel
  code resolve against the project, not wherever the rig process
  happened to start.
