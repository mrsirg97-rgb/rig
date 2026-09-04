# rig: minimum agent runtime

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
- No compaction. `ContextPolicy` exists: v1 ships the passthrough.
- No subagents, no queues, no background tasks.
- No config files. Flags and env only (`flag`, `os`).
- No registry with runtime lookup. Slices passed to a constructor. Wiring is
  explicit at the root and happens once.

## layout

```
rig/
  core/          interfaces + wire types, stdlib only
    message.go
    provider.go
    tool.go
    middleware.go
    policy.go
    frontend.go
    session.go
    interrupt.go // WithInterrupt / InterruptFrom: the turn's cancel rides the ctx
    command.go   // Command: the user-command seam (deliverable 9, SPEC_COMMANDS)
  loop/          the concrete turn loop
    loop.go
  provider/
    openai/      OpenAI-compatible chat completions over net/http (llama.cpp)
  tool/
    bash/
    file/        read, write, edit
    fs/          ls, find, grep
    todo/        the concurrent job queue
    rem/         memory
    scheduler/   background jobs
    python/      the persistent IPython kernel
    web/         web_search, web_fetch
  frontend/
    cli/         stdin/stdout REPL
  command/       the user-command leaf (deliverable 9, SPEC_COMMANDS): the
                 prefix rule, the Env the root builds, one file per command
  config/        the config leaf (SPEC_CONFIG): one load for every entry
                 mode; the embedded settings.json and models.json are the
                 0.2.0 defaults moved out of code
  cmd/
    rig/      main.go, the composition root
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
	Reasoning string     // assistant turns only; the model's thinking, round-tripped
	ToolCalls []ToolCall // assistant turns only
	ToolID    string     // tool result turns only

	ContextTokens int // assistant turns only; the server-reported
			// prompt+completion at the moment this message completed (L8);
			// 0 when unreported. Never rides the wire.
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
type ReasoningDelta struct{ Text string } // the model's thinking (reasoning_content)
type ToolCallEvent struct{ Call ToolCall }
type Done struct {
	StopReason string
	Usage      Usage
}
type Fault struct{ Err error }

type TurnReason string

const (
	TurnOver      TurnReason = "over"      // the turn completed
	TurnFault     TurnReason = "fault"     // a Fault or transport error crossed it
	TurnInterrupt TurnReason = "interrupt" // the turn context died (steering)
)

// loop events (the loop emits them around execution; SPEC_HARDENING L4, L7):
type ToolStart struct{ Call ToolCall }
type ToolResult struct {
	ID       string
	Content  string
	Err      error // nil = success; the guarded result, named
	Duration time.Duration
}

// TestEvent is a documented test seam: the compat rule's subject (below).
type TestEvent struct{ Name string }

// policy event (SPEC_COMPACT 5): emitted at Assemble time or on-stream by
// the policy's overflow decorator; the loop forwards it in its existing
// default.
type Compacted struct {
	Summary string
	Dropped int
	Kept    int
	Usage   Usage
}

type TurnEnd struct{ Reason TurnReason } // closes every turn inside the run

type Usage struct {
	Prompt     int
	Completion int
	CacheRead  int // tokens served from the server-side prompt cache (a subset of Prompt on this wire)
	CacheWrite int // zero until the transport reports one
}
```

Provider-stream events (`TextDelta`, `ReasoningDelta`, `ToolCallEvent`,
`Done`, `Fault`) are emitted by the adapter in stream order. Loop events
(`ToolStart`, `ToolResult`, `TurnEnd`) bracket execution and close the
turn; they are emitted by the loop. Policy events (`Compacted`,
SPEC_COMPACT) are emitted at `Assemble` or on-stream by the policy's
decorator; the third emitter category; the loop forwards them in its
existing default, and the recorder lands them (SPEC_STATE). `TurnEnd` fires at every turn exit
(`over` / `fault` / `interrupt`), after the turn's last other event; a
run-context cancel ends the run, not a turn, and does not emit it. The
recorder's rule (SPEC_HARDENING 4): an unlanded partial at any `TurnEnd`
is a partial and is discarded.

