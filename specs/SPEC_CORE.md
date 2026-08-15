# looper: minimum agent runtime

A single-binary agent harness in Go. One sequential turn loop, a small set of
interfaces around it, everything else registered on top. The loop is the spec
of ordering; plugins observe and intercept, they never drive.

Design test (applies to every decision below): adding a new tool, a new
provider, a new context policy, or a new frontend is one file and one
registration line. If it touches the loop, the interfaces are wrong.

## goals

- Tiniest loop that runs a real coding agent: model call, tool execution,
  repeat until a plain text turn.
- Plugin seams for providers, tools, middleware, context policy, frontends.
- CLI first. TUI is a later `Frontend` registration, not a rewrite.
- Standard library only in the core. Zero third-party deps in `core/` and the
  loop. A provider adapter may not import anything beyond `net/http` and
  `encoding/json` either; the OpenAI-compatible wire format is plain JSON.

## non-goals (v1)

- No TUI, no scrollback, no rendering beyond stdout.
- No compaction. `ContextPolicy` exists; v1 ships the passthrough.
- No subagents, no queues, no background tasks.
- No config files. Flags and env only (`flag`, `os`).
- No registry with runtime lookup. Slices passed to a constructor. Wiring is
  explicit at the root and happens once.

## layout

```
looper/
  core/          interfaces + wire types, stdlib only
    message.go
    provider.go
    tool.go
    middleware.go
    policy.go
    frontend.go
    session.go
  loop/          the concrete turn loop
    loop.go
  provider/
    openai/      OpenAI-compatible chat completions over net/http (llama.cpp)
  tool/
    bash/
    file/        read, write, edit
    fs/          ls, find, grep
  frontend/
    cli/         stdin/stdout REPL
  cmd/
    looper/      main.go, the composition root
```

One concept per file, interfaces in `core`, concrete types unexported with
`NewX` constructors, mirroring lift/engine.

## wire types

The kernel owns the message shape. Providers adapt to and from it; SDK or
wire types never cross into `core` or the loop.

```go
package core

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role      Role
	Content   string
	ToolCalls []ToolCall // assistant turns only
	ToolID    string     // tool result turns only
}

type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}
```

## streaming events

Go has no sum types. A sealed interface with a marker method is the least-bad
encoding and keeps the switch in the loop exhaustive by convention.

```go
type Event interface{ event() }

type TextDelta struct{ Text string }
type ToolCallEvent struct{ Call ToolCall }
type Done struct {
	StopReason string
	Usage      Usage
}
type Fault struct{ Err error }
```

`Fault` terminates the stream. A provider closes the channel after `Done` or
`Fault`; the loop treats a closed channel without either as a provider bug and
fails loudly.

## interfaces

Five seams. Each is swappable without touching the loop.

### Provider

```go
type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

type Request struct {
	Messages []Message
	Tools    []ToolSpec // name, description, schema; execution stays in the kernel
}
```

One method. Cancellation is `ctx`; a cancelled context tears down the HTTP
stream. Per-model tool-call formats are the adapter's problem, not the loop's.

### Tool

```go
type Tool interface {
	Name() string
	Description() string // model-facing, terse; reaches the wire as function.description
	Schema() json.RawMessage // hand-written JSON Schema, no reflection
	Exec(ctx context.Context, args json.RawMessage) (string, error)
}
```

Schemas are authored by hand next to the tool. Reflection-derived schemas
drift from intent and add deps. Edit is exact-match string replacement with
loud, specific failure messages; a fuzzy edit tool silently corrupts files.

### ToolMiddleware

```go
type ToolExec func(ctx context.Context, call ToolCall) (string, error)
type ToolMiddleware func(next ToolExec) ToolExec
```

The http.Handler shape. Permissions, retry guards, timeouts, and logging are
all this one seam, composed in order at the root. Deny-by-default permission
middleware ships in v1 with a static allowlist; a denied call returns a
refusal string to the model, not an error to the user.

### ContextPolicy

```go
type ContextPolicy interface {
	Assemble(ctx context.Context, s *Session) ([]Message, error)
}
```

