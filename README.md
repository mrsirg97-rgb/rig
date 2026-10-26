# rig

A small operating system for agents. The kernel is a few hundred lines.

rig assembles context, streams the model, executes tool calls, returns results, and repeats. The TUI, piped CLI, headless worker, and dashboard share the same session, task, memory, and scheduler stores.

Every rig instance on the machine is a member of one room over unix
sockets, and no process is the center: the model is a guarded key, the
instances own the models' children, and a swap and an update are
operations in the same order the requests are. With `mesh` set in
`settings.json` the room spans machines, their orders ordered by the
clocks the machines agree on and converged by gossip over a tailnet.

## install

One line, and the rest is `docs/SETUP.md`:

```sh
curl -fsSL https://tryrig.ai/install.sh | sh
```

POSIX sh, no Go, no sudo, installs to `~/.local/bin`; the release
binary, `go install`, `make install`, and `rig -update` are the other
paths, all documented in `docs/SETUP.md`.

### first run

```sh
./rig --base-url $ENDPOINT --model $NAME
```

rig needs an OpenAI-compatible SSE endpoint and a model ID, and a row
for that model — the binary ships no model rows, so naming an ID the
table doesn't know refuses at startup. The endpoint defaults to
`http://127.0.0.1:8090/v1`; there is no model default, and a run
without one refuses at start, naming the three ways to set it
(`--model`, `RIG_MODEL`, the `model` key in `settings.json`). The TUI
is the frontend when stdout is a terminal, the piped CLI otherwise.
For scripts, run `rig -p "the task"`, or `rig -p -` to take the prompt
from stdin — the worker path, capped at the read tool's ceiling.

#### in a session

- **tools**: the menu below; results capped, refusals named.
- **the queue**: `todo` is the project's present (worktrees share one board); `claim` takes the next unblocked task, `complete` lands it, tasks link with `requires`/`blocks`.
- **memory**: `rem learn`/`recall`/`reflect`/`prune`, plus `index`/`pack` over the project's code map, at the project's scope.
- **schedules**: `scheduler` puts a job on the crontab; a job is a one-shot `rig -p` in its own cwd, jailed by default.
- **the swarm**: `swarm start 2` drains the queue (the count rides start, capped at 16; `budget=5` caps the spend): workers claim, run one-shot, and submit; reviewers accept or reject.
- **resume**: `sessions` lists the vitals; `rig --resume <id>` replays a session from the state store in one read-only transaction.

The semantics behind all of it: `docs/USAGE.md`.

### configuration

Configuration lives in `~/.rig/` (`$RIG_HOME` moves it); every file is optional, every key resolves flag > env > file > embedded default, and a malformed file fails startup naming the file and the field.

**`settings.json`** — the knobs, flat, by their env names. These two
are the ones a run needs; everything else keeps its default:

```json
{
  "model": "local",
  "baseUrl": "http://127.0.0.1:8090/v1"
}
```

**`models.json`** — one row per model: its window, its budget, its
dials. This file *is* the table, so every row carries its four numbers:

```json
[
  {
    "id": "local",
    "window": 393216,
    "maxTokens": 65536,
    "reserve": 104858,
    "keepRecent": 98304,
    "role": "interactive",
    "effort": "xhigh",
    "efforts": ["low", "medium", "xhigh"],
    "vision": true
  },
  {
    "id": "sonnet",
    "window": 200000,
    "maxTokens": 8192,
    "reserve": 16384,
    "keepRecent": 40000,
    "provider": "openrouter",
    "baseUrl": "https://openrouter.ai/api/v1",
    "apiKey": "sk-or-…",
    "reasoning": "reasoning"
  },
  {
    "id": "claude",
    "window": 200000,
    "maxTokens": 8192,
    "reserve": 16384,
    "keepRecent": 40000,
    "provider": "anthropic",
    "baseUrl": "https://api.anthropic.com",
    "apiKey": "sk-ant-…",
    "thinkingBudget": 2048,
    "cacheControl": true,
    "inputPrice": 3,
    "outputPrice": 15,
    "cacheReadPrice": 0.3,
    "cacheWritePrice": 3.75
  }
]
```

