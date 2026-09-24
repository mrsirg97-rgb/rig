# rig

A small operating system for agents. The kernel is a few hundred lines.

rig assembles context, streams the model, executes tool calls, returns results, and repeats. The TUI, piped CLI, headless worker, and dashboard share the same session, task, memory, and scheduler stores.

## measured

Each number names its mechanism.

- **99.1% cache hit over 2,925 turns.** The request prefix is byte-stable, so the provider's prefix cache reuses it. The ratio is `cache_read/prompt` from the usage table, the same arithmetic `sessions summary` uses (`TestSessionsSummaryCacheRatioFixture`). 297M of 299M prompt tokens came from cache.
- **208M prompt tokens on 2026-09-22.** The same query, that day alone.
- **7k byte-stable preamble.** The system prompt, the tool schemas, and the append-only transcript are a few thousand bytes, pinned by `TestWireToolsPrefixGolden` (a sha256 over the fleet's wire shape), `TestWireMarshalingIsDeterministic`, `TestWireMessagesAreAppendOnly`, and `TestSystemPromptIsByteStableAcrossBuilds`, so a stray timestamp cannot silently kill the cache.
- **723 lines for the swarm.** The supervisor board's non-test Go: claim, spawn, complete, verdict, reap, and the status throttle.
- **34,301 lines of Go, 53,626 lines of tests.** Core and loop are stdlib-only; the one store dependency is pure-Go SQLite.

## what's different

- **the loop is closed.** The turn runtime names no concrete tool, provider, policy, frontend, or middleware. One file plus one registration line extends it.
- **the wire is pinned.** The exact bytes sent to the model are golden-tested. The cache win is a measured property, not a claim.
- **the loop never retries.** A failed call executes once and the model is told. Results are capped with loud markers; denials are named refusals with reasons.
- **state belongs to the repo.** Tasks, memory, and schedules carry the project's identity, shared by worktrees. A session resumes from the store in one read-only transaction.
- **default deny at the boundary.** Allowlist, approval gate, pathguard, plugin provenance, worker jail. Narrowing is the operator's act.
- **spec first.** Every behavior is one sentence in specs/ with a test that holds it there. Core is frozen.

## the os

| OS concept | rig | where |
|---|---|---|
| kernel | `loop.Run(ctx, kernel)` over the typed seams; `loop.go` + `batch.go` are 417 lines, stdlib-only | `loop/`, `core/`, `kernel.go` |
| scheduler | the turn's batch: concurrent reads beside each other, bounded by the kernel's Parallel (8), effects in call order; background jobs on the operator's crontab, model fires jailed | `loop/batch.go`, `tool/scheduler`, `store/scheduler` |
| processes | sessions (TUI, piped, one-shot), delegates, drain workers; each worker owns a transcript, sandboxed and resumable | `frontend/`, `tool/delegate`, `swarm/` |
| IPC | the event stream (`TextDelta`, `ToolCallEvent`, `ToolResult`, `TurnEnd`) and tool calls through the middleware chain; the stores are the shared state across processes | `core/provider.go`, `evt/`, `store/` |
| filesystem | the workspace through read/write/edit/ls/find/grep, provenance-canonicalized; the stores are SQLite files under the rig home | `tool/file`, `tool/fs`, `store/` |
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
go install github.com/mrsirg97-rgb/rig/cmd/rig@latest
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
- **tools.** `bash`, `read`/`write`/`edit`, `ls`/`find`/`grep`, `python`,
  `web_search`, `web_fetch`, `diff`, `todo`, `rem`, `scheduler`, `delegate`,
  `sessions`, `plugin`/`plugins`. Results are capped, refusals are named.
- **the queue.** `todo` reads the project's tasks (worktrees share one
  board); `todo claim` takes the next unblocked task, `todo complete` lands
  it, `todo notes tN` lists a task's notes. Tasks link with `requires` and
  `blocks`.
- **memory.** `rem learn`/`recall`/`reflect`/`prune` at the project's scope;
  a repo and its worktrees share the same memories.
- **schedules.** `scheduler` puts a job on the crontab; a job is a one-shot
  `rig -p` in its own cwd, jailed by default.
- **the swarm.** `swarm 2` drains the queue: workers claim, run one-shot,
  and submit; reviewers accept or reject. `swarm stop` ends it;
  `swarm 2 budget=5` caps the spend.
- **resume.** `sessions` lists the vitals; `rig --resume <id>` replays a
  session from the state store in one read-only transaction.

## the tools

