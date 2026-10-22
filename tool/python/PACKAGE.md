# tool/python

## What it is

The persistent IPython kernel tool: one kernel per session, state across
calls, the JSON-lines wire protocol over stdio, no third-party client.
The shared kernel is also the plugin discovery/execution surface
(SPEC_PLUGINS): one process, the namespace shared.

## What it includes

- `Python`: the tool as its interface (2.12.6): `tool.Definition`, `Exec`
  as the one JSON door — decode `action`/`code`/`timeoutMs` strictly (an
  unknown field refuses, matching the bash/delegate doors), apply the
  absent-field default, route, and refuse an unknown action with the words
  that name the actions — and one method per verb: `Code(ctx, code,
  timeoutMs)`, `Vars(ctx, timeoutMs)`, `Reset(ctx, timeoutMs)`. The
  blank-cell refusal and the `timeoutMs` bound live in the verb, so a Go
  caller and the model hit the same checks. The interface also carries the
  seams the root and plugin discovery hold: `Run(ctx, code, timeoutMs,
  onStream)` (the raw `Reply`, what `plugins.Kernel` asks for; `onStream`
  receives the cell's stdout as the host drains it — a `stream` protocol
  line per chunk, the text also collected into the final reply's
  `out`/`err`; nil collects silently), `Host()` and `Close()`. `New`/`NewWith` return the interface; the struct is
  unexported. A `timeoutMs` of 0 sets no timer: the cell runs until it
  answers or the caller's context ends (the programmatic seam's no-cap
  call; the model-facing verb keeps its bound).
- `kernel_host.py`: embedded host script (`//go:embed`).
- The JSON-lines wire protocol reader/writer over the subprocess stdio.
  The reply line is bounded: the host clips `out`/`result`/`error` at
  16000 chars each and JSON escaping inflates to six bytes a character at
  worst, so 512 KiB holds three maxed streams with headroom — a line over
  the ceiling is not a host reply, and the read loop tears down like any
  kernel death (the pending cells fail with the exit note).

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
- `timeoutMs` alone is the in-turn cell's ceiling in place of the
  default (2.18.4). `defer: true` with `timeoutMs` promotes the cell to
  the kernel queue: the call answers at once and the result arrives as
  a message once it ends, the kernel's own restart named in it
  (2.16.0). `defer` without `timeoutMs` refuses by name. The kernel is the worker — calls serialize
  in submission order, and a dead session takes pending results with
  it.
