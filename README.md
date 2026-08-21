# rig

A minimal agent loop machine. It executes the agent loop faithfully — assemble
context, stream the model's output, execute what it asks for, feed results
back, repeat — with every dependency held at a typed seam and every induced
work bounded. Not a framework: a machine for one thing, built closed.

**Stdlib-only core.** `core/` and `loop/` carry no dependencies; the one
leaf dependency is `modernc.org/sqlite` (pure-Go driver) for the stores —
justified in `specs/SPEC_STATE.md`.

Status: feature-complete runtime, version `0.8.0`. The freeze discipline
holds on `core/` and `loop/`; the 1.0 tag waits for lived use (a worker
soak, the TUI field-tested as the daily driver).

## quickstart

```sh
go build ./cmd/rig
./rig --base-url $ENDPOINT --model $NAME
```

then talk. Configuration and verification: `docs/SETUP.md`.

## install

Three paths (`specs/SPEC_BUILD.md` 5); pick one:

**Installer** (POSIX sh, no Go, no sudo — lands in `~/.local/bin`):

```sh
curl -fsSL https://mrsirg97-rgb.github.io/rig/install.sh | sh
```

**Release binary** — the same asset the installer fetches, downloaded
directly (no script). Pick `rig_<os>_<arch>` from
`releases/latest`; the version lives in the URL, not the name:

```sh
curl -fsSL https://github.com/mrsirg97-rgb/rig/releases/latest/download/rig_linux_amd64 -o rig
chmod +x rig
./rig --version
```

**go install** (needs Go ≥ 1.26; pre-tag `@master` works today, `@latest`
once tagged):

```sh
go install github.com/mrsirg97-rgb/rig/cmd/rig@latest
```

## docs

| doc                | what it is                                        |
|--------------------|---------------------------------------------------|
| `docs/DESIGN.md`   | architecture, the seams, turn semantics, extension guide |
| `docs/SETUP.md`    | build, configuration, verification               |
| `docs/USAGE.md`    | running a session; session and failure semantics |

## layout

```
cmd/rig      composition root — wires every seam once; flags and env only
core            the seams, wire types, and the streaming-event vocabulary
loop            the concrete turn runtime (fault/cancel-aware)
kernel.go       the composition kernel
command/        the user commands (/compact, /models, /sessions, /effort, ...)
config/         the four-layer config resolution (flag > env > file > embedded)
models/         the per-model table (window, compaction numbers, role, effort)
policy/         ContextPolicy implementations: compact (per-model trigger),
                effort (the reasoning dial's provider decorator)
middleware/     ToolMiddleware: toolset (the live table), approve (the gate),
                perm (deny by default + plugin provenance), guard.Bound (the bound)
provider/       Provider implementations (the openai-compatible SSE adapter)
plugins/        python plugin discovery (one file, one tool)
store/          the SQLite stores (state, todo, rem, scheduler) and the state
                recorder; -resume projects a session back from the state rows
tool/           Tool implementations: bash(1); file read/write/edit; fs ls/find/grep;
                todo the job queue; rem memory; scheduler background jobs; python the
                persistent IPython kernel; web search and fetch; diff the observation
                diff; plugins_reload the live reload
frontend/       Frontend implementations: cli (the piped reference), tui (the
                terminal default), oneshot (-p worker)
specs/          the specs, written and agreed before the code (SPEC_CORE first)
docs/           DESIGN (architecture), SETUP (build/config), USAGE (running)
```

## extending

The design test, enforced structurally: adding a tool, a provider, a context
policy, a frontend, or a tool middleware is **one file plus one registration
line** at the composition root — the loop never names a concrete type. How:
`docs/DESIGN.md`.
