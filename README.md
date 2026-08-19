# rig

A minimal agent loop machine. It executes the agent loop faithfully — assemble
context, stream the model's output, execute what it asks for, feed results
back, repeat — with every dependency held at a typed seam and every induced
work bounded. Not a framework: a machine for one thing, built closed.

**Stdlib-only core.** `core/` and `loop/` carry no dependencies; the one
leaf dependency is `modernc.org/sqlite` (pure-Go driver) for the stores —
justified in `specs/SPEC_STATE.md`.

Status: feature-complete runtime, version `0.3.0`. The freeze discipline
holds on `core/` and `loop/`; the 1.0 tag waits for lived use (a worker
soak, the TUI field-tested as the daily driver).

## quickstart

```sh
go build ./cmd/rig
./rig --base-url $ENDPOINT --model $NAME
```

then talk. Configuration and verification: `docs/SETUP.md`.

Once tagged and public, the install path is the binary, not a script
(`specs/SPEC_BUILD.md`):

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
policy/         ContextPolicy implementations (passthrough)
middleware/     ToolMiddleware: perm (deny by default), guard.Bound (the bound)
provider/       Provider implementations (the openai-compatible SSE adapter)
store/          the SQLite stores (state, todo, rem, scheduler) and the state
                recorder; -resume projects a session back from the state rows
tool/           Tool implementations: bash(1); file read/write/edit; fs ls/find/grep;
                todo the job queue; rem memory; scheduler background jobs; python the
                persistent IPython kernel; web search and fetch (the web-tools compose)
frontend/       Frontend implementations (the cli REPL, the oneshot -p worker)
specs/          the specs, written and agreed before the code (SPEC_CORE first)
```

## extending

The design test, enforced structurally: adding a tool, a provider, a context
policy, a frontend, or a tool middleware is **one file plus one registration
line** at the composition root — the loop never names a concrete type. How:
`docs/DESIGN.md`.
