# rig

A small operating system for agents. The kernel is a few hundred lines.

rig assembles context, streams the model, executes tool calls, returns results, and repeats. The TUI, piped CLI, headless worker, and dashboard share the same session, task, memory, and scheduler stores.

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
- **7k byte-stable preamble.** The system prompt, the tool schemas, and the append-only transcript are a few thousand bytes, pinned by `TestWireToolsPrefixGolden` (a sha256 over the fleet's wire shape), `TestWireMarshalingIsDeterministic`, `TestWireMessagesAreAppendOnly`, and `TestSystemPromptIsByteStableAcrossBuilds`, so a stray timestamp cannot silently kill the cache.
- **720 lines for the swarm.** The supervisor board's non-test Go: claim, spawn, complete, verdict, reap, and the status throttle.
- **34,336 lines of Go, 53,815 lines of tests.** Core and loop are stdlib-only; the one store dependency is pure-Go SQLite.

## what's different

- **the loop is closed.** The turn runtime names no concrete tool, provider, policy, frontend, or middleware. One file plus one registration line extends it.
- **the wire is pinned.** The exact bytes sent to the model are golden-tested. The cache win is a measured property, not a claim.
- **the loop never retries.** A failed call executes once and the model is told. Results are capped with loud markers; denials are named refusals with reasons.
- **state belongs to the repo.** Tasks, memory, and schedules carry the project's identity, shared by worktrees. A session resumes from the store in one read-only transaction.
- **decisions are kept, answers are reviewed.** The gates record what they decide, an optional decision server proposes and an LLM reviews: one sqlite store of questions, answers, confidences, and verdicts (SPEC_DECISION) — proposals, never actions.
- **default deny at the boundary.** Allowlist, approval gate, pathguard, plugin provenance, worker jail. Narrowing is the operator's act.
- **spec first.** Every behavior is one sentence in specs/ with a test that holds it there. Core is frozen.

## the os

| OS concept | rig | where |
|---|---|---|
| kernel | `loop.Run(ctx, kernel)` over the typed seams; `loop.go` + `batch.go` are 417 lines, stdlib-only | `loop/`, `core/`, `kernel.go` |
| scheduler | the turn's batch: concurrent reads beside each other, bounded by the kernel's Parallel (8), effects in call order; background jobs on the operator's crontab, model fires jailed | `loop/batch.go`, `tool/scheduler`, `store/scheduler` |
| processes | sessions (TUI, piped, one-shot), delegates, drain workers; each worker owns a transcript, sandboxed and resumable | `frontend/`, `tool/delegate`, `swarm/` |
| IPC | the event stream (`TextDelta`, `ToolCallEvent`, `ToolResult`, `TurnEnd`) and tool calls through the middleware chain; the stores are the shared state across processes | `core/provider.go`, `evt/`, `store/` |
| filesystem | the workspace through read/write/edit, provenance-canonicalized; the stores are SQLite files under the rig home | `tool/file`, `store/` |
| permissions | allowlist, approval gate, pathguard, plugin provenance, worker jail; deny by default, refusals named | `middleware/`, `policy/`, `specs/SPEC_SANDBOX.md` |
| modules | python plugins, one file one tool, pending until approved; typed Go seams beside them | `plugins/`, `core/` |
| shells | the frontends: TUI default, piped CLI, `-p` one-shot, web dashboard; user commands are the builtins | `frontend/`, `command/` |

## install

Choose one:

**Installer** (POSIX sh, no Go, no sudo; installs to `~/.local/bin`):

```sh
curl -fsSL https://mrsirg97-rgb.github.io/rig/install.sh | sh
```

**Release binary** from `releases/latest`. Choose your `<os>_<arch>`:

```sh
curl -fsSL https://github.com/mrsirg97-rgb/rig/releases/latest/download/rig_linux_amd64 -o rig
chmod +x rig
```

**go install** (needs Go ≥ 1.26.6; the core is stdlib-only):

```sh
go install github.com/mrsirg97-rgb/rig/v2/cmd/rig@latest
```

`rig -update` fetches, verifies, and atomically installs the latest release. The running process keeps the old binary until restart.

## first run

```sh
./rig --base-url $ENDPOINT --model $NAME
```

rig needs an OpenAI-compatible SSE endpoint and a model ID. The endpoint defaults to `http://127.0.0.1:8090/v1`; there is no model default. a run without one refuses at start, naming the three ways to set it (`--model`, `RIG_MODEL`, the `model` key in `settings.json`). The TUI is the frontend when stdout is a terminal, the piped CLI otherwise. For scripts, run `./rig -p "the task"`. See `docs/SETUP.md` for configuration.

## a day with rig

- **first prompt.** `./rig` opens the TUI; `./rig -p "the task"` runs one
  prompt headless. `--base-url` and `--model` point at the endpoint, or set
  `RIG_BASE_URL` and `RIG_MODEL`; `settings.json` is the fallback.
- **tools.** `bash`, `read`/`write`/`edit`, `python`, `web` (search and
  fetch), `todo`, `rem`, `scheduler`, `delegate`, `sessions`,
  `plugin`/`plugins`. Results are capped, refusals are named.
- **the queue.** `todo` reads the project's present (worktrees share one
  board): open work first, then the related and recent finished, ten
  rows total, the hint naming what is hidden; `todo finished` lists the
  n most recent finished. `todo claim` takes the next unblocked task,
  `todo complete` lands it, `todo notes tN` lists a task's notes. Tasks
  link with `requires` and `blocks`.