`Fault` terminates the stream. A provider closes the channel after `Done` or
`Fault`; the loop treats a closed channel without either as a provider bug and
fails loudly (a cancelled turn reads the same and breaks the turn instead, per
the loop section).

A `ToolCallEvent` is emitted only for a call whose accumulated args are valid
JSON (empty args stay legal: a no-arg call). A stream cut off by length can
cut the args mid-JSON; executing or re-sending the half of a call would poison
the transcript, so the adapter faults with the truncation named instead of
emitting the partial call (provider/openai enforces this; the named test is
`TestLengthFinishedTruncatedToolCallArgsFault`).

**The compat rule (additive).** Events are added, never changed. A Frontend
must tolerate an `Event` it does not recognize; the default is to ignore it.
The loop forwards the kinds it names and ignores the rest. This is what lets
1.0 freeze: a Frontend written against 1.0 stays correct when 1.x adds
`ToolStart` or `ReasoningDelta` (noise to the old Frontend, never a misread),
and an adapter may emit a kind the loop does not yet name without breaking the
stream.

## interfaces

Five seams. Each is swappable without touching the loop.

### Provider

```go
type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

type Request struct {
	Messages  []Message
	Tools     []ToolSpec // name, description, schema; execution stays in the kernel
	MaxTokens int        // 0 = the provider's default (additive; a provider that
				// does not know it ignores it; SPEC_COMPACT 8's request-side reserve)
	ReasoningEffort string // the reasoning budget asked for; empty = the provider's
				// default (additive; SPEC_COMPACT 3's summary effort, "medium")
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
drift from intent and add deps.

**The description's shape** (amended 0.12.2): every description is the
same four parts, in order; *what* it does (one sentence); *when* to
reach for it and when not (a `Guidelines:` sentence, on every tool; it
is the sentence that changes behavior, and it was missing from 13 of
18); the *reply's* shape (one clause); at most *one gotcha*. Storage
mechanics and implementation notes live in the package's `PACKAGE.md`,
never on the wire: the model needs the reply contract, not the event
log's compaction rule. Refusals and descriptions share vocabulary (the
pending zone, approve, the door), so the model meets each word in both
places. The whole menu; every native's description plus schema; is
pinned under 14,000 characters by a case in `cmd/rig` over the wire
golden (13.1k at the amendment: 5.5k of description, 7.5k of schema),
so growth is a decision; the schemas of `rem`, `scheduler`, `todo`, and
`diff` are 4k of that and the next lever, named. No description carries
the voice of another harness ("pi", "pane"), pinned by the same case. The
one-line tools (`bash`, `write`, `edit`, `ls`) keep their line and gain
the shape's clauses without padding.

**An empty reply names its scope** (amended 2026-08-23): "(no matches)"
from a `grep` whose root defaulted to a subdirectory read as "does not
exist", and a model spent four turns doubting a file it had just read.
Every empty reply says what it searched and where: `ls` names the
directory (`(empty: /abs/dir)`), `find` and `grep` the pattern and the
root (`(no matches for /re/ under /abs/root, glob 'g')`), `web_search`
the query, `rem` recall the scopes and the query (`(no memories in rig,
nor global for 'q')`), `todo` the queue it read (`(no tasks in
<label>'s queue)`; a subdirectory, a second worktree, and a `project`
override all name the queue they read, never "this directory's"),
`/plugins` the directory. The schemas say whose `.` a default root is
("the working directory"). The rule generalises: a reply that could be
read two ways carries the word that picks one. Edit is exact-match string replacement with
loud, specific failure messages; a fuzzy edit tool silently corrupts files.

**A read that finds a stale observation names it** (amended for the
daily driver): when the path was recorded in the threaded session's
`Files` and the on-disk hash or mtime differs, the read prepends
`[changed since your observation]` to the content before the bytes, so
the model is told its prior read is stale before it acts on it; the
push that `diff last` would otherwise force the model to go ask for.
The note rides the content it already returns; nothing new is written,
and a fresh observation re-records as usual.