Prompt assembly and, later, compaction. This is the load-bearing seam for
local models; it is an interface from day one so the passthrough can be
replaced without touching anything. v1 passthrough: system prompt plus
transcript, verbatim.

### Frontend

```go
type Frontend interface {
	Input(ctx context.Context) (string, error)
	Notify(ev Event)
}
```

Blocking pull for input, fire-and-forget observation of the turn stream. The
CLI implements it over stdin/stdout. The TUI, a test driver, or a
programmatic caller implement the same two methods later.

## the loop

The loop is a concrete function, not an interface. It is the one place turn
ordering is written down; making it pluggable would make ordering emergent
and undebuggable.

```go
func Run(ctx context.Context, k *Kernel) error
```

Per turn:

1. `frontend.Input(ctx)` blocks for the user message; append to session.
2. `policy.Assemble(ctx, session)` produces the request messages.
3. `provider.Stream(ctx, req)`; forward every event to `frontend.Notify`.
4. Accumulate deltas and tool calls until `Done`.
5. No tool calls: append the assistant message, turn over, goto 1.
6. Tool calls: execute each through the middleware chain sequentially and in
   order, append one tool message per call, goto 2.

State machine per turn: `awaiting_input -> awaiting_model -> executing_tools
-> awaiting_model -> ... -> done`. Tool execution is sequential in v1;
parallel execution is a loop change and is deliberately out.

Faults: a `Fault` event or transport error aborts the turn, surfaces the
error through `Notify`, preserves the session up to the last complete
message, and returns to `awaiting_input`. A malformed tool call (bad JSON,
unknown name) is fed back to the model as a tool error result, once; the
middleware retry guard bounds repetition. The loop never retries silently.

Cancellation: `ctx` threads through every await. Interrupt support later is
`cancel()` plus re-entering `awaiting_input`; no mailbox until something
needs one.

## kernel and wiring

```go
k := looper.New(
	looper.WithProvider(openai.New(baseURL, model)),
	looper.WithTools(bash.New(), file.Read(), file.Write(), file.Edit()),
	looper.WithMiddleware(perm.Allowlist(rules), guard.Retry(3)),
	looper.WithPolicy(policy.Passthrough(systemPrompt)),
	looper.WithFrontend(cli.New(os.Stdin, os.Stdout)),
)
err := loop.Run(ctx, k)
```

Functional options over a plain struct. Every dependency is explicit in the
constructor call; swapping happens at registration with zero consumer
changes. An extension is any function that contributes options; there is no
extension type, lifecycle, or discovery mechanism.

## session

```go
type Session struct {
	ID       string // minted at NewSession: ULID-style time-ordered; persistence attributes to it
	Messages []Message
	Files    map[string]FileState // path -> hash + mtime at last read
}
```

In-memory for v1, but JSON-serializable from day one (`encoding/json`, a
file under `os.UserConfigDir()`). `Files` exists so edit-after-external-change
fails loudly instead of clobbering; the file tools maintain it.

## dependencies

`core/`, `loop/`: standard library only, enforced by review. Everything
needed exists there: `net/http` (streaming via `bufio.Scanner` over SSE),
`encoding/json`, `os/exec`, `context`, `flag`. The go.mod for v1 has zero
requires. Any future dep is added to a leaf package, never to core, and is
justified in this file first.

## testing

- Loop: fake Provider (scripted event channels) and fake Frontend drive full
  turns; assert message ordering, fault paths, and cancellation mid-stream.
  The fakes live at the DI seam, nowhere else.
- Tools: real filesystem in `t.TempDir()`, real subprocesses for bash. No
  mocked syscalls.
- Middleware: table tests on the chain; the retry guard test proves the
  bounded-repetition invariant by exhausting it.
- Provider adapter: `httptest.Server` replaying recorded llama.cpp SSE
  bodies; malformed and truncated streams are named cases.
- Boundary cases by name: empty transcript, zero tools, tool result at the
  context edge, cancellation between tool calls.

## v1 scope

One provider (OpenAI-compatible), four tools (bash, read, write, edit),
allowlist + retry middleware, passthrough policy, CLI frontend, in-memory
session. Roughly 600 lines plus tests. Everything after v1 fills an
interface that already exists.
