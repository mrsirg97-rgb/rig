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
unknown id is loud). The per-process state; the guard's counts, the steering
slot; starts fresh, and the session's id is the one to look up in the
`sessions` table of the state store (`~/.rig/sessions/*.sqlite` under the
rig home; `$RIG_HOME` over `~/.rig`). `-p` one-shot and `--resume` refuse at construction: one-shot
stays one-shot.

## commands

Typed lines with a `/` prefix; the loop never sees them. An unknown command
is a loud line naming the known set, never silently a prompt.

- `/compact`: force a compaction now (the `⧉` line reports dropped/kept),
  or `compact: nothing to drop`.
- `/new`: close the session row ok, mint a fresh session, same process.
- `/project <path>`: close this session and open a fresh one whose
  workspace is that path (same process, same seam as `/new`); `~`
  expands, the path canonicalizes, and a non-directory refuses by name.
  The new session's system prompt carries that workspace's AGENTS.md.
- `/sessions`: list (as many rows as fit the screen; `list all` or
  `list <n>` for more); `summary` shows the vitals over the recent
  sessions (models, faults, the cache ratio); `show <id>` renders a
  transcript; `resume <id>` swaps to it in-process.
- `/models`: the per-model table with the active row marked; `/models <id>`
  switches for the next turn and moves the open session's model row with
  it, so the store records the current id rather than the open-time one.
- `/steer <text>`: queue for the next boundary (interrupts a live turn);
  bare `/steer` interrupts only.
- `/todo`, `/scheduler`: the same tools the model gets, same queue, same
  store; the tool's own refusals teach the shape.
- `/effort`: the reasoning dial: bare shows the active level and the
  model's available ones; `/effort <level>` sets it for subsequent turns
  (`specs/SPEC_MODES.md`).
- `/role`: the stance: `/role <default|architect|reviewer>` sets the
  session's role prose between the system prompt and AGENTS.md.