- **memory.** `rem learn`/`recall`/`reflect`/`prune` at the project's scope;
  a repo and its worktrees share the same memories.
- **schedules.** `scheduler` puts a job on the crontab; a job is a one-shot
  `rig -p` in its own cwd, jailed by default.
- **the swarm.** `swarm start` drains the queue, one drain worker per
  free slot on the resident model: workers claim, run one-shot, and
  submit; reviewers accept or reject. `swarm stop` ends it;
  `swarm start budget=5` caps the spend. The drain pair is wired only
  where a second request can run — a remote row, or more than one slot
  on the resident server (`workers: false` turns it off; the scheduler
  is wired everywhere).
- **resume.** `sessions` lists the vitals; `rig --resume <id>` replays a
  session from the state store in one read-only transaction.

## the tools

rig's default menu is 13 built-in tools on a model row without vision;
`view` joins for a row whose `"vision": true` says it takes images.
`scheduler` and `delegate` are on the menu everywhere and refuse by name
where no worker fleet stands. Restrict them with `--allow`:

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
| `delegate` | a headless worker for a bounded subtask; wired where a second request can run (a remote row, or more than one slot on the resident server) |
| `sessions` | vitals of the session store (an older store is migrated on open) |
| `plugin` / `plugins` | the door into your python plugins, and their ecosystem |

Every tool result is capped. Repeated identical failures are bounded. An
optional round cap limits calls per turn. A failed call executes once.

## hosted mode

The provider speaks the OpenAI wire as-is, so a hosted endpoint
(OpenRouter, DeepSeek's API, any remote OpenAI-compatible server) is a
model row, not a new provider. A row says where it runs:

```json
{"id": "openrouter-sonnet", "window": 200000, "maxTokens": 8192, "reserve": 16384, "keepRecent": 40000,
 "provider": "openrouter", "baseUrl": "https://openrouter.ai/api/v1",
 "apiKey": "sk-or-...", "reasoning": "reasoning",
 "providerPin": "Together", "cacheControl": true}
```

The row's key rides `Authorization: Bearer <key>` (from the file or
`RIG_MODEL_API_KEY`, never logged); 429 and 5xx retry with bounded
backoff and jitter instead of faulting a turn; `usage.cost` lands in
the state store's cost column and shows in the TUI footer; OpenRouter
rows read and echo `reasoning` / `reasoning_details` while everything
else keeps `reasoning_content`; remote rows omit llama-server-only
fields. A remote row's delegate and scheduled fire skip the local swap
entirely — no gate at all; the endpoint's own 429 retry is the
backpressure. `swarm start budget=5`
and a scheduled job's `budget` cap spend in dollars, summed from the
cost column (SPEC_HOSTED).

## subagents

`delegate` runs a bounded sub-task on a headless worker and waits for its
last message. In one turn you can fan out several delegates: they run in
parallel, the turn blocks until each finishes or times out, and the free
slots llama-swap reports live bound how many run — one with no free slot
refuses (`no free slot; this turn holds the only one` on a one-slot
model). A worker runs on the resident model; a model you name asks for a
swap only when nothing is resident, and a different resident model
refuses, naming the holder (never an eviction from inside a turn).
Workers are sandboxed, cannot delegate in turn (`RIG_DELEGATE`), and their
transcripts are resumable with `sessions resume <id>`.

## plugins

A Python plugin is one file and one tool. It provides `run` and `schema`.
There is no build step. Model-authored plugins land in `~/.rig/plugins/pending/`. Approve, disable, and reload them with `/plugins` or the dashboard. See `docs/PLUGINS.md`.

## configuration

Configuration lives in `~/.rig/`. Set `$RIG_HOME` to move it. Every file is optional. Invalid files fail startup and name the file and field.

