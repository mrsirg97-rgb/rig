# rig usage

rig is a terminal REPL: you type, the model answers, and when the model
asks for work the tools execute it in your working directory. Build and
configure per `docs/SETUP.md`; this file covers running it.

## starting a session

```sh
rig --base-url http://127.0.0.1:8090/v1 --model your-model --system "be terse"
```

Then just talk. The boundary is plain text in, plain text out. Blank lines
are no-ops; EOF (Ctrl-D) ends the session. A line starting with `/` is a
command (below); `//` escapes it back into a prompt.

A session outlives the process. `--resume <id>` continues an earlier one:
the transcript, the file provenance, and the identity are rebuilt from the
state store in one read-only transaction (dangling tool calls are kept; an
unknown id is loud). The per-process state — the guard's counts, the steering
slot — starts fresh, and the session's id is the one to look up in the
`sessions` table of the state store (`~/.rig/sessions/*.sqlite` under the
rig home — `$RIG_HOME` over `~/.rig`). `-p` one-shot and `--resume` refuse at construction: one-shot
stays one-shot.

## commands

Typed lines with a `/` prefix; the loop never sees them. An unknown command
is a loud line naming the known set, never silently a prompt.

- `/compact` — force a compaction now (the `⧉` line reports dropped/kept),
  or `compact: nothing to drop`.
- `/new` — close the session row ok, mint a fresh session, same process.
- `/sessions` — list; `show <id>` renders a transcript; `resume <id>` swaps
  to it in-process.
- `/models` — the per-model table with the active row marked; `/models <id>`
  switches for the next turn.
- `/steer <text>` — queue for the next boundary (interrupts a live turn);
  bare `/steer` interrupts only.
- `/todo`, `/scheduler` — the same tools the model gets, same queue, same
  store; the tool's own refusals teach the shape.
- `/effort` — the reasoning dial: bare shows the active level and the
  model's available ones; `/effort <level>` sets it for subsequent turns
  (`specs/SPEC_MODES.md`).
- `/role` — the stance: `/role <default|architect|reviewer>` sets the
  session's role prose between the system prompt and AGENTS.md.
- `/approve` — `/approve auto` (today's behavior) or `/approve manual`:
  manual pauses every mutating tool call for the operator's y/n at the
  TUI ask row; a denial is a model-visible teaching refusal.
- `/plugins` — the python plugins: the loaded ones (name, description,
  file), the skipped ones with their reasons, and the pending zone —
  `pending` lists the model's authoring with each file's DESCRIPTION,
  `approve <name>` installs one (the operator's verb), `reload`
  re-registers from disk (the `plugins` tool's command door),
  `create <text>` queues the authoring prompt.

Context compacts automatically at the active model's own trigger (the
models table); the `⧉` line reports it and the summary lands in the
transcript and in rem.

The `delegate` tool (SPEC_DELEGATE) spawns a headless worker on a task
now — a bounded sub-task whose result is a message, not a conversation
— on the worker model, in a cwd under your session's or the rig home.
It waits, feeds back the worker's last message, and records the run in
the one scheduler store under an ad-hoc key, so `scheduler runs`
shows it beside cron runs; the worker's transcript is resumable with
`sessions resume <id>`.

## what you see

The piped CLI's rendering is deliberately plain and greppable (the
terminal default is the TUI — see `docs/SETUP.md`; the CLI stays the byte
reference):

```
$ what files are here?
● bash
total 12
drwxr-xr-x 3 you you 4096 ... .
bash ✓ 12ms
↑922 ↓40 · cache 918 99%
```

- model text streams verbatim as it arrives, and so does the model's
  thinking when it reports any (the reasoning round-trips with the
  transcript, so interleaved-thinking tool turns keep theirs);
- each tool invocation renders as `● NAME` around its output, closed by
  `NAME ✓ <duration>` (`✕` when it failed) — what executed is visible, not
  implied; a guarded refusal fails the row and says so;
- the usage line closes every turn: `↑prompt ↓completion · cache read hit%`
  — the turn's totals across its model calls, pane's token shaping, the hit
  rate as cached-over-prompt;
- faults render as `[fault] <reason>` and the turn stops there; the session
  survives and the next prompt resumes at the last complete message.

## session behavior

- **The conversation persists** across turns within a run; after a fault or
  interruption the loop returns to awaiting input rather than dying.
- **A failed tool call is fed back to the model once** — the loop never
  retries silently. The bound (`--retries`) tracks the model's re-issuance of
  a failing *tool*, keyed by tool name with the streak per args, and
  cleared at the start of every turn: the bound strikes identical retries
  only, so a corrected call (args differing from the last failed args)
  resets its own streak and always executes. The limit-th consecutive
  failure of a call carries a note telling the model to read the error and
  change the call, or stop calling the tool; the next re-issuance of that
  call is refused without executing, naming the bound. A successful call
  clears the count — the bound tracks streaks, not history. Practical
  effect: persistent flapping on one broken tool gets a named refusal, not
  an infinite loop.
- **Unknown tool names** are fed back as errors; the turn continues.
- **Denials** (a tool outside the allow-list) are attributed with the reason
  and are countable by the bound — that pairing is the spec's core invariant
  and is proven by the suite, not trusted.

## interruption and failure semantics

- **Steering** — a line typed while a turn is live interrupts the turn and is
  delivered as the next user message when the loop re-enters the prompt (one
  slot, latest wins); a line typed between turns is served directly. The
  interrupt is the turn's own context, threaded onto the Input ctx
  (`core.WithInterrupt`) — there is no mailbox.
- **Ctrl-C** ends the session once the in-flight step unwinds: a mid-tool
  turn unwinds quickly (the tool's process group is killed), a mid-stream
  turn waits for the server's stream to close (the process stays alive in
  the meantime, and a second Ctrl-C is ignored: the first signal is the
  exit). Teardown surfaces cleanly (exit 0, session closed).
- A provider that closes the stream without a Done or Fault marker makes
  `rig` exit non-zero with a loud error. Silent termination is impossible by
  construction.

## narrowing what may execute

```sh
rig --allow read                 # inspection only: bash/write/edit denied
rig --allow bash,read            # run things, inspect things, change nothing
```

Anything not named is refused at the boundary with the reason named, and the
refusal goes back to the model. The default permits the 15 built-in
tools. Python plugins (outside the default) are admitted by their
presence in `~/.rig/plugins/` root (SPEC_PLUGINS 7) — an installed
plugin's own allow-list entry — not by an `allow` line; a plugin still
in `plugins/pending/` stays refused until approved (the approve's reload
lands it live, SPEC_STREAMLINE 4).
Narrowing is always available and compose-order-agnostic.

## working-directory discipline

The file tools normalize paths before any provenance decision, and `edit`
validates that the file is still what it was when last read — external drift
is named and the write is refused; ambiguous old-strings ("occurs N times")
are refused, never guessed at. Outputs are capped (bash 256 KiB, read 1 MiB)
and the truncation is named in the output.