A hosted endpoint is a model row: an OpenAI-compatible server
(OpenRouter, DeepSeek, any of them) speaks that wire as-is — `remote`
or `provider`, `baseUrl`, `apiKey`, bounded 429/5xx retry, and cost
accounting that feeds the TUI footer, the swarm's `budget=`, and a
job's `budget` — and `provider: "anthropic"` is the one named second
provider: native thinking (`thinkingBudget`), cache-control
breakpoints (`cacheControl`), and the cost column from the row's own
per-million prices. The row's fields and invariants: `docs/SETUP.md`
and `specs/SPEC_HOSTED.md`. Switch rows any time mid-session with
`/models sonnet`.

## what's different

**the engine.** One thread touches the session, and it is the loop.
Input, the model's stream, tool completions, and the fleet's messages
all arrive as closures on a single queue, ordered by priority first and
arrival second. Only the consumer touches the transcript, so there is
nothing to lock — the queue is the one synchronization point, and
nothing races. It removes time too: nothing polls, nothing sleeps on a
timeout; a step that must wait on the world spawns a goroutine and
posts its completion back, because the event *is* the wakeup.

Order is guaranteed. A parallel tool call is a goroutine that posts its
completion, and completions are applied in call order: three reads run
beside each other and land in the order the model asked, exactly as if
nothing ran in parallel. That is batching — admitted calls (reads,
views, fetches, delegates) run beside each other eight at a time, and
anything with an effects lands as a barrier between them. The model
gets concurrency; the transcript keeps its order.

Broadcast rides the same queue. A delegate's return posts above the
turn's own events, so the work you asked for is in hand at the boundary
of the turn in flight — a busy session is never a reason it waits. The
fleet's other messages — the reviewers, the swarm's notices — post below
the turn's own events, so they run in the gaps of a turn and never ahead
of it; a busy session delays the chatter and loses nothing, because the
stores hold the facts. And the runtime names no
concrete tool, provider, policy, frontend, or middleware: one file plus
one registration line extends it.

**the decision pipeline.** Point rig at a small decision server and it
runs as a sidecar over a queue of its own — no GPU negotiation, one
URL. You run that server yourself: `decisionUrl` is rig's half of a wire
(one `POST /v1/systemone` per batch, `decision/http.go:53`), the small
model behind it is yours to train, and `specs/SPEC_DECISION.md` governs
the contract. Three things happen: every bash call grows a pending question with
a risk proposal, the `decide` tool joins the menu so the model hands
over its sorting — classifying a thousand items by a typed question
(choice, score, yes/no) instead of reading them through context — and a
reviewer settles the pending rows in batches at the lowest priority, so
labeling happens in idle time and nothing ever waits on it.

The rule that makes it safe: an answer is a proposal an LLM reviews,
never an action. And every question, answer, confidence, and verdict
lands in one SQLite store beside the sessions — recording never changes
a decision, and a store error never fails a call.

That store is the training set. The pipeline is built in: a trainer is
one Python file in the train zone, run headless, never inside a turn.
rig exports the settled rows — an approved row keeps the proposer's
answer, a denied row keeps the reviewer's correction — splits them
80/20, and scores candidate, incumbent, and the constant baseline on
the same held-out rows. Promotion is a measured win: the candidate must
beat the baseline *and* the incumbent on every question — a tie is not
a beat — and it lands as a rewritten line in the systemd unit, printed
with the restart command, because rig restarts no service it does not
own. The sidecar model is rig's own, trained on rig's own traffic.

**words are the budget.** Everything the model is told about its tools
crosses the wire every turn, so the system prompt and the tool schemas
are lean, byte-stable, and carefully chosen. The whole menu aims under
15,000 characters and the wire job fails the build past 16,500 —
trimming is a decision, not a leak. Nothing is goldened: the wire job
renders the request bodies at the merge-base and at the head and posts
their diff, so a byte that moves is reviewed, not trusted. The
byte-stable prefix is why 99 cents of every prompt dollar serve from
cache.