| file | what it holds |
|------|---------------|
| `settings.json` | the knobs: endpoint, model, the allow-list, the retry bound, the approval dial, the worker sandbox |
| `models.json` | the per-model table: context window, max tokens, the compaction reserve, the role (`worker`/`interactive`), the effort levels, `vision` (the model takes images, which unlocks `view`), and the hosted run site: `remote`/`provider` (where it runs), `baseUrl`, `apiKey`, `reasoning`, `providerPin`, `cacheControl`, `retries` |
| `blobs/` | the images `view` has read, named by sha256; delete anything, and rig never rewrites a file it did not create |
| `AGENTS.md` | global instructions, read before the project's `<cwd>/AGENTS.md` |
| `theme.json` | the terminal theme: base, slot colors, glyph set |
| `plugins/` | your python plugins (top-level files are live) |

Each key resolves in this order: flag, environment, file, built-in default. `/models` lists and switches models. `/effort` changes reasoning effort. See `docs/SETUP.md` for configuration and sandbox settings.

## the dashboard

```sh
rig serve
```

`rig serve` is rig with the page as its terminal: the same loop, model, workers, stores, and commands as `rig` in a shell, on loopback only, token-gated (printed once, stored `0600`, exchanged for a cookie). The home view is the live session in the TUI's grammar, with `/models`, `/effort`, `/new`, `/sessions resume` and the rest typed into the same `❯` prompt; a sidebar on desktop, a tab bar on the phone; `warm` and `cool` palettes. From Safari, share → add to home screen installs it as an app.

- **chat**: the live session, streamed; approvals answered in place; stop while a turn runs
- **sessions**: list them per workspace, open a transcript, resume one into the chat
- **todo**: the queue, with create (one task per line), start, complete, and retry; the rows show the requires/blocks links and claims
- **scheduler**: the jobs, with create, pause, resume, remove, repair (shown on a row that carries a drift line), an in-place update form that opens with the job's current fields, and each job's run audit trail
- **swarm**: the drain workers, live, with start and stop
- **models**: the table, switch, and the effort dial
- **plugins**: approved, pending, disabled; the forge reads and saves a plugin's source into the pending zone

## docs

| doc                | what it is                                        |
|--------------------|---------------------------------------------------|
| `docs/DESIGN.md`   | architecture, the seams, turn semantics, extension guide |
| `docs/SETUP.md`    | build, configuration, verification               |
| `docs/USAGE.md`    | running a session; session and failure semantics |
| `docs/PLUGINS.md`  | the python plugins: the contract, the zones, creating and consuming |
| `docs/EMBED.md`    | rig as a Go module: the seams, the freeze, a worked example |
| `SECURITY.md`      | the trust model and how to report a vulnerability |
| `CONTRIBUTING.md`  | the process: spec first, tests before code, the freeze |

## layout

```
cmd/rig      composition root; wires every seam once; flags and env only
core            the seams, wire types, and the streaming-event vocabulary
loop            the concrete turn runtime (fault/cancel-aware)
evt             the event loop (SPEC_EVT): one consumer, many producers; the
                turn runtime's engine
kernel.go       the composition kernel
command/        the user commands (/compact, /models, /sessions, /effort, /theme, ...)
config/         the four-layer config resolution (flag > env > file > embedded)
models/         the per-model table (window, compaction numbers, role, effort)
policy/         ContextPolicy implementations: compact (per-model trigger),
                and the provider decorators: effort (the reasoning dial),
                empty (the empty-turn guard)
middleware/     ToolMiddleware: toolset (the live table), approve (the gate),
                paths (the ~ boundary), perm (deny by default + plugin
                provenance), guard (the bound, the round cap, the result cap)
provider/       Provider implementations (the openai-compatible SSE adapter)
plugins/        python plugin discovery (one file, one tool) and the plugin
                door (run/schema) and the ecosystem (list/create/delete/reload)
store/          the SQLite stores (state, todo, rem, scheduler), the sqlx
                transaction seam, the project scope identity (store/scope);
                -resume projects a session back from the state rows
tool/           Tool implementations: bash(1); file read/write/edit (read
                appends the file's git diff against HEAD on ask, the
                drift refusal carries the capped diff); view the image
                reader (a vision row only); todo the job queue; rem
                memory; scheduler
                background jobs; delegate the one-shot worker; python the
                persistent IPython kernel; web search and fetch; sessions
                the soak's vitals
frontend/       Frontend implementations: cli (the piped reference), tui (the
                terminal default), oneshot (-p worker), web (the serve
                dashboard)
specs/          the specs, written and agreed before the code (SPEC_CORE first)
docs/           DESIGN (architecture), SETUP (build/config), USAGE (running),
                PLUGINS (the python plugins), EMBED (rig as a module)
```

## extending

The structural test is simple: add one file and one registration line. The loop never names a concrete tool, provider, policy, frontend, or middleware. A Python plugin needs no Go. See `docs/DESIGN.md`, `docs/PLUGINS.md`, and `CONTRIBUTING.md` for the process.

## under the hood

`core/` and `loop/` use only the standard library. Stores use the pure-Go `modernc.org/sqlite` driver.
