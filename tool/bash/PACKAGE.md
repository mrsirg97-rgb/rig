# tool/bash

## What it is

The bash(1) tool: real subprocesses, output surfaced to the model,
bounded. Stdlib only.

## What it includes

- `Bash`: the tool as its interface (2.12.6): `tool.Definition` (the
  words), `Exec` as the one JSON door — it decodes and routes, nothing
  else — and `Run(ctx, command, workspace)`, the one verb, named for what
  it does. The empty-command refusal lives in `Run` and at the bounded
  door, so a Go caller and the model meet the same words on every door;
  the workspace check is `Run`'s for the sync path, and the bounded
  door's workspace rides the delegate's pathguard — the same rule, the
  worker's voice. The struct is
  unexported and `New` returns the interface; the root's tool map holds it
  as a `core.Tool`.

## How it is consumed

- Registered at the root as a native tool: `command`'s `toolCmd` and the
  middleware chain call it through `core.Tool.Exec`.

## Gotchas

- The command runs through a shell (`bash -c`) by design: the model
  authors the command string; quoting is the model's, not the tool's.
- A failure reply carries the workspace line (the failure voice): a success
  reply is byte-identical to the process output. The workspace is stat'ed and
  checked before the child starts — the same check when the arg is missing,
  the process cwd resolved for the name and refused through `.` when the cwd
  cannot be resolved: a missing, non-directory, or
  unsearchable workspace fails with `bash: workspace X: <reason>` and no content
  (the loop feeds the error text into the result content once), never a
  fork/exec line naming /usr/bin/bash.
- The check is usability, not containment: existence, directory, and search
  permission (X_OK). bash is unscoped by design like the file tools
  (SECURITY.md) — the check asks whether the workspace is usable, never
  whether the command stays inside it; containment rides the landlock
  runner when the operator turns it on.
- The capture keeps 256 KiB of one command's output — four times the
  kernel's default 64 KiB result cap, so the capture is never what trims
  what the model sees: the guard's cap decides over the fuller output.
  `WaitDelay` gives a killed process's pipes one second to drain, so the
  partial output a killed command already wrote still lands, and a writer
  holding the pipe cannot stretch the call past it.
- The result writer keeps the head of the child's output at the cap and
  drops the rest, so a huge stream cannot pin memory: the child's
  writes are always fully consumed (it never blocks) and the kept output
  is byte-identical to the post-hoc truncation (head + marker).
- `timeoutMs` alone is a kill bound on the in-turn run (2.18.4): the
  process group dies at the bound, the output so far is kept and the
  error is `bash: killed at timeoutMs=N`; the turn's own interrupt
  stays an interrupt. `defer: true` with `timeoutMs` hands the command
  to a delegate command worker: the call answers with the hand-back and
  the result arrives as a message once it ends, killed at the bound
  (2.16.0). `defer` without `timeoutMs` refuses by name. With neither,
  the exec path is byte-identical to the synchronous tool.