**the room, no center.** Every rig instance on the machine sits in one
room over a unix socket in the rig home, and the model is a key in it.
Before a request, the instance claims the key at its class's priority —
the operator's turn 90, a delegate's return 70, a worker 30, a child's
start or stop 20, an outside client 0 — and the request goes to the row's
port itself once every live member has agreed the claim is first. No
process routes: llama-swap left the stack at 3.0.0 and nothing took its
place. A claim that finds the port silent starts the row's child and
waits for it, one child at a time, and `settings.json`'s `mesh` extends
one room to many machines, their orders ordered by the clocks they agree
on. Its face is the dashboard's room page — the machine's vitals, the
members, who holds which model, who waits, whether the port answers, the
served requests and the log's tail — and the page the dashboard opens
on; there is no slash command for it. `specs/SPEC_ROOM.md` is the design
and `docs/DESIGN.md` the summary.

**a worker does, the session decides.** The verbs that judge or delete
shared state belong to the session that owns the state. A delegated
worker gets the doing set — `bash read write edit view python web rem`
— and the session's own verbs (`todo` prune/accept/reject/move,
`scheduler` remove, `plugin` delete) leave its menu and refuse at its
gate; the swarm's workers inherit the same narrow set, and a reviewer
adds one verb of its own, `verdict`. The hand-off is async: the turn
that delegates gets one line naming the worker, and the worker's last
message arrives as a message of its own — at the live turn's next step,
or as the head of whatever you would have typed, in every frontend. The same queue makes the swarm cheap:
it orders by priority, not by source, so drain workers, reviewers, and
your own input share the loop, the jail, and the return path.

The loop never retries: a failed call executes once and the model is
told; denials are named refusals with reasons, results are capped with
loud markers. State belongs to the repo: tasks, memory, and schedules
carry the project's identity, shared by worktrees. Default deny at the
boundary — allow-list, approval gate, pathguard, plugin provenance,
worker jail; narrowing is the operator's act. Spec first: every
behavior is one sentence in specs/ with a test that holds it there, and
the core is frozen.

## measured

<!-- measured:begin (scripts/readme-measured; do not hand-edit) -->
Numbers, not adjectives — and each one names its mechanism. The block is computed: `scripts/readme-measured` reads the stores and the tree, CI refuses drift, so it cannot go stale on you. One SQL read of the state store: the store is the receipt.

**97.7% of 7.4 billion prompt tokens served from cache**, across 3,387 sessions and 66,545 recorded turns — the earliest on v0.2.0. The ratio is `cache_read / prompt`, the arithmetic `sessions summary` runs; the byte-stable prefix is why the provider can reuse so much of it. Cost per turn, era by era:

| era | turns | new tokens / turn | completion / turn | cache |
|---------|--------|------|------|--------|
| 0.x | 14,006 | 1,499 | 722 | 98.65% |
| 1.0–1.3 | 7,440 | 933 | 676 | 99.13% |
| 1.4–1.9 | 10,045 | 962 | 525 | 99.26% |
| 2.0–2.9 | 7,360 | 906 | 503 | 99.09% |
| 2.10.x | 14,785 | 3,946 | 667 | 96.37% |

The 2.1.x consolidation rethought the system prompt and the toolset and kept the machinery — the kink is visible. The 2.10.x row is the fleet's own traffic: swarm workers and delegates are short-lived sessions whose first turns cannot hit a cache that does not exist yet, and the models rotate; the interactive sessions of that era hold the ~98% line.

**A few thousand bytes of preamble.** The system prompt and the tool schemas are lean, byte-stable, and carefully chosen, and the tests say so: `TestSystemPromptIsByteStableAcrossBuilds`, `TestWireMarshalingIsDeterministic`, `TestWireMessagesAreAppendOnly`. A stray timestamp cannot quietly kill the cache.

**448 lines is the loop** — `loop.go` plus `batch.go`, stdlib only. **686 lines is the whole swarm** — claim, spawn, complete, verdict, reap, and the status throttle. **53,829 lines of Go, 88,433 of tests.** The one store dependency is pure-Go SQLite.

<!-- measured:end -->

The ratio spans providers, and the denominator is each endpoint's own
number: on the OpenAI wire the cached tokens sit inside `prompt`, and on
the Anthropic wire rig sums input with the cache read and the cache write
to land on the same convention (`provider/anthropic/stream.go:287`). One
ratio over both, and the convention is the reason it holds.

## the os