rig's default menu is 17 built-in tools: `view` joins the set only for a
model row whose `"vision": true` says it takes images, and `scheduler` and
`delegate` join when a worker fleet is configured. Restrict them with
`--allow`:

| tool | what it does |
|------|--------------|
| `bash` | run shell commands; output bounded |
| `read` / `write` / `edit` | files; edits are exact-match, provenance-checked |
| `ls` / `find` / `grep` | the filesystem, by name and by content |
| `view` | look at an image: downscaled, content-addressed, sent to a vision model (off unless your model row has `"vision": true`) |
| `diff` | the working tree against HEAD, or a tool's two latest observations |
| `python` | a persistent IPython kernel; variables and imports survive |
| `web_search` | a local SearXNG instance |
| `web_fetch` | a URL as readable text; private addresses refused |
| `todo` | the task queue, scoped to the project (a repo's worktrees share one); tasks link with `requires`/`blocks` |
| `rem` | memory across sessions: learn, recall, reflect, prune; scoped to the project |
| `scheduler` | background jobs on your crontab, run in a bubblewrap jail |
| `delegate` | a headless worker for a bounded subtask; several run in parallel in one turn, up to the fleet's slots |
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
 "apiKey": "sk-or-...", "concurrency": 4, "reasoning": "reasoning",
 "providerPin": "Together", "cacheControl": true}
```

The row's key rides `Authorization: Bearer <key>` (from the file or
`RIG_MODEL_API_KEY`, never logged); 429 and 5xx retry with bounded
backoff and jitter instead of faulting a turn; `usage.cost` lands in
the state store's cost column and shows in the TUI footer; OpenRouter
rows read and echo `reasoning` / `reasoning_details` while everything
else keeps `reasoning_content`; remote rows omit llama-server-only
fields. A remote row's delegate and scheduled fire skip the local swap
entirely — no busy probe — and bound parallelism by the row's
`concurrency` tokens beside the fleet's slots. `swarm <n> budget=5`
and a scheduled job's `budget` cap spend in dollars, summed from the
cost column (SPEC_HOSTED).

## subagents

`delegate` runs a bounded sub-task on a headless worker and waits for its
last message. In one turn you can fan out several delegates: they run in
parallel, the turn blocks until each finishes or times out, and
`workers.json`'s `slots` bounds how many run at once, with extras waiting
for a slot. A worker runs only when the model's GPU slot is free; a held
GPU refuses by name (`busy:skip`, never an eviction from inside a turn).
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
| `models.json` | the per-model table: context window, max tokens, the compaction reserve, the role (`worker`/`interactive`), the effort levels, `vision` (the model takes images, which unlocks `view`), and the hosted run site: `remote`/`provider` (where it runs), `baseUrl`, `apiKey`, `concurrency`, `reasoning`, `providerPin`, `cacheControl`, `retries` |
| `blobs/` | the images `view` has read, named by sha256; delete anything, and rig never rewrites a file it did not create |
| `workers.json` | the worker fleet: `{"model": "<id>", "slots": N}`. Unlocks `scheduler` and `delegate`; `slots` bounds concurrent delegates per session |
| `AGENTS.md` | global instructions, read before the project's `<cwd>/AGENTS.md` |
| `theme.json` | the terminal theme: base, slot colors, glyph set |
| `plugins/` | your python plugins (top-level files are live) |

Each key resolves in this order: flag, environment, file, built-in default. `/models` lists and switches models. `/effort` changes reasoning effort. See `docs/SETUP.md` for configuration and sandbox settings.

## the dashboard

```sh
rig serve
```

The dashboard serves the rig stores on loopback only. On first run it prints an access token, stores it with mode `0600`, and includes it in the URL. The page exchanges the token for a cookie. Mobile friendly.

- **sessions**: list them per workspace, and open a transcript mid-work
- **todo**: the queue, with create (one task per line), start, complete, and retry; the rows show the requires/blocks links and claims
- **scheduler**: the jobs, with create, pause, resume, remove, an in-place update form that opens with the job's current fields, and each job's run audit trail
- **models**: the table, with the effort dial
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
command/        the user commands (/compact, /models, /sessions, /effort, ...)
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
tool/           Tool implementations: bash(1); file read/write/edit; fs
                ls/find/grep; view the image reader (a vision row only);
                todo the job queue; rem memory; scheduler background jobs;
                delegate the one-shot worker; python the persistent IPython
                kernel; web search and fetch; diff the observation diff;
                sessions the soak's vitals
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
