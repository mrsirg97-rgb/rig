# rig

A small operating system for agents. The kernel is a few hundred lines.

rig assembles context, streams the model, executes tool calls, returns results, and repeats. The TUI, piped CLI, headless worker, and dashboard share the same session, task, memory, and scheduler stores.

## what's different

**the engine.** One thread touches the session, always: the turn runs on an event loop — one consumer, many producers, work arriving as closures ordered by priority then arrival. "Parallel tool calls are goroutines that *post their completion*; the loop applies completions in call order; nothing needs a lock except the queue" (`specs/SPEC_EVT.md`). Three reads run beside each other and land in the order the model asked, as if nothing ran in parallel. The fleet shares the loop: the room posts below the turn's own events, "so a worker's message runs in the gaps of a turn and never ahead of it" (`specs/SPEC_EVT.md` 8). And the runtime names no concrete tool, provider, policy, frontend, or middleware: one file plus one registration line extends it.

**the decision pipeline.** The gates record what they decide — the refusal, the skip, the approval — into one sqlite store of questions, answers, confidences, and verdicts. The rule that names the shape: "an answer is a proposal an LLM reviews, never an action" (`specs/SPEC_DECISION.md`). The decision model trains from those same rows: the settled rows go out, a candidate checkpoint comes back, and it serves only once it beats the constant baseline and the incumbent on every question — a tie is not a beat (`specs/SPEC_DECISION.md` 2.13.0).

**words are the budget.** Everything the model is told about its tools crosses the wire every turn. The whole menu — every description plus every schema — is pinned under 15,000 characters by a case in `cmd/rig`; the wire job fails past 15,500, the two numbers carried in the job's environment (`specs/SPEC_CORE.md`, `specs/SPEC_BUILD.md`). Nothing is goldened: `scripts/wire-check` renders the request bodies at the merge-base and at the head and posts their diff, so a byte that moves is reviewed, not trusted — the byte-stable prefix is why 99% of prompt tokens serve from cache.

**a worker does, the session decides.** "The verbs that judge or delete shared state belong to the session that owns the state" (`specs/SPEC_WORKERS.md`); the worker gets the doing set — `bash read write edit view python web rem` — and the operator's verbs (`todo` prune/accept/reject/move, `scheduler` remove, `plugin` delete) leave its menu and refuse at its gate (`specs/SPEC_DELEGATE.md`). The hand-off is async: the turn that delegates gets one line naming the worker, and the worker's last message arrives on the next turn — as the head of whatever you would have typed, in every frontend.

The loop never retries: a failed call executes once and the model is told; denials are named refusals with reasons, results are capped with loud markers. State belongs to the repo: tasks, memory, and schedules carry the project's identity, shared by worktrees. Default deny at the boundary — allow-list, approval gate, pathguard, plugin provenance, worker jail; narrowing is the operator's act. Spec first: every behavior is one sentence in specs/ with a test that holds it there, and the core is frozen.

## measured

Each number names its mechanism.

- **99.0% of 4.1B prompt tokens served from cache, over every recorded turn.** The fleet's stores hold 1,529 sessions and 36,125 turns, the earliest on 0.2.0, three days after v0.1.0. The ratio is `cache_read/prompt` from the usage table, the same arithmetic `sessions summary` uses (`TestSessionsSummaryCacheRatioFixture`); the prefix is byte-stable, so the provider's cache reuses it. The cost per turn is the curve:

| era | turns | new tokens / turn | completion / turn | cache |
|-------|--------|------|------|--------|
| 0.x | 14,006 | 1,499 | 722 | 98.65% |
| 1.0–1.3 | 7,440 | 933 | 676 | 99.13% |
| 1.4–1.9 | 10,045 | 962 | 525 | 99.26% |
| 2.x | 4,634 | 711 | 438 | 99.24% |