| OS concept | rig | where |
|---|---|---|
| kernel | `loop.Run(ctx, kernel)` over the typed seams; `loop.go` + `batch.go` are the 448 lines the block above names, stdlib-only | `loop/`, `core/`, `kernel.go` |
| scheduler | the turn's batch: concurrent reads beside each other, bounded by the kernel's Parallel (8), effects in call order; background jobs on the operator's crontab, model fires jailed | `loop/batch.go`, `tool/scheduler`, `store/scheduler` |
| processes | sessions (TUI, piped, one-shot), delegates, drain workers; each worker owns a transcript, sandboxed and resumable | `frontend/`, `tool/delegate`, `swarm/` |
| IPC | the event stream (`TextDelta`, `ToolCallEvent`, `ToolResult`, `TurnEnd`) and tool calls through the middleware chain; the stores are the shared state across processes | `core/provider.go`, `room/`, `store/` |
| the model's key | one key per model: a claim is ordered by priority (the operator's turn 90, a delegate's return 70, a fleet worker 30, a scheduled job 20, an outside client 0), the resident child belongs to the instance that started it, and one child runs at a time | `room/`, `cmd/rig/children.go`, `specs/SPEC_ROOM.md` |
| filesystem | the workspace through read/write/edit, provenance-canonicalized; the stores are SQLite files under the rig home | `tool/file`, `store/` |
| permissions | allowlist, approval gate, pathguard, plugin provenance, worker jail; deny by default, refusals named | `middleware/`, `policy/`, `specs/SPEC_SANDBOX.md` |
| modules | python plugins, one file one tool, pending until approved; typed Go seams beside them | `plugins/`, `core/` |
| shells | the frontends: TUI default, piped CLI, `-p` one-shot, web dashboard; user commands are the builtins | `frontend/`, `command/` |

## the tools

rig's default menu is 12 built-in tools on a model row without vision,
13 with `"vision": true` (`view` joins the set). `decide` joins when a
decision server is set (`decisionUrl`), making it 13/14; `verdict` is
never on the interactive menu, only on a fleet worker that holds the
pipe. `scheduler` is on the menu everywhere; `delegate` is wired where a
second request can run (a remote row, or an endpoint that answers one
live read of what it has loaded). Restrict them with `--allow`:

| tool | what it does |
|------|--------------|
| `bash` | run shell commands; output bounded |
| `read` / `write` / `edit` | files; read is the observation path (drift-checked), edits are exact-match, provenance-checked |
| `view` | look at pixels: an image (downscaled, content-addressed), a video's frames as one labelled contact sheet, or a live browser page driven by refs — sent to a vision model (off unless your model row has `"vision": true`) |
| `python` | a persistent IPython kernel; variables and imports survive |
| `web` | search a local SearXNG, or fetch a URL as readable text; private addresses refused |
| `todo` | the task queue, scoped to the project (a repo's worktrees share one); tasks link with `requires`/`blocks` |
| `rem` | memory across sessions: learn, recall, reflect, prune, and `index`/`pack` over the project's code map; scoped to the project |
| `scheduler` | background jobs on your crontab, run in a bubblewrap jail |
| `delegate` | a headless worker for a bounded subtask, handed off and returned on a later turn; wired where a second request can run (a remote row, or an endpoint that answers one live read of what it has loaded) |
| `sessions` | vitals of the session store (an older store is migrated on open) |
| `decide` | hand many items to a decision server against one typed question instead of reading them (on the menu only when `decisionUrl` is set) |
| `verdict` | deliver the reviewer's one word on work a worker was asked to review (registered only in a fleet worker that holds the pipe) |
| `plugin` | the door into your python plugins: run one, read its contract, or tend the ecosystem (list, create, delete, reload) |

Every tool result is capped. Repeated identical failures are bounded.
An optional round cap limits calls per turn. A failed call executes
once. A delegated or swarm worker runs a narrower board: the doing set
(`bash`, `read`, `write`, `edit`, `view`, `python`, `web`, `rem`) — the
session's verbs (`todo` prune/accept/reject/move, `scheduler` remove,
`plugin` `delete`) are off its menu and refused at its gate, and only a
reviewer holds `verdict`.

## plugins

