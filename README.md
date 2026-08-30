# rig

A daily-driver coding agent. One binary. One model endpoint. One terminal.

rig assembles context, streams the model, executes tool calls, returns
results, and repeats. Every dependency sits behind a typed seam. The TUI,
piped CLI, headless worker, and dashboard share the same session, task,
memory, and scheduler stores.

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

**go install** (needs Go ≥ 1.26; the core is stdlib-only):

```sh
go install github.com/mrsirg97-rgb/rig/cmd/rig@latest
```

`rig -update` fetches, verifies, and atomically installs the latest release.
The running process keeps the old binary until restart.

## first run

```sh
./rig --base-url $ENDPOINT --model $NAME
```

rig needs an OpenAI-compatible SSE endpoint and a model ID. It defaults to
`http://127.0.0.1:8090/v1`, model `local`, and the TUI when stdout is a
terminal. Otherwise it uses the piped CLI. For scripts, run
`./rig -p "the task"`. See `docs/SETUP.md` for configuration.

## the tools

rig ships 18 built-in tools. Restrict them with `--allow`:

| tool | what it does |
|------|--------------|
| `bash` | run shell commands; output bounded |
| `read` / `write` / `edit` | files; edits are exact-match, provenance-checked |
| `ls` / `find` / `grep` | the filesystem, by name and by content |
| `diff` | the working tree against HEAD, or a tool's two latest observations |
| `python` | a persistent IPython kernel; variables and imports survive |
| `web_search` | a local SearXNG instance |
| `web_fetch` | a URL as readable text; private addresses refused |
| `todo` | the task queue, scoped to the project (a repo's worktrees share one) |
| `rem` | memory across sessions: learn, recall, reflect, prune; scoped to the project |
| `scheduler` | background jobs on your crontab, run in a bubblewrap jail |
| `delegate` | a one-shot headless worker for a bounded subtask |
| `sessions` | read-only vitals of the session store |
| `plugin` / `plugins` | the door into your python plugins, and their ecosystem |

Every tool result is capped. Repeated identical failures are bounded. An
optional round cap limits calls per turn. A failed call executes once.

## plugins

A Python plugin is one file and one tool. It provides `run` and `schema`.
There is no build step. Model-authored plugins land in
`~/.rig/plugins/pending/`. Approve, disable, and reload them with `/plugins`
or the dashboard. See `docs/PLUGINS.md`.

## configuration

Configuration lives in `~/.rig/`. Set `$RIG_HOME` to move it. Every file is
optional. Invalid files fail startup and name the file and field.

| file | what it holds |
|------|---------------|
| `settings.json` | the knobs: endpoint, model, the allow-list, the retry bound, the approval dial, the worker sandbox |
| `models.json` | the per-model table: context window, max tokens, the compaction reserve, the role (`worker`/`interactive`), the effort levels |
| `AGENTS.md` | global instructions, read before the project's `<cwd>/AGENTS.md` |
| `theme.json` | the terminal theme: base, slot colors, glyph set |
| `plugins/` | your python plugins (top-level files are live) |

Each key resolves in this order: flag, environment, file, built-in default.
`/models` lists and switches models. `/effort` changes reasoning effort. See
`docs/SETUP.md` for configuration and sandbox settings.

## the dashboard

```sh
rig serve
```

The dashboard serves the rig stores on loopback only. On first run it prints
an access token, stores it with mode `0600`, and includes it in the URL. The
page exchanges the token for a cookie.

- **sessions**: list them per workspace, and resume one mid-work
- **todo**: the queue, with create, start, complete, and retry
- **scheduler**: the jobs, with create, pause, resume, remove, an
  in-place update form that opens with the job's current fields, and
  each job's run audit trail
- **models**: the table, with the effort dial
- **plugins**: approved, pending, disabled; the forge reads and
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

The structural test is simple: add one file and one registration line. The
loop never names a concrete tool, provider, policy, frontend, or middleware.
A Python plugin needs no Go. See `docs/DESIGN.md` and `docs/PLUGINS.md`.

## under the hood

`core/` and `loop/` use only the standard library. Stores use the pure-Go
`modernc.org/sqlite` driver. Version `0.20.0` remains pre-1.0 while the worker
fleet and TUI continue daily-driver soak testing.