**An edit against a path with no recorded observation refuses** (amended
for the daily driver): the threaded session's `Files` is the edit's
license. `read` or `write` mints it, an external or cross-session change
invalidates it, and a standalone exec (no session) carries no license to
check. The rule is at the tool, so grep-first stays a guideline while an
unlicensed edit is a refusal that names the missing observation.

### ToolMiddleware

```go
type ToolExec func(ctx context.Context, call ToolCall) (string, error)

type ToolMiddleware interface {
	Wrap(next ToolExec) ToolExec
}

// ToolMiddlewareFunc adapts a plain function to the seam (http.HandlerFunc
// shape), for participants that wrap only.
type ToolMiddlewareFunc func(next ToolExec) ToolExec

// Optional capabilities, checked by assertion (SPEC_HARDENING 6):
type TurnObserver interface{ TurnStart(ctx context.Context, s *Session) }
type GuidelineContributor interface{ Guidelines() string }
```

The http.Handler shape, widened (deliverable 7): the one agent-side
extension seam, not a second one. Permissions, retry guards, timeouts, and
logging all wrap here, composed in order at the root. A participant may
additionally observe the turn boundary (`TurnObserver`; the loop fans out
`TurnStart` at every turn start) and contribute system-prompt prose
(`GuidelineContributor`; the root collects it into the system prompt). The
retry guard is the first participant beyond a plain wrap: it wraps to bound
and refuse, and observes the turn boundary to clear its per-turn budget
(bound keyed by tool name, the streak per args, cleared per turn, pane's
semantics).

Deny-by-default permission middleware ships in v1 with a static allowlist; a
denied call returns a refusal string to the model, not an error to the user.

### ContextPolicy

```go
type ContextPolicy interface {
	Assemble(ctx context.Context, s *Session) ([]Message, error)
}
```

Prompt assembly and, later, compaction. This is the load-bearing seam for
local models; it is an interface from day one so the passthrough can be
replaced without touching anything. v1 passthrough: system prompt plus
transcript, verbatim. Deliverable 8 (SPEC_COMPACT) lands the first
non-passthrough policy: at its trigger the policy rewrites the session
transcript (the one named mutation the seam carries; the passthrough
stays pure), and the loop is untouched but for L8 (the loop section,
above).

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

**Steering (deliverable 7).** The loop threads the turn's cancel onto the
ctx it passes to `Input` (`core.WithInterrupt`; `core.InterruptFrom`
recovers it, the `WithSession` pattern). A Frontend holds that CancelFunc
as the interrupt handle: cancelling it interrupts the running turn (the
loop breaks the turn and re-enters `awaiting_input`). A Frontend may hold
one queued message (a slot, latest wins, not a mailbox) and must return it
before blocking; it is delivered on the next `Input`, whether the previous
turn ended naturally or was interrupted. Turn boundaries are explicit:
the loop emits `TurnEnd{Reason}` at every turn exit. `Notify` events are
additive: a Frontend ignores any `Event` it does not recognize (the
default), per the compat rule.

**User commands (deliverable 9, SPEC_COMMANDS).** A Frontend may dispatch
commands: a line that starts with `/` is checked against the registered
set before `Input` returns it to the loop, and a dispatch consumes the
line; the loop never sees a command. `//` escapes the prefix (one slash
consumed, the rest is a prompt), and a `/` line that is not a known
command is a loud refusal naming the known set, never a silent prompt.
Dispatching is optional: a Frontend without it (one-shot, the test
drivers) never dispatches, its `/` lines stay prompts, and nothing is
hijacked from it. The command's output is the Frontend's stdout; no new
`Event` is needed for any command. The loop is untouched; dispatch is
the Frontend's business, exactly as the steering slot is.

## the loop

