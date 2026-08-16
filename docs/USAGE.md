# looper usage

looper is a terminal REPL: you type, the model answers, and when the model
asks for work the tools execute it in your working directory. Build and
configure per `docs/SETUP.md`; this file covers running it.

## starting a session

```sh
looper --base-url http://127.0.0.1:8080/v1 --model your-model --system "be terse"
```

Then just talk. There are no subcommands and no commands to memorize — the
boundary is plain text in, plain text out. Blank lines are no-ops; EOF (Ctrl-D)
ends the session.

## what you see

Rendering is deliberately plain and greppable (a TUI frontend is a later
extension, same seam):

```
$ what files are here?
[call] bash
ls -la
total 12
drwxr-xr-x 3 you you 4096 ... .
...
```

- model text streams verbatim as it arrives;
- each tool invocation renders as `[call] NAME` followed by its arguments and
  output — what executed is visible, not implied;
- faults render as `[fault] <reason>` and the turn stops there; the session
  survives and the next prompt resumes at the last complete message.

## session behavior

- **The conversation persists** across turns within a run; after a fault or
  interruption the loop returns to awaiting input rather than dying.
- **A failed tool call is fed back to the model once** — the loop never
  retries silently. If the model re-issues an identical failing call, the
  bound (`--retries`) counts it across turns and refuses at the limit, naming
  the bound. A successful re-issuance (polling) never counts. Practical
  effect: persistent flapping on one broken call gets a named refusal, not an
  infinite loop.
- **Unknown tool names** are fed back as errors; the turn continues.
- **Denials** (a tool outside the allow-list) are attributed with the reason
  and are countable by the bound — that pairing is the spec's core invariant
  and is proven by the suite, not trusted.

## interruption and failure semantics

- **Ctrl-C** ends the session once the in-flight step unwinds: a mid-tool
  turn unwinds quickly (the tool's process group is killed), a mid-stream
  turn waits for the server's stream to close (the process stays alive in
  the meantime, and a second Ctrl-C is ignored: the first signal is the
  exit). Teardown surfaces cleanly (exit 0, session closed).
- A provider that closes the stream without a Done or Fault marker makes
  `looper` exit non-zero with a loud error. Silent termination is impossible by
  construction.

## narrowing what may execute

```sh
looper --allow read                 # inspection only: bash/write/edit denied
looper --allow bash,read            # run things, inspect things, change nothing
```

Anything not named is refused at the boundary with the reason named, and the
refusal goes back to the model. The default permits all thirteen built-in
tools; narrowing is always available and compose-order-agnostic.

## working-directory discipline

The file tools normalize paths before any provenance decision, and `edit`
validates that the file is still what it was when last read — external drift
is named and the write is refused; ambiguous old-strings ("occurs N times")
are refused, never guessed at. Outputs are capped (bash 256 KiB, read 1 MiB)
and the truncation is named in the output.