- `/approve`: `/approve auto` (today's behavior) or `/approve manual`:
  manual pauses every mutating tool call for the operator's y/n at the
  TUI ask row; a denial is a model-visible teaching refusal.
- `/rem`: the memory store's operator verbs: bare lists the live
  memories (as many as fit the screen; `list all` or `list <n>` for
  more), `show <id>` renders one, `forget <id>` drops it, `project
  <path> [all|<n>]` shows another project's memories.
- `/plugins`: the python plugins: the loaded ones (name, description,
  file), the skipped ones with their reasons, and the pending zone:
  `pending` lists the model's authoring with each file's DESCRIPTION,
  `approve <name>` installs one (the operator's verb), `disabled`
  lists the disabled zone, `disable <name>` and `enable <name>` move
  a plugin across it, `reload` re-registers from disk (the `plugins`
  tool's command door), `create <text>` queues the authoring prompt.
- `/decision train <trainer>`: enqueue the decision model's training
  run off the turn — `<trainer>` is a trainer file's name (its filename
  stem, validated like a plugin name), and the command lands a one-shot
  job on the scheduler that runs headless and never inside the turn; a
  bare `/decision` reports the usage instead (2.13.0; the nightly is the
  operator's own cron line).
- `/todo project [path]`: shows a queue — with a path that project's,
  bare this workspace's. Every todo call names its scope: the workspace
  path, or global (the tool refuses without it). `todo <path> <verb>`
  acts there in one line, and `/todo prune` drops the done rows. The swarm surface (1.3.9): `claim`
  takes the next task nothing waits for (or `claim review`), `note
  <id>` attaches a message to any task, `notes <id>` lists a task's
  notes in order with their session and time, `read <id>` renders one
  task summary-only, `done` lands it done in a solo session and submits
  it for review from a worker (`rig -p`), and `accept`/`reject` decide.
  `read` is the present — open work first, then related and recent
  finished, ten rows total with the hint naming what is hidden — and
  `list finished <n>` lists the n most recent finished (default 10,
  cap 100); `read all:true` stays the operator's full-history read.
  Tasks carry two links, one each: `requires tN` (I wait for it) and
  `blocks tN` (it waits for me), gating claim and finish
  (`specs/SPEC_TODO_EDGES.md`).
- `/rem project <path>`: a one-off read of another project's memories:
  the path resolves to a repo identity (worktrees share).
- `/swarm`: the drain workers (1.4.0). Bare lists the supervisor's
  workers (`2 workers · 1 running`, then `w1 [~] worker resident · task t3 ·
  heartbeat 2s ago · done 1 failed 0`); `swarm start <count> [role=worker|reviewer] [model=<id>] [budget=<dollars>]`
  starts that many workers (a budget stops the
  controller's claims at the cap with a notice, the spend summed from
  the recorded run costs); the reply is `swarm: added N agents (role X ·
  model M)` whether the swarm was empty or running — one router on the
  session's queue is the only reader of it, and it hands each ready
  task to an idle worker on the events (a task created, completed, or
  a worker finishing); each claims a task,
  spawns a one-shot `rig -p` through the delegate path (jail, socket
  proxy, recorded run), and finishes it itself: workers submit for
  review, reviewers take the verdict the worker delivered with the
  `verdict` tool and call `accept`/`reject`. A worker on a hosted row
  skips the local swap and the gate entirely. The drain pair is wired
  only where a second request can run — the session's model row is
  remote, or the resident swap answers one live read (one slot hosts it;
  a queued request waits at the server; `workers: false` turns it off;
  the scheduler is
  wired everywhere; `/swarm` names the reason when it refuses).
  Against a running swarm a
  start adds workers (the roles mix); `swarm stop` ends it and releases
  the in-flight claims (`swarm: stopped N agents`; `specs/SPEC_SWARM.md`).
  The task workers run
  with no stall and no timeout on the spawn — the worker's own context
  (the swarm's) is the only bound, so one lives until it exits or the
  swarm stops; every abnormal end is recorded on the run (`canceled` or
  `killed by signal N`), never only as a log marker. A dead
  worker's claim
  is released and the task retried once; a second death fails it (or
  rejects it with the reason). No fleet configured refuses by name.
  The TUI shows the swarm beside the session: the transcript gets one
  line at each decision (a task failed with its note, a reviewer
  rejected with the reason, a worker died and was restarted or exited,
  the board emptied or the swarm stopped), and the footer's status band
  carries the live counts below the status line, behind a short dim
  rule (`workers 2 · +3 ✓5 ✕1 · w2 t388 12s` / `reviewer 1 · ⧗1 ✓1 ✕0 ·
  w3 t386 4m`) — one row per role while a swarm runs, none when nothing
  runs (1.5.4); a delegated batch takes two rows of its own while it runs,
  the batch's count and elapsed over the most recent call of any of its
  workers (`delegating · 3 workers · 1m12s` / `#2 edit tool/file/edit.go ·
  12s`), and it keeps them breathing between turns (2.14.0). The usage
  line shows the session's dollars when the endpoint reported a cost
  (`up 214k down 18k · cache r 187k 87% · $1.23`, 1.5.0).
- `/theme`: the interface theme (2.3.2): bare shows the active preset;
  `/theme warm|cool|custom` sets it — warm is the default palette,
  cool is the cool palette, and custom is `theme.json` in the rig
  home, which refuses by name when the file is absent. The choice is
  written to the `theme` key of settings.json (persistent), and the
  TUI repaints immediately: new output in the new theme, committed
  scrollback keeps its bytes.

**Hosted rows** (`specs/SPEC_HOSTED.md`): a row with `remote: true` or
`provider: "<name>"` speaks the OpenAI wire at its `baseUrl`, the key
riding `Authorization: Bearer <key>` (from the file or
`RIG_MODEL_API_KEY`, never logged); 429 and 5xx retry with bounded
backoff instead of faulting a turn (`retries`, default 3 for remote
rows); `usage.cost` rides the endpoint's `usage.cost` into the state
store's cost column and shows in the usage line. OpenRouter rows read
and echo `reasoning` / `reasoning_details`; everything else keeps
`reasoning_content`. Remote rows omit the llama-server-only fields.

Context compacts automatically at the active model's own trigger (the
models table); the `⧉` line reports it. The summary lands in the
transcript only; context, not memory: compaction writes nothing to
rem (`specs/SPEC_STATE.md`: rem is deliberate).

The `delegate` tool (SPEC_DELEGATE) spawns a headless worker on a task
now: a bounded sub-task whose result is a message, not a conversation,
on the resident model (the session's default when nothing is
resident), in a cwd under your session's or the rig home — wired
wherever the worker tools are on and the swap is readable (one slot
hosts it; the request queues at the server).
Several delegate calls in one turn run in parallel, and none of them
block the turn (2.14.0): each answers at once with
`delegate: worker #2 started · session <id> · log <path>` and keeps
working. When a worker finishes, its return arrives as the head of the
next turn — `delegate #2 returned · exit 0 · 4m12s · session <id>` over
its output — and if nothing was running, the return starts that turn by
itself. Two returns that land during one turn come as one block, in the
order they finished, before whatever you had typed. While a batch runs
the status footer carries the band: `delegating · 3 workers · 1m12s` and
the most recent call of any of them. There is no timeout to set: a
worker lives until it exits or the session ends (waiting behind nine
others in a slot is work, not a hang); esc on an empty prompt with no turn
live stops every worker you have running, and in the dashboard the send
button does the same while a batch is out. A model that is not resident refuses by name, naming the holder
— never an eviction from inside a turn. The
run is recorded in the one scheduler store under an ad-hoc key, so
`scheduler runs` shows it beside cron runs, and the worker's
transcript is resumable with `sessions resume <id>`. A piped session
(`rig -p`) has no next turn to carry a return, so there the delegate
still waits and answers with the worker's message.

## what you see

The piped CLI's rendering is deliberately plain and greppable (the
terminal default is the TUI; see `docs/SETUP.md`; the CLI stays the byte
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
  `NAME ✓ <duration>` (`✕` when it failed); what executed is visible, not
  implied; a guarded refusal fails the row and says so;
- a `view` row is the exception to the output: `● view · shot.png ·
  2560x1440 -> 1568x882 · 412 KB` and nothing else, because no picture is
  ever painted into the transcript (the bytes go to the model, from the
  content-addressed store under your rig home; `docs/SETUP.md`);
- the usage line closes every turn: `↑prompt ↓completion · cache read hit%`
 ; the turn's totals across its model calls, pane's token shaping, the hit
  rate as cached-over-prompt;
- faults render as `[fault] <reason>` and the turn stops there: the session
  survives and the next prompt resumes at the last complete message.

## images

`view` is rig's eyes, and it is off until you say otherwise: a model row
carries `"vision": true` in `~/.rig/models.json` and the tool appears, for
that model only (switching models moves it, `/models`). A view call reads
the file, caps the longest side at 1568 px, and stores the result under
`~/.rig/blobs/` named by its sha256; the transcript keeps one line, and the
picture is re-read from the store on every turn that sends it. So: the same
image twice costs one blob and no extra prompt, a screenshot never bloats
compaction, and deleting `~/.rig/blobs/` is always safe — the next look
re-writes what it needs. `view` never edits and never records the file as
seen; reading a picture is not a license to `edit` it.

## session behavior

- **The conversation persists** across turns within a run: after a fault or
  interruption the loop returns to awaiting input rather than dying.
- **A failed tool call is fed back to the model once**: the loop never
  retries silently. The bound (`--retries`) tracks the model's re-issuance of
  a failing *tool*, keyed by tool name with the streak per args, and
  cleared at the start of every turn: the bound strikes identical retries
  only, so a corrected call (args differing from the last failed args)
  resets its own streak and always executes. The limit-th consecutive
  failure of a call carries a note telling the model to read the error and
  change the call, or stop calling the tool; the next re-issuance of that
  call is refused without executing, naming the bound. A successful call
  clears the count; the bound tracks streaks, not history. Practical
  effect: persistent flapping on one broken tool gets a named refusal, not
  an infinite loop.
- **Unknown tool names** are fed back as errors: the turn continues.
- **Denials** (a tool outside the allow-list) are attributed with the reason
  and are countable by the bound; that pairing is the spec's core invariant
  and is proven by the suite, not trusted.

## interruption and failure semantics

- **Steering**: a line typed while a turn is live interrupts the turn and is
  delivered as the next user message when the loop re-enters the prompt (one
  slot, latest wins); a line typed between turns is served directly. The
  interrupt is the turn's own context, threaded onto the Input ctx
  (`core.WithInterrupt`); there is no mailbox.
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
refusal goes back to the model. The execution allow-list is not vision-gated:
twelve names in the embedded `settings.json`, fourteen once `scheduler` and
`delegate` join them (they do whenever the settings file carries no `allow`
key; write the key and you decide). What the model is offered is a different
count — the menu: twelve on a model row without vision, thirteen with
`vision: true` (`view` registers only there), `decide` only where
`decisionUrl` is set. `scheduler` is wired everywhere; `delegate` registers
only where a second request can run. Python
plugins (outside the
default) are admitted by their
presence in `~/.rig/plugins/` root (SPEC_PLUGINS 7); an installed
plugin's own allow-list entry; not by an `allow` line; a plugin still
in `plugins/pending/` stays refused until approved (the approve's reload
lands it live, SPEC_STREAMLINE 4).
Narrowing is always available and compose-order-agnostic.

## working-directory discipline

The file tools normalize paths before any provenance decision, and `edit`
validates that the file is still what it was when last read; external drift
is named and the write is refused. Edit takes `path`, `old` and `new`:
one change per call, and several changes to one file are several calls
in one turn, applied in call order, each drift-checked against what the
call before it left. An edit of a file the session has not
read applies when `old` matches exactly once; on a miss it hands back the
file's text (capped like a read) so the next call edits from it.
Ambiguous old-strings are never guessed at: a read file's edit refuses
naming the count, an unread file's edit hands back the text. Outputs are
capped (bash 256 KiB, read 1 MiB)
and the truncation is named in the output; a read streams the file, so a
huge file is never materialised through a read.
