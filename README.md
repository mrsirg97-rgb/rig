# rig

A coding agent you can use every day: one binary, a model endpoint, a
terminal.

rig runs the agent loop faithfully — assemble context, stream the
model's output, execute what it asks for, feed the results back,
repeat — with every dependency at a typed seam. It is a terminal UI by
default, a piped CLI when stdout is not a terminal, and a headless
worker for scheduled jobs. And what it does is on disk: the session
store, the todo queue, memory, and the scheduler's crontab, read the
same way by the TUI, the CLI, and the dashboard.

## install

Three paths, pick one:

**Installer** (POSIX sh, no Go, no sudo — lands in `~/.local/bin`):

```sh
curl -fsSL https://mrsirg97-rgb.github.io/rig/install.sh | sh
```

**Release binary** — the same asset the installer fetches, straight
from `releases/latest`; pick your `<os>_<arch>` (the version lives in
the URL, not the name):

```sh
curl -fsSL https://github.com/mrsirg97-rgb/rig/releases/latest/download/rig_linux_amd64 -o rig
chmod +x rig
```

**go install** (needs Go ≥ 1.26; the core is stdlib-only):

```sh
go install github.com/mrsirg97-rgb/rig/cmd/rig@latest
```

A built release updates itself in place: `rig -update` fetches,
verifies, and atomically renames the latest release over the running
binary (a running rig keeps the old file until restarted).

## first run

```sh
./rig --base-url $ENDPOINT --model $NAME
```

An OpenAI-compatible SSE endpoint and a model id are the minimum.
Defaults: endpoint `http://127.0.0.1:8090/v1`, model `local`, the
terminal UI when stdout is a terminal, the piped CLI otherwise. For
scripts, one-shot: `./rig -p "the task"`. Configure, and verify:
`docs/SETUP.md`.

## the tools

18 built-in tools ship on, and the allow-list names every one —
narrow it with `--allow`:

| tool | what it does |
|------|--------------|
| `bash` | run shell commands; output bounded |
| `read` / `write` / `edit` | files; edits are exact-match, provenance-checked |
| `ls` / `find` / `grep` | the filesystem, by name and by content |
| `diff` | the working tree against HEAD, or a tool's two latest observations |
| `python` | a persistent IPython kernel — variables and imports survive |
| `web_search` | a local SearXNG instance |
| `web_fetch` | a URL as readable text; private addresses refused |
| `todo` | the task queue, scoped to the project (a repo's worktrees share one) |
| `rem` | memory across sessions: learn, recall, reflect, prune — scoped to the project |
| `scheduler` | background jobs on your crontab, run in a bubblewrap jail |
| `delegate` | a one-shot headless worker for a bounded subtask |
| `sessions` | read-only vitals of the session store |
| `plugin` / `plugins` | the door into your python plugins, and their ecosystem |

The work the model can induce is bounded: a result cap on every tool
output, a retry bound on identical re-issues, an optional round cap —
and a failing call executes exactly once, never silently re-run.

## plugins

A python plugin is one file under `~/.rig/plugins/` and one tool: a
`run`, a `schema`, no build step. The model can draft plugins into
`~/.rig/plugins/pending/` (the provenance rule keeps its writes
there), and the `/plugins` command — or the dashboard's forge —
approves, disables, and reloads them. The contract and worked
examples: `docs/PLUGINS.md`.

## configuration

Everything lives in the rig home, `~/.rig/` (override with
`$RIG_HOME`). Every file is optional; a present-but-bad one is a loud
refusal at start, naming the file and the field.

| file | what it holds |
|------|---------------|
| `settings.json` | the knobs: endpoint, model, the allow-list, the retry bound, the approval dial, the worker sandbox |
| `models.json` | the per-model table: context window, max tokens, the compaction reserve, the role (`worker`/`interactive`), the effort levels |
| `AGENTS.md` | global instructions, read before the project's `<cwd>/AGENTS.md` |
| `theme.json` | the terminal theme: base, slot colors, glyph set |
| `plugins/` | your python plugins (top-level files are live) |

Every knob resolves per key, flag > env > file > built-in default:
what you typed on the line wins, and an unset key descends. `/models`
lists the runtime table and switches the active model; `/effort` dials
the reasoning effort the model uses. The full table, the presence
keys, and the sandbox: `docs/SETUP.md`.

## the dashboard

```sh
rig serve
```

A loopback-only server on the rig home's stores — the same files a
live session is writing, read through the same store verbs. It
prints the address and, on a first mint, a token (kept in the home,
`0600`, printed once); open the printed link with the token and the
page sets its own cookie.

- **sessions** — list them per workspace, and resume one mid-work
- **todo** — the queue, with create, start, complete, and retry
- **scheduler** — the jobs, with create, pause, resume, remove, an
  in-place update form that opens with the job's current fields, and
  each job's run audit trail
- **models** — the table, with the effort dial
- **plugins** — approved, pending, disabled; the forge reads and
  saves a plugin's source into the pending zone

Every write is attributed to `dashboard` and rides the store verb the
matching tool calls; the reply is the store's voice, verbatim. Below
720px the view is phone-first: the job row's controls stay a
horizontal row of 44px tap targets, wrapping when the width forces
it. Spec: `specs/SPEC_SERVE.md`.

## docs

| doc                | what it is                                        |
|--------------------|---------------------------------------------------|
| `docs/DESIGN.md`   | architecture, the seams, turn semantics, extension guide |
| `docs/SETUP.md`    | build, configuration, verification               |
| `docs/USAGE.md`    | running a session; session and failure semantics |
| `docs/PLUGINS.md`  | the python plugins: the contract, the zones, creating and consuming |

## layout

```
cmd/rig      composition root — wires every seam once; flags and env only
core            the seams, wire types, and the streaming-event vocabulary
loop            the concrete turn runtime (fault/cancel-aware)
evt             the event loop (SPEC_EVT): one consumer, many producers; the
                turn runtime's engine
kernel.go       the composition kernel
command/        the user commands (/compact, /models, /sessions, /effort, ...)
config/         the four-layer config resolution (flag > env > file > embedded)
models/         the per-model table (window, compaction numbers, role, effort)
policy/         ContextPolicy implementations: compact (per-model trigger),
                effort (the reasoning dial's provider decorator)
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
                ls/find/grep; todo the job queue; rem memory; scheduler
                background jobs; delegate the one-shot worker; python the
                persistent IPython kernel; web search and fetch; diff the
                observation diff; sessions the soak's vitals
frontend/       Frontend implementations: cli (the piped reference), tui (the
                terminal default), oneshot (-p worker), web (the serve
                dashboard)
specs/          the specs, written and agreed before the code (SPEC_CORE first)
docs/           DESIGN (architecture), SETUP (build/config), USAGE (running),
                PLUGINS (the python plugins)
```

## extending

The design test, enforced structurally: adding a tool, a provider, a
context policy, a frontend, or a tool middleware is **one file plus
one registration line** at the composition root — the loop never names
a concrete type. Or no Go at all: a python plugin is one file under
the rig home's `plugins/` and one tool (`docs/PLUGINS.md`). How:
`docs/DESIGN.md`.

## under the hood

`core/` and `loop/` are stdlib-only; the one leaf dependency is
`modernc.org/sqlite` (pure-Go driver) for the stores, justified in
`specs/SPEC_STATE.md`. Status: feature-complete runtime, version
`0.18.0`; the freeze discipline holds on `core/` and `loop/`, and the
1.0 tag waits for lived use (a worker soak, the TUI field-tested as
the daily driver).
