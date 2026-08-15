# looper

A minimal agent loop machine. It executes the agent loop faithfully — assemble
context, stream the model's output, execute what it asks for, feed results
back, repeat — with every dependency held at a typed seam and every induced
work bounded. Not a framework: a machine for one thing, built closed.

**Zero third-party dependencies.** `go.mod` carries no require lines; Go
standard library only.

Status: initial release, version `0.1.0`.

## quickstart

```sh
go build ./cmd/looper
./looper --base-url $ENDPOINT --model $NAME
```

then talk. Configuration and verification: `docs/SETUP.md`.

## docs

| doc                | what it is                                        |
|--------------------|---------------------------------------------------|
| `docs/DESIGN.md`   | architecture, the seams, turn semantics, extension guide |
| `docs/SETUP.md`    | build, configuration, verification               |
| `docs/USAGE.md`    | running a session; session and failure semantics |

## layout

```
cmd/looper      composition root — wires every seam once; flags and env only
core            the seams, wire types, and the streaming-event vocabulary
loop            the concrete turn runtime (fault/cancel-aware)
kernel.go       the composition kernel
policy/         ContextPolicy implementations (passthrough)
middleware/     ToolMiddleware: perm (deny by default), guard.Bound (the bound)
provider/       Provider implementations (the openai-compatible SSE adapter)
tool/           Tool implementations: bash(1); file read/write/edit; fs ls/find/grep
frontend/       Frontend implementations (the cli REPL)
```

## extending

The design test, enforced structurally: adding a tool, a provider, a context
policy, a frontend, or a tool middleware is **one file plus one registration
line** at the composition root — the loop never names a concrete type. How:
`docs/DESIGN.md`.