A Python plugin is one file and one tool — `DESCRIPTION`, `SCHEMA`, a
`run` function, no build step. It is discovered at startup and reached
through the one `plugin` door: `run`, `schema`, and the ecosystem verbs
(`list`, `create`, `delete`, `reload`). A model-authored plugin lands
in the pending zone and stays untrusted until the operator installs it.
The contract, the zones, and the train zone: `docs/PLUGINS.md`.

## the dashboard

`rig serve` is rig with the page as its terminal: the live session
streamed in the TUI's grammar, plus sessions, the queue, the jobs, the
swarm, the models, the plugin forge, and the web tools — loopback only,
token-gated, installable to a phone home screen. `docs/SETUP.md` and
`specs/SPEC_SERVE.md`.

## docs

Read in this order: this file, `docs/SETUP.md` to get it running,
`docs/USAGE.md` for what a session does and how it fails, then
`docs/DESIGN.md` for the architecture, and a spec for the rules behind
any one behavior.

| doc                | what it is                                        |
|--------------------|---------------------------------------------------|
| `docs/SETUP.md`    | build, every knob, verification                   |
| `docs/USAGE.md`    | running a session; commands, and the session and failure semantics |
| `docs/DESIGN.md`   | architecture: the seams, the turn and the queue, the room and the mesh, the decision pipeline, the fleet, the code map, the extension guide |
| `docs/PLUGINS.md`  | the python plugins and the train zone             |
| `docs/EMBED.md`    | rig as a Go module: the seams, the freeze, a worked example |
| `docs/TUI_DESIGN.md` | the terminal frontend's implementation notes    |
| `specs/`           | the governing specs, written and agreed before the code; `specs/FREEZE.txt` is the gate that keeps the core closed |
| `SECURITY.md`      | the trust model and how to report a vulnerability |
| `CONTRIBUTING.md`  | the process: spec first, tests before code, the freeze |
| `docs/history/`    | dated notes: the roadmap that shipped, the consolidation read — never cite them as current |

The words rig uses for its own parts, in one place:

| word | what it names |
|---|---|
| room | the machine's rig instances, and the keys they order over (`room/`) |
| key | what a claim is taken on: one model, or the model table |
| claim | the ask to be served; first in the room's order takes the model |
| child | the model process an instance starts; one at a time, its log under the rig home |
| row | a model row in the table, a store's row, or the one background-status line the TUI keeps (3.2.0) |
| band | the TUI's footer rows for a running fleet |
| bite | one reviewer fire over the pending decision rows |
| board | the todo queue at a project's scope |
| door | one tool that carries many verbs (`plugin`, the room's front, the ask door) |
| zone | a directory with a trust level: `plugins/pending/`, `disabled/`, `train/` |

## layout

The short map; the full one is `AGENTS.md` and each package's
`PACKAGE.md`. The engine is not in this tree: `github.com/mrsirg97-rgb/evt`
is its own module, and its `broadcast` package is the room's primitive
(SPEC_EVT).

```
cmd/rig        the binary and composition root; wires every seam once
core           the seams, the wire types, the event vocabulary; types only
kernel.go      the dependency bag the loop drives, assembled from options
loop           the turn runtime: ordering, faults, cancellation
command        the seventeen slash commands, dispatchable with fakes
provider       the model wires: openai-compatible SSE, anthropic Messages
store          the SQLite stores: state, todo, rem, scheduler, graph, decision
tool           the model's words (registry.json) and one leaf per tool
middleware/ policy/  the tool chain and the context/provider decorators,
             including the operator link a delegated worker carries
frontend       four frontends: tui, cli, oneshot (the -p worker), web
room/ swarm/   the machine's room (SPEC_ROOM) and the drain controller
stats/         the machine's vitals, read from /proc, /sys and nvidia-smi
webtools/      the machine-local web-tools bundle: the bytes the binary
             carries and the verbs that build and run it (SPEC_WEBTOOLS)
decision/ plugins/  the decision seam (SPEC_DECISION) and the python zones
config/ models/ pathguard/ imagemarker/  the four-layer knobs, the model
             table, and the two stdlib leaves the tools and the wires share
```

## extending

The structural test is simple: add one file and one registration line. The loop never names a concrete tool, provider, policy, frontend, or middleware. A Python plugin needs no Go. See `docs/DESIGN.md`, `docs/PLUGINS.md`, and `CONTRIBUTING.md` for the process.
