# tool/bash

## What it is

The bash(1) tool: real subprocesses, output surfaced to the model,
bounded. Stdlib only.

## What it includes

- `Bash`: the tool as its interface (2.12.6): `tool.Definition` (the
  words), `Exec` as the one JSON door — it decodes and routes, nothing
  else — and `Run(ctx, command, workspace)`, the one verb, named for what
  it does. The empty-command refusal and the workspace check live in
  `Run`, so a Go caller and the model meet the same words. The struct is
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
  checked before the child starts: a missing, non-directory, or
  unsearchable workspace fails with `bash: workspace X: <reason>` and no content
  (the loop feeds the error text into the result content once), never a
  fork/exec line naming /usr/bin/bash.
- The result writer keeps the head of the child's output at the cap and
  drops the rest, so a huge stream cannot pin memory: the child's
  writes are always fully consumed (it never blocks) and the kept output
  is byte-identical to the post-hoc truncation (head + marker).
