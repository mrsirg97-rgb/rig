# tool/bash

## What it is

The bash(1) tool: real subprocesses, output surfaced to the model,
bounded. Stdlib only.

## What it includes

- `Tool` — a `core.Tool` that runs `bash -c <command>` via
  `exec.CommandContext`, surfacing stdout/stderr and a bounded result.

## How it is consumed

- Registered at the root as a native tool; `command`'s `toolCmd` and the
  middleware chain call it through `core.Tool.Exec`.

## Gotchas

- The command runs through a shell (`bash -c`) by design — the model
  authors the command string; quoting is the model's, not the tool's.
- A failure reply carries the cwd line (the failure voice); a success
  reply is byte-identical to the process output.