The loop is a concrete function, not an interface. It is the one place turn
ordering is written down; making it pluggable would make ordering emergent
and undebuggable. Since SPEC_EVT 2b it is the `evt` engine's consumer:
each step below is a closure executed on the loop goroutine, and what
waits on the world; the Frontend's `Input`, the provider's stream, a
tool's execution; is a producer goroutine that posts. One goroutine
touches the session; the steps and their order are unchanged.

```go
func Run(ctx context.Context, k *Kernel) error
```

Per turn (deliverable 7 adds the named changes; SPEC_HARDENING has the
argument):

1. `frontend.Input(ctx)` is asked once, between turns, by a producer; the
   line is posted and appended to the session on the loop goroutine. A
   queued message (steering) returns here before a fresh read.
2. The loop calls `TurnStart` on every middleware that observes it (the
   guard's per-turn budget resets).
3. `policy.Assemble(ctx, session)` produces the request messages.
4. `provider.Stream(ctx, req)`; a producer drains the channel and posts
   each event in order; the loop forwards every event to
   `frontend.Notify`, including `ReasoningDelta` (accumulated into the
   assistant message).
5. Accumulate deltas, reasoning, and tool calls until `Done`.
6. No tool calls: append the assistant message, turn over, goto 1.
7. Tool calls: the batch (SPEC_EVT 2a). A call the kernel's
   `Concurrent` predicate admits runs beside its admitted neighbours
   (a run, bounded by `Parallel`); any other call is a barrier that
   waits for everything before it and runs alone. Emission is in call
   order regardless of completion order: for each call, emit
   `ToolStart` as the cursor reaches it, emit `ToolResult` (its own
   duration) and append one tool message when its completion has been
   posted; then goto 3. Every call runs in a goroutine; the loop
   goroutine never blocks on a tool. A nil predicate is the sequential
   order, byte-identical.

L8 (SPEC_COMPACT 4): the loop stamps the assistant message it appends:
in both the no-calls and the tool-calls branch, with `Done.Usage`'s
prompt+completion as `Message.ContextTokens` (0 when the `Done` reported
none). A named line with its own loop test; it is the anchor the
compaction trigger reads. No other loop change.

State machine per turn: `awaiting_input -> awaiting_model -> executing_tools
-> awaiting_model -> ... -> done`. Deliverable 7 leaves it unchanged; its
additions are inside the states. Tool execution was sequential through
0.11; SPEC_EVT 2a (the batch) is the named loop change that admits
concurrent runs inside `executing_tools` while keeping emission and the
transcript in call order; the one reopening of the frozen loop, with
its own gate clause and re-freeze.

Faults: a `Fault` event or transport error aborts the turn, surfaces the
error through `Notify`, preserves the session up to the last complete
message, and returns to `awaiting_input`. A malformed tool call (bad JSON,
unknown name) is fed back to the model as a tool error result, once; the
retry guard bounds repetition (keyed by tool name, identical retries only,
cleared per turn). The
loop never retries silently.

Cancellation and steering: the run context threads through every await and
its cancel ends the session at the boundary, clean. Each turn also carries a
per-turn context (child of the run's), passed to `Assemble`, `Stream`, and
every execution, and cancelled on every turn exit; the loop threads the
turn's cancel onto the ctx it hands to `Input` (`core.WithInterrupt` /
`core.InterruptFrom`, the `WithSession` pattern), which is the Frontend's
interrupt handle. A dead turn context with a live run context is an
interrupt: at the prompt the loop re-enters `awaiting_input` (no `Fault`),
mid-stream it breaks the turn. The Frontend cancels the handle it was
given to steer; it holds one queued message and delivers it on the next
`Input`. No mailbox; the slot is the Frontend's business. Every turn exit
emits `TurnEnd{Reason}` (`over` / `fault` / `interrupt`), the recorder's
discard signal and the Frontend's boundary signal.

## kernel and wiring

```go
k := rig.New(
	rig.WithProvider(openai.New(baseURL, model)),
	rig.WithTools(bash.New(), file.Read(), file.Write(), file.Edit()),
	rig.WithMiddleware(perm.Allowlist(rules), guard.Bound(3)),
	rig.WithPolicy(policy.Passthrough(systemPrompt)),
	rig.WithFrontend(cli.New(os.Stdin, os.Stdout)),
	rig.WithCommands(command.All()...),
)
err := loop.Run(ctx, k)
```

Functional options over a plain struct. Every dependency is explicit in the
constructor call; swapping happens at registration with zero consumer
changes. An extension is anything registered by an option (a tool, a
provider, a middleware participant, a command); there is no extension
type, lifecycle, or discovery mechanism. `WithCommands` registers the
user commands (deliverable 9, SPEC_COMMANDS) on the same seam: the loop
ignores the registry; dispatch is the Frontend's, and a kernel without
`WithCommands` is byte-identical (the compat rule). Duplicate command
names are a wiring error, refused at construction like duplicate tools.

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

`loop` imports `evt` (SPEC_EVT), an in-repo stdlib-only leaf: the engine
the loop consumes. No third-party line; the stdlib-only rule holds.

## testing

- Loop: fake Provider (scripted event channels) and fake Frontend drive full
  turns; assert message ordering, fault paths, and cancellation mid-stream.
  The fakes live at the DI seam, nowhere else.
- Tools: real filesystem in `t.TempDir()`, real subprocesses for bash. No
  mocked syscalls.
- Middleware: table tests on the chain: the retry guard test proves the
  bounded-repetition invariant by exhausting it.
- Provider adapter: `httptest.Server` replaying recorded llama.cpp SSE
  bodies; malformed and truncated streams are named cases.
- Boundary cases by name: empty transcript, zero tools, tool result at the
  context edge, cancellation between tool calls.
- Deliverable 7 (SPEC_HARDENING): the tool-event bracket order and the
  guarded refusal riding `ToolResult`; `TurnEnd` at every turn exit with
  its reason; reasoning accumulation in both assistant branches; steering
  (the `InterruptFrom` handle cancels the turn and the run continues; the
  slot delivers on the next `Input`; latest wins; the recorder discards
  the unlanded partial at `TurnEnd`; a mid-tool interrupt keeps the
  batch's results with the exec's error string); the guard's name keying,
  per-turn clear, and bound-th note; the compat rule (`TestEvent`
  forwarded untouched by the loop, ignored by the frontends); the resume
  projection (full transcript, dangling calls kept, unknown id loud).
- Deliverable 8 (SPEC_COMPACT): the row invariants by name: the trigger
  boundary at exactly `Window - Reserve` (anchored, 4); the pair-boundary
  cut; the once recovery and the surfaced second fault; `Compacted` compat
  (forwarded untouched by the loop, rendered one line by the CLI, ignored
  by one-shot); the L8 stamp in both assistant branches; the
  resume-after-compaction shape; the `-p` run crossing the trigger
  mid-turn; the clamp's refuse-loud below its minimum (a kept batch larger
  than the window, not the floor-1 slow death); `Request.ReasoningEffort`
  semantics.
- Deliverable 9 (SPEC_COMMANDS): dispatch by prefix with a scripted kernel
  proving the loop never ran; a frontend without commands byte-identical;
  each command by name against the root's closures, refusals included
  (the live-turn refusals through a fake `Steerer`); `steer` live-turn vs
  between-turns and the empty-steer interrupt; `new` closing the old row
  and landing the next prompt in the fresh session with per-process state
  intact; `sessions` resume's validate-before-mutate order; `models`
  switch taking effect on the next turn's request and the runtime table
  including the env-synthesized row; the todo/scheduler round-trips over
  the same tool instances the model gets; one-shot with a command-shaped
  prompt.

## v1 scope

One provider (OpenAI-compatible), four tools (bash, read, write, edit),
allowlist + retry middleware, passthrough policy, CLI frontend, in-memory
session. Roughly 600 lines plus tests. Everything after v1 fills an
interface that already exists.