The 2.1.x consolidation rethought the system prompt and the toolset and kept the machinery; the kink is visible. One SQL read of the state store: the store is the receipt.
- **7k byte-stable preamble.** The system prompt, the tool schemas, and the append-only transcript are a few thousand bytes, pinned by `TestWireMarshalingIsDeterministic`, `TestWireMessagesAreAppendOnly`, and `TestSystemPromptIsByteStableAcrossBuilds` (and the wire job's diff, which renders the request bodies at the merge-base and at the head), so a stray timestamp cannot silently kill the cache.
- **650 lines for the swarm.** The supervisor board's non-test Go: claim, spawn, complete, verdict, reap, and the status throttle.
- **45,941 lines of Go, 72,411 lines of tests.** Core and loop are stdlib-only; the one store dependency is pure-Go SQLite.

## the os

| OS concept | rig | where |
|---|---|---|
| kernel | `loop.Run(ctx, kernel)` over the typed seams; `loop.go` + `batch.go` are 409 lines, stdlib-only | `loop/`, `core/`, `kernel.go` |
| scheduler | the turn's batch: concurrent reads beside each other, bounded by the kernel's Parallel (8), effects in call order; background jobs on the operator's crontab, model fires jailed | `loop/batch.go`, `tool/scheduler`, `store/scheduler` |
| processes | sessions (TUI, piped, one-shot), delegates, drain workers; each worker owns a transcript, sandboxed and resumable | `frontend/`, `tool/delegate`, `swarm/` |
| IPC | the event stream (`TextDelta`, `ToolCallEvent`, `ToolResult`, `TurnEnd`) and tool calls through the middleware chain; the stores are the shared state across processes | `core/provider.go`, `evt/`, `store/` |
| filesystem | the workspace through read/write/edit, provenance-canonicalized; the stores are SQLite files under the rig home | `tool/file`, `store/` |
| permissions | allowlist, approval gate, pathguard, plugin provenance, worker jail; deny by default, refusals named | `middleware/`, `policy/`, `specs/SPEC_SANDBOX.md` |
| modules | python plugins, one file one tool, pending until approved; typed Go seams beside them | `plugins/`, `core/` |
| shells | the frontends: TUI default, piped CLI, `-p` one-shot, web dashboard; user commands are the builtins | `frontend/`, `command/` |

## the tools

rig's default menu is 12 built-in tools on a model row without vision,
13 with `"vision": true` (`view` joins the set). `decide` joins when a
decision server is set (`decisionUrl`), making it 13/14; `verdict` is
never on the interactive menu, only on a fleet worker that holds the
pipe. `scheduler` is on the menu everywhere; `delegate` is wired where
a second request can run (a remote row, or a readable swap). Restrict
them with `--allow`:

| tool | what it does |
|------|--------------|
| `bash` | run shell commands; output bounded |
| `read` / `write` / `edit` | files; read is the observation path (drift-checked), edits are exact-match, provenance-checked |
| `view` | look at an image: downscaled, content-addressed, sent to a vision model (off unless your model row has `"vision": true`) |
| `python` | a persistent IPython kernel; variables and imports survive |
| `web` | search a local SearXNG, or fetch a URL as readable text; private addresses refused |
| `todo` | the task queue, scoped to the project (a repo's worktrees share one); tasks link with `requires`/`blocks` |
| `rem` | memory across sessions: learn, recall, reflect, prune; scoped to the project |
| `scheduler` | background jobs on your crontab, run in a bubblewrap jail |
| `delegate` | a headless worker for a bounded subtask, handed off and returned on a later turn; wired where a second request can run (a remote row, or a readable swap) |
| `sessions` | vitals of the session store (an older store is migrated on open) |
| `decide` | hand many items to a decision server against one typed question instead of reading them (on the menu only when `decisionUrl` is set) |
| `verdict` | deliver the reviewer's one word on work a worker was asked to review (registered only in a fleet worker that holds the pipe) |
| `plugin` | the door into your python plugins: run one, read its contract, or tend the ecosystem (list, create, delete, reload) |

Every tool result is capped. Repeated identical failures are bounded. An
optional round cap limits calls per turn. A failed call executes once.

## install

One line, and the rest is `docs/SETUP.md`:

```sh
curl -fsSL https://mrsirg97-rgb.github.io/rig/install.sh | sh
```

POSIX sh, no Go, no sudo, installs to `~/.local/bin`; the release
binary, `go install`, `make install`, and `rig -update` are the other
paths, all documented in `docs/SETUP.md`.

## first run

```sh
./rig --base-url $ENDPOINT --model $NAME
```

rig needs an OpenAI-compatible SSE endpoint and a model ID. The endpoint defaults to `http://127.0.0.1:8090/v1`; there is no model default. a run without one refuses at start, naming the three ways to set it (`--model`, `RIG_MODEL`, the `model` key in `settings.json`). The TUI is the frontend when stdout is a terminal, the piped CLI otherwise. For scripts, run `./rig -p "the task"`.

## in a session

- **tools**: the menu above; results capped, refusals named.
- **the queue**: `todo` is the project's present (worktrees share one board); `claim` takes the next unblocked task, `complete` lands it, tasks link with `requires`/`blocks`.
- **memory**: `rem learn`/`recall`/`reflect`/`prune` at the project's scope.
- **schedules**: `scheduler` puts a job on the crontab; a job is a one-shot `rig -p` in its own cwd, jailed by default.
- **the swarm**: `swarm start 2` drains the queue (the count rides start, capped at 16; `budget=5` caps the spend): workers claim, run one-shot, and submit; reviewers accept or reject.
- **resume**: `sessions` lists the vitals; `rig --resume <id>` replays a session from the state store in one read-only transaction.

The semantics behind all of it: `docs/USAGE.md`.

## configuration

Configuration lives in `~/.rig/` (`$RIG_HOME` moves it); every file is optional, every key resolves flag > env > file > embedded default, and a malformed file fails startup naming the file and the field. `docs/SETUP.md` owns every knob: the files, the knob table, the sandbox, the model table, hosted rows, the dashboard. A hosted endpoint (OpenRouter, DeepSeek, any remote OpenAI-compatible server) is a model row, not a new provider: `remote`/`provider`, `baseUrl`, `apiKey`, bounded 429/5xx retry, cost accounting and `budget=` caps — one row in `models.json` (`docs/SETUP.md`, `specs/SPEC_HOSTED.md`).

## plugins

A Python plugin is one file and one tool — `run` and `schema`, no build
step; model-authored plugins land in the pending zone and the operator
installs them. The contract, the zones, and the train zone:
`docs/PLUGINS.md`.

## the dashboard

`rig serve` is rig with the page as its terminal: the live session
streamed in the TUI's grammar, plus sessions, the queue, the jobs, the
swarm, the models, and the plugin forge — loopback only, token-gated,
installable to a phone home screen. `docs/SETUP.md` and
`specs/SPEC_SERVE.md`.

## docs

| doc                | what it is                                        |
|--------------------|---------------------------------------------------|
| `docs/DESIGN.md`   | architecture: the seams, the turn, the decision pipeline, the fleet, the extension guide |
| `docs/SETUP.md`    | build, every knob, verification                   |
| `docs/USAGE.md`    | running a session; session and failure semantics  |
| `docs/PLUGINS.md`  | the python plugins and the train zone             |
| `docs/EMBED.md`    | rig as a Go module: the seams, the freeze, a worked example |
| `docs/TUI_DESIGN.md` | the terminal frontend's implementation notes    |
| `SECURITY.md`      | the trust model and how to report a vulnerability |
| `CONTRIBUTING.md`  | the process: spec first, tests before code, the freeze |
| `docs/history/`    | dated notes: the roadmap that shipped, the consolidation read |

## layout

Ten names; the full map is `AGENTS.md` and each package's `PACKAGE.md`.

```
cmd/rig        the binary and composition root; wires every seam once
core           the seams, the wire types, the event vocabulary; types only
loop           the turn runtime: ordering, faults, cancellation
evt            the event loop the turn and the fleet run on (SPEC_EVT)
store          the SQLite stores: state, todo, rem, scheduler, graph, decision
tool           the model's words (registry.json) and one leaf per tool
middleware/ policy/  the tool chain and the context/provider decorators,
             including the operator link a delegated worker carries
frontend       four frontends: tui, cli, oneshot (the -p worker), web
broadcast/ swarm/    the fleet's room (SPEC_SWARM) and the drain controller
decision/ plugins/  the decision seam (SPEC_DECISION) and the python zones
```

## extending

The structural test is simple: add one file and one registration line. The loop never names a concrete tool, provider, policy, frontend, or middleware. A Python plugin needs no Go. See `docs/DESIGN.md`, `docs/PLUGINS.md`, and `CONTRIBUTING.md` for the process.
