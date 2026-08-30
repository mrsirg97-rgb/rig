# rig: user commands and tools (roadmap deliverable 9)

A user-facing command seam and the first commands, so the human has verbs of
their own, not only prompts. In the CLI they are typed lines with a prefix;
in the TUI (10) they become `/` commands over the same seam. Same glass, both
sides: where a command has an agent-side tool (`todo`, `scheduler`), it is
the same `core.Tool` instance.

The deliverable closes the runtime: after this, `core/` and `loop/` are open
to extension and closed to modification (the roadmap's freeze). 10 consumes
the frozen runtime in its own module. This spec is the last one allowed to
touch core (one interface) and the last before the loop freezes for good.

## goals

- One mechanism: `core.Command` registered at the root
  (`rig.WithCommands`), dispatched Frontend-side by prefix before `Input`
  returns to the loop. The loop never sees a command.
- Five runtime commands with named output contracts: `compact`, `new`,
  `sessions`, `models`, `steer`.
- One operator verb over rem (`rem`: list / show / forget): the
  store is a leaf reached through the root's closures, not the tool.
- Two tool-backed commands: `todo`, `scheduler`; over the same
  `core.Tool` instances the model gets: parse the line into the tool's args,
  call `Exec`, print the reply verbatim. No parallel implementation.
- A command is one file plus one registration line: testable with fakes, no
  kernel, no stores, no provider.
- The freeze holds: no loop change, no new deps, `core/` gains `Command`
  and nothing else, the CLI rendering stays greppable plain lines.

## non-goals

- No TUI, no glass, no styling: the CLI prints plain lines: 10 renders them.
- No `/help` command: the refusals name the known commands and their shape;
  the TUI owns the discoverable surface.
- The `rem` command is the operator's verb surface: `rem [list|show|
  forget]` over the same store (SPEC_STATE: rem is deliberate); the model's
  multi-line learn/recall/reflect/prune stays the tool; the operator's
  prune and read get a typed line.
- No commands that need the loop (pausing mid-execution, injecting into a
  running turn, ordering against a tool call): that is a loop change and
  reopens 7 first (decision 1's stop condition).
- No config: the commands are code plus one registration line. Reversed,
  named (SPEC_CONFIG): the models table's source is the merged table
  (the embedded `config/models.json` overlaid by the user's `models.json`,
  SPEC_CONFIG 4) plus the resolved active row; the command's surface
  unchanged except the one named exception below: the role column.
- No steering mailbox: the slot is 7's, latest wins, unchanged.
- No change to `-p` one-shot or `run-job` semantics: commands are
  interactive-only (decision 9); the worker's stdout stays the answer.

## layout

```
core/command.go       NEW: the Command interface (Name, Description, Run)
kernel.go             +Commands field, +WithCommands (duplicate-name panic)
command/              NEW leaf package (stdlib + core + models, nothing else)
  env.go              Env, Steerer, SessionRow
  parse.go            the prefix rule: Parse(line)
  commands.go         the standard set: All()
  compact.go          compact
  new.go              new
  sessions.go         sessions (list / summary / show / resume) + the plain render
  models.go           models (list / switch)
  steer.go            steer
  rem.go              rem (list / show / forget)
  tools.go            todo + scheduler over core.Tool
  parse_test.go, commands_test.go, compact_test.go, new_test.go,
  sessions_test.go, models_test.go, steer_test.go, tools_test.go
policy/compact        +Compact(ctx) (core.Compacted, bool, error); the
                      exported seam 8's scope promised (decision 3)
store/state           +ListSessions (the rows plus a turns count; decision 5),
                      +Recorder.Retarget / Recorder.Ensure (the handoff;
                      decision 4)
frontend/cli          +WithCommands(cmds, env) option, the dispatch inside
                      Input, the Steerer implementation
cmd/rig/main.go       the env, the pair rebuild, the registration; the
                      runtime models table (decision 6)
```

The design test holds at the seams: a new command is one file in
`command/` and one line in the root's `WithCommands`. Zero loop lines. The
loop does not read `Commands`.

## interfaces

```go
// core, additive (the only core change; the compat rule, decision 10):
//
// Command is a user-facing verb: one the human types, not one the model
// calls. A Frontend with command support dispatches by prefix before
// Input returns to the loop; the loop never sees a command. Run gets the
// dispatcher's context, the command's arguments (the line after the name,
// verbatim), and the env the root built (decision 2); it returns the
// reply to print (or the refusal, as an error).
type Command interface {
	Name() string
	Description() string
	Run(ctx context.Context, args string, env any) (string, error)
}

// kernel.go, additive:
k.Commands []core.Command               // the registry; the loop ignores it
func WithCommands(cmds ...core.Command) Option // duplicate names panic, tools' precedent

// command/env.go; the command's world, built at the root:
type Steerer interface {
	Steer(text string) bool // queue latest-wins; interrupt a live turn;
	                  // reports whether the interrupt landed
	Interrupt() bool       // interrupt a live turn only; reports the same
	ClearSlot()            // drop the queued intent (a new session does not inherit it)
	LiveTurn() bool        // a turn is live right now (compact / new / sessions resume refuse on it)
}

type SessionRow struct {
	ID      string
	Started time.Time
	Exit    string
	Turns   int
	Current bool // the live session, marked in the list
}

type Env struct {
	Session func() *core.Session          // the live session (post-swap)

	// frontend-owned seam, filled by the dispatcher (decision 2)
	Steer Steerer                        // nil = the steer command refuses
	                                         // loud; new / sessions resume
	                                         // read Steer.LiveTurn, nil-safe

	// root-owned operations
	Compact       func(ctx context.Context) (core.Compacted, bool, error)
	NewSession    func(ctx context.Context) (string, error)
	SessionList   func(ctx context.Context) ([]SessionRow, error)
	SessionShow   func(ctx context.Context, id string) (string, error)
	SessionResume func(ctx context.Context, id string) error
	Models        func() models.Table
	ActiveModel   func() string
	SwitchModel   func(ctx context.Context, id string) error
	Tools         map[string]core.Tool   // the same instances the model gets
}
```

The `Env` carries closures, not handles: the command package sees
`core` and `models` and nothing else; no store type, no recorder, no
policy, no kernel. The root owns every concrete type; the command owns
only its own vocabulary.

## decisions

### 1. One seam, Frontend-side dispatch, prefix `/`, escape `//`

The mechanism: `core.Command` registered on the kernel
(`rig.WithCommands`), dispatched by the Frontend **inside `Input`, before
it returns to the loop**. A command line is consumed there, exactly as a
blank line is consumed today: the loop's contract is "Input returns one
message"; a prompt. The loop never reads `k.Commands`; the dispatcher is
the Frontend's business, the way the steering slot is (SPEC_HARDENING 4:
"delivery is inside `Input`").

Why the Frontend, and the two candidates rejected, named:

- **The loop.** It would learn the command vocabulary (names, the prefix,
  the unknown-name refusal); a loop change this deliverable exists to
  avoid (the design test: if it touches the loop, the interfaces are
  wrong). Rejected.
- **The root.** The root is wiring, not a runtime participant: the lines
  arrive at the Frontend, and dispatch needs the input stream's state
  (the slot, the reading flag, the interrupt handle). Moving the stream
  to the root re-wires 7's steering contract. Rejected.

The prefix is `/`, the one the TUI (10) will use, so both sides share one
rule. The collision it has is real and named: prompts are paths and shell
lines; `/home/ng/Projects/rig/x.go`, `/bin/ls -la`. The escape is `//`:
a line starting with `//` is a prompt, and the escape consumes one slash,
so `//home/ng/x` reaches the model as `/home/ng/x`. The cost is one extra
slash on a line that would otherwise be hijacked; the alternative is a
prefix the prompt never starts with, and the candidates are worse:

- **Exact-word matching** (`compact`, `steer` as bare lines): every
  command word becomes a prompt the user can no longer type; "compact"
  alone is a plausible prompt, and the not-a-command rule would need an
  escape per word.
- **`\`**: prompts carry backslashes (regexes, escapes, Windows paths),
  and `\\` is a double-escape the shell user already thinks means
  something else.
- **`!` / `>`**: shell history and redirection associations leak into the
  prompt text.

`/` keeps the collision to one character with one escape, and the
escape restores exactly what was typed.

The rule, total:

- A line whose first byte is `/` and whose second is not `/` is a
  command line, full stop. It is never a prompt, whatever it says.
- The name is the first token (up to the first space or tab): the args
  are the remainder after that one separator run; the leading run of
  blanks stripped, everything after verbatim (`/steer fix  it` steers
  with `fix  it`).
- `//` + the rest is an escaped prompt: the model sees `/` + the rest.
- `/` alone is the command line with the empty name: the unknown-command
  refusal, which names the known commands.

**The not-a-command rule.** An unknown command is a loud line, never
silently a prompt:

```
unknown command: bogus (known: compact, models, new, scheduler, sessions, steer, todo)
```

The line is consumed (the REPL reads the next), the refusal names what
was wrong and what would be right (the standing rule). A known command's
own refusal (bad args) prints as-is; the command owns its voice. The
dispatch prints exactly one of the two Run returns: the error if the
command refused, else the reply if non-empty; one line, newline
terminated, on the frontend's stdout (10). A prompt that starts with
the prefix needs the escape (`//`), named above; there is no other path
from a `/` line to the model.

**The stop condition.** If a command needs the loop; pause it mid-tool,
inject into a running turn, order against a tool call; that is a loop
change: reopen 7 first (the roadmap's rule), and this spec's decision 1
is rewritten. Named, like 8's.

### 2. What Run gets: the Env, built at the root: not the Kernel

`Run(ctx, args, env any)`: the dispatcher carries the env, the command
package asserts it, and the env is a struct in `command/` built by the
root; not `*rig.Kernel`.

What the commands need, and where each comes from: the live session
(thread it onto the tool `Exec` ctx, as the loop does), the state store's
read side (sessions list/show/resume), the tool seam (sessions summary),
the models table plus the active
row plus the switch, the policy's Compact seam, the steering slot and
handle. The Kernel carries five of those as fields (provider, frontend,
policy, tools, session) and none of the rest; the store, the recorder,
the active model, the pair rebuild are root state that is not a loop
dependency. Three reasons for the separate struct:

- **core/ stays interfaces.** `Command` is in core: an `Env` in core would
  be a concrete type (the standing layout: interfaces in core, concrete
  types unexported at the leaves). The constraint holds literally: core
  gains `Command` and nothing else.
- **The import direction.** A command that takes `*rig.Kernel` makes
  `command/` import the root package. The root imports `command/` (it
  registers `command.All()`), so one of the two edges goes away; the
  root stops importing the leaf, and "one file plus one registration
  line" means a registration line in a package the commands can no
  longer see. The env inverts it: the leaf depends on `core` and
  `models`; the root depends on the leaf.
- **Fakes.** A command test builds `&command.Env{Compact: fakeCompact,
  Tools: map[string]core.Tool{...}}` and runs `Run`; no kernel, no
  provider, no store, no frontend. A fake kernel would be five fake seams
  and a panic-on-duplicate-name constructor for one function call.

Why `env any` in the core signature: the seam stays type-free (core knows
`Command`, not `Env`), the dispatcher carries what the root built without
naming its type, and a dispatcher that wires a foreign type is refused
loud at dispatch (`command: env is *command.Env (got X)`); a wiring
error named where it is found, the `WithSession` pattern with the type
on the leaf side.

The split of ownership, named:

- **Root-owned** (the root fills them): `Session`, `Compact`,
  `NewSession`, `SessionList`, `SessionShow`, `SessionResume`, `Models`,
  `ActiveModel`, `SwitchModel`, `Tools`. The closures capture the root's
  mutable command state (the active model, the current recorder, the
  current session) at call time, so a swap (decisions 4, 5, 6) is
  visible to every command with no re-wiring.
- **Frontend-owned** (the dispatcher fills it, in its
  `WithCommands`): `Steer`; the slot, the interrupt handle, and the
  liveness fact are the Frontend's contract (7's); the root does not own
  them. `Steer` nil = the steer command refuses loud
  (`steer: no steering seam (the frontend does not support steering)`),
  and `new` / `sessions resume` read `LiveTurn` as false (nil-safe):
  the CLI is structural; dispatch is inside `Input`, and the loop is
  in `awaiting_input`; a TUI dispatching from a keypress mid-turn is the
  one that trips the refusal.

**Concurrency, named.** Commands run on the loop's goroutine: dispatch
happens inside `Input`, the loop calls `Input` sequentially, and no
command outlives the call. The root's mutable command state
(activeModel, the current recorder, the current session) is written
there and read by the loop after `Input` returns; one goroutine, no
locks. The CLI's own state (slot, handle) is under its existing mutex.
A command's I/O runs under the `Input` ctx, so Ctrl-C cancels a running
command the way it cancels anything else.

### 3. `compact`: force the action; the Compacted line, or "nothing to drop"

The policy gains the seam 8's scope promised ("added then, no shape
change owed now"):

```go
// policy/compact
func (p *policy) Compact(ctx context.Context) (core.Compacted, bool, error)
```

Over the same internal compact action: split, summarize, rewrite,
reflect. On a compact it spends the once budget (the trigger path does
the same); the caller owns delivery; the action never emits
(SPEC_COMPACT 5), so the command path delivers exactly once. Forcing
bypasses the trigger on purpose: the trigger is the model's window math;
the verb is the user's judgment. The action's own boundaries still apply
loud; nothing to drop (empty older prefix), summary input that does not
fit the window (the numbers named).

The root's env closure: run `Compact`; on success deliver the event to
the current recorder (`rec.Notify`; the recorder lands the summary row
plus its usage row, re-lands the kept tail, and forwards to the CLI),
return the event. The command's output contract:

- compacted: the `Compacted` line, the existing CLI rendering, exactly
  once; `⧉ compact: -12k kept 16k · summary ↑812 ↓640`, and nothing
  else (the command prints no second line; the event line is the
  output).
- nothing to drop: `compact: nothing to drop` (no event, no row, the
  transcript untouched).
- refused: the action's error, verbatim
  (`compact: local: the summary input alone does not fit the window:
  window 65536, estimate 71000`).
- a turn live (the dispatcher's `LiveTurn`, nil-safe as in 2):
  `compact: a turn is live; steer or interrupt first`. The action
  rewrites `Session.Messages`; a mid-turn rewrite races the loop's own
  read of the transcript, and the channel-ordering property that makes
  the decorator's rewrite safe (SPEC_COMPACT 7's named loop property)
  does not hold on the command path. Structural in the CLI (dispatch is
  inside `Input`); the TUI's mid-turn keypress is the case.
- args: none. `compact extra` → `compact: usage: compact`.

Nothing is handed to rem on this path, exactly as on the trigger path
(the action is shared); a forced compaction is a compaction, and a
compaction writes no memory (amended 0.13.0, SPEC_STATE: rem is
deliberate; the `AutoReflect` handoff is cut).

### 4. `new`: close the row ok, mint fresh, keep the process: and the handoff

`/new` closes the current session row with exit `ok` (the user chose to
close it: not a fault, not Ctrl-C's `cancelled`), mints a fresh
`core.Session` and a fresh recorder, rebuilds the provider+policy pair
on the same inner model, and swaps all of it into the kernel; same
process. The per-process state, named: the python kernel survives (its
state lives in the tool instance, which stays in `k.Tools`; one kernel
per process, SPEC_PYTHON's "per session" reads as "per process" in
rig's one-session-per-process shape, and `/new` does not reset
variables), the retry guard survives (a middleware participant; its
per-turn budget clears at the next `TurnStart` anyway), the
rem/todo/scheduler stores survive (workspace-scoped, not
session-scoped). The steering slot is cleared: a steer queued for the
old session is not delivered into the new one.

**The handoff (the subtle part, named).** Dispatch happens inside the
current recorder's `Input`; the chain is loop → `rec1.Input` →
`cli.Input` → dispatch. If the swap retired `rec1` silently, its in-
flight `Input` would land the next user row under the just-closed
session's id while the transcript (and the model's request) belongs to
the new one. The swap therefore re-points the retiring recorder before
it completes:

1. `rec1.Close("ok")`; close the old row (the row exists: `ensure` ran
   at the start of `rec1.Input`).
2. Mint the new session `s2`; build `rec2` over the same inner
   frontend; `rec2.Ensure()`; the new row exists before any row lands
   under it.
3. `rec1.Retarget(s2.ID, s2)`; the in-flight `Input` lands the user row
   (and the files snapshot) under the new session, then retires.
4. `k.Frontend = rec2`, `k.Session = s2`, and the pair
   (decision 6's rebuild) on `(rec2, s2, the active row)`.

Same goroutine, no locks; the loop reads `k.Session` after `Input`
returns, so the prompt it appends is already on the new session. Two
small additions to the recorder, named in the layout: `Retarget` and
the exported `Ensure`.

The output contract:

- success: `new: session <id>`: the fresh id (it is in the store, in
  the session row, and on the line).
- a turn live (the dispatcher's `LiveTurn`): `new: a turn is live;
  steer or interrupt first` (structural in the CLI; a TUI keypress
  mid-turn is the case).
- args: none. `new extra` → `new: usage: new`.
- a refused close (store fault) is loud and the swap does not happen:
  the current session continues (`new: <error>`).

### 5. `sessions`: list, summary, show, resume: over the rows that exist

`list`, `show`, and `resume` read the workspace state store directly (the
file is already workspace-keyed by cwd, SPEC_STATE's paths; no cwd filter
needed); `summary` reads it through the `sessions` tool (SPEC_STATE).

**`sessions`**; the list, newest first, capped at 50 (a glance, not an
archive; `show` is the deep read). One plain line per row, the current
session marked:

```
01j3c4x9ab12  started 2026-07-09T12:00:00Z  exit open   turns 3  *
01j3c2f7cd01  started 2026-07-09T09:14:11Z  exit ok     turns 12
01j3b19eaa55  started 2026-07-08T16:02:47Z  exit fault  turns 1
```

`exit open` is the render of a row not yet closed (`ended_at` NULL):
the one place the word appears; the store's exit vocabulary stays
`ok | fault | cancelled`.

`turns` is defined, not counted by feel: a turn starts with a user
prompt, so turns = the session's `role = 'user'` rows minus the
`[compaction] ` summary rows (SPEC_COMPACT 5's marked user rows are
transcript machinery, not prompts). One `ListSessions` read in
`store/state` (the layout's owed line): the rows plus that count as a
scalar subquery, `ORDER BY started_at DESC LIMIT 50`. No rows:
`sessions: none`.

**`sessions summary`**; the soak's vitals over the recent sessions: the
command calls the `sessions` tool (SPEC_STATE) with `{"action":"summary"}`
and passes the reply through verbatim (the tool owns the shape: the
session and turn counts, the models with their versions, the fault count
with the latest fault's first line, the aggregate cache ratio). This
workspace at the default `n`; the verb takes no args, so the operator's
glance is this project's soak. Refusals, named: `sessions summary extra`
→ `sessions: summary takes no args (sessions summary)`; no tools seam →
`sessions: no tools seam (the root did not wire one)`; a tools seam that
lacks the sessions tool → `sessions: no sessions tool (the root did not
put it in Env.Tools)`.

**`sessions show <id>`**; the transcript projection (the same
`state.Resume` function `-resume` uses; one projection, one truth)
rendered plain, numbered in projection order, headers always
`[n]`-prefixed so `grep '^\['` walks the conversation:

```
[1] user: fix the flaky test
[2] assistant: let me look
    thinking: the guard test is the flaky one…
    call c7 bash go test ./middleware/
[3] tool (c7): ok  middleware/guard 0.4s
[4] assistant: fixed the race in the budget map
```

- one line per message: multi-line content keeps its lines verbatim
  (unindented; the `[n]` prefix is what makes a header a header);
- assistant messages: the content on the header line (possibly empty),
  then `    thinking: <reasoning>` when the model thought, then
  `    call <id> <name> <args>` per tool call (args as the stored JSON);
- tool results: `[n] tool (<id>): <result>`;
- a compaction summary renders as an ordinary user row: the marker is
  in the content and self-describing.

Refusals, named: `sessions show` (no id) → `sessions: show needs an id
(sessions show <id>)`; unknown id → `sessions: no such session: <id>`;
`sessions show a b` → `sessions: show takes one id`.

**`sessions resume <id>`**; the in-process swap: the same handoff as
`new` (4), except the new session is the projection of the named one.
Order, named: validate before mutate:

1. `Steer.LiveTurn` (nil-safe, decision 2) → refuse: `sessions: a turn
   is live; steer or interrupt first`.
2. the current id → refuse: `sessions: already the current session:
   <id>`.
3. `state.Resume(id)`; unknown id is loud here, **before** the current
   row is touched: `sessions: no such session: <id>`.
4. only then close the current row `ok`, `Ensure` the resumed row,
   re-target, swap, clear the slot.

The resumed session keeps its identity (the recorder adopts the
existing row, 7's `-resume` semantics), so todo's claims and rem's
sources attribute to it; its file provenance is the projection's
(`Session.Files`), so drift checks resume; the per-process state
(python kernel, guard) survives, as with `new`. A row that was `open`
when a process died is resumed normally and closed by whatever ends it
next; adoption is idempotent, named.

Output: `sessions: resumed <id> (<N> messages)`, N the projection's
message count.

Usage: `sessions <other>` → `sessions: usage: sessions [list|summary|show|resume
<id>]`. `list` is the bare command's read under a name; the verb the
TUI's menu (10) accepts, and the `Sub()` hints are `list`, `summary`,
`show`, `resume`, one-lined, so `/sessions` opens the same selectable, scrollable
verb menu as `todo` and `scheduler` (SPEC_TUI 9).

### 6. `models`: the table, and the switch that takes effect next turn

**`models`**; the table, one plain line per row, the active row
marked, the raw token counts (greppable; `formatTokens` is the event
shaping, not the table's):

```
local            interactive  window 65536  max 8192  reserve 8192  keep 16384  trigger 57344  *
qwen3.8-workers  worker       window 65536  max 8192  reserve 8192  keep 16384  trigger 57344
```

The columns are the row's own fields (8's invariants make each
self-contained) plus `trigger = Window - Reserve`; the boundary the
operator watches, and the **role column** after the id (SPEC_CONFIG 4,
the named exception of this spec's 6): `interactive` or `worker`,
validated at parse. The rows come from the **runtime table**: the merged
table (the embedded `config/models.json` overlaid by the user's
`models.json`, SPEC_CONFIG 4) with the active row replaced by the
resolved row when resolution overlaid or synthesized it, so it lists
and `models <id>` can switch back to it. A table the operator cannot
see is a table the operator cannot use. File rows list like any others:
same columns, same switch, same refusal voice for unknown ids; the
listing order is the table's `Known()` order (sorted by id), so the
lines do not depend on merge order.

**`models <id>`**; switch the active model for the **next turn**, by
rebuilding the provider+policy pair at the root; the seam 8 named
("the models command reads the same table and switches the active model
by rebuilding the policy pair at the root; `k.Policy` and `k.Provider`
are the root's fields to write, and the loop borrows them per turn"):

1. the row must exist in the runtime table: unknown id is loud, naming
   the known; `models: no row for "nope" (known: local,
   qwen3.8-workers)`;
2. rebuild: a fresh inner `openai.New(baseURL, id)` (the model name
   rides the wire on every request, so the next request is the new
   model), a fresh `compact.New(inner, the current recorder, the current
   session, the system prompt, the row)`, `k.Provider =
   compact.Decorator(inner, pol)`, `k.Policy = pol`, and the active
   model state; the root's one mutable string every closure reads;
3. the transcript, the session row, and the recorder are untouched: the
   switch is not a new session, and the row keeps the model the session
   started with (a historical record; the switch is not retroactive).

The effect is the next turn's request: the loop reads `k.Provider` /
`k.Policy` fresh at each turn start (it already does; that is why the
swap is legal), so the next `Assemble` is the new policy with the new
row's math (trigger, keep budget, the clamp), and the next `Stream` is
the new model on the wire. The per-process state (guard, python
kernel) survives, as with `new`.

Mid-turn, named (no refusal needed): the swap is safe against a live
turn because the in-flight turn holds its stream channel and the old
decorator finishes its own relay; including an overflow recovery
against the old pair; while the loop reads `k.Provider` / `k.Policy`
fresh at the next turn start. The switch is next-turn by construction,
not by guard.

The session row's `model` column stays the model the session started
with; a historical record, named in 4's contract; the switch is not
recorded per-message. A `[models] switched to <id>` breadcrumb row was
considered and rejected: it would put transcript machinery in the
model's context for a fact the operator can read from the command's own
output and the store's usage rows.

Output: `models: active is now <id>`. Usage: `models` with two or more
args → `models: usage: models [<id>]`.

### 7. `steer`: 7's slot, made a verb

Deliverable 7 already has all the mechanics: the interrupt handle rides
the `Input` ctx (`core.WithInterrupt`/`InterruptFrom`), the slot is one
queued message, latest wins, and a line typed during a live turn is
already queued-and-interrupted by the CLI's reader. The command exposes
the same behavior as a named verb with a named output; including the
between-turns queue, which plain lines reach only by becoming a prompt:

- **`steer <text>`**: queue the text (latest wins, replacing whatever
  is there) and interrupt a live turn if one is; queue only if not.
  The dispatcher's `Steer` method does both (queue + interrupt-if-live)
  and reports whether the interrupt landed. Output:
  `steer: queued <text>`, or `steer: queued <text> · turn interrupted`
  when a live turn was broken. A steer typed during a live turn is
  delivered at the re-entry as the next user message; the model runs
  on it; a steer typed between turns is delivered on the next `Input`,
  which is the current one: it becomes the next prompt, exactly as a
  typed line would, with no turn wasted.
- **`steer`** (empty): interrupt only: no text is queued (the slot, if
  it holds an earlier steer, keeps it only if it arrived after; latest
  wins is the slot's rule, not the command's). Output:
  `steer: interrupted` when a live turn was broken, `steer: no live
  turn` when there was nothing to break.
- **no seam**: a dispatcher that did not fill `Env.Steer` refuses
  loud: `steer: no steering seam (the frontend does not support
  steering)` (the compat shape: a Frontend without steering simply
  never gets the command to work).

The CLI's liveness truth, named: a line typed while the agent works is
interrupted by the reader at arrival (7's behavior, unchanged) and the
command at re-entry reports that interrupt (`· turn interrupted`,
`steer: interrupted`); a line typed at a quiet prompt interrupts
nothing. `LiveTurn` (the `new`/`resume` refusal predicate) is the
narrower fact; a turn live *right now*, and in the CLI is
structurally false at dispatch (the loop is in `awaiting_input`); it is
the TUI's mid-turn keypress that trips it.

### 8. `todo` and `scheduler`: the same tools the model gets

The adapter is thin and shared (one file, `tools.go`): parse the line
into the tool's JSON args, call `Exec` with the session threaded
(`core.WithSession(ctx, env.Session())`, the loop's own threading), and
print the reply **verbatim**; success or refusal. No parallel
implementation, no re-voicing: the queue the model reads is the queue
the user reads, and the tool's own refusals teach the protocol
(`todo: no task 't9'`, `scheduler: pause requires 'id' (jN)`).

The arg syntax is named; `<tool> <action> [id] [n]`, token-shaped, and
the per-action shape is enforced at the boundary (extras are refused
loud, not silently dropped into the tool's ignored fields):

```
todo    read
todo    create <text…>          the whole remainder, one task's text
todo    start|complete|fail|retry <id>
todo    move <id> <pos>
todo    project <path>         show that project's queue (a one-off read)
scheduler list
scheduler create <name> <prompt…> <cron>     5-field vixie, or
scheduler create <name> <prompt…> once <ISO>
scheduler pause|resume|remove <id>
scheduler runs <id> [n]
```

- `create`'s `name` is the first token after the action: the prompt is
  the tokens between name and cron; the cron is the tail; five vixie
  fields, or `once <ISO>`. The tail rule is total for vixie cron (five
  space-separated fields) and for `once` (a one-word cron plus one
  token); a create that fits neither is the adapter's own refusal,
  naming the shape: `scheduler: create needs name, prompt, and a cron
  (5-field, or 'once' <ISO>)`. The store still validates the cron it
  gets; the adapter parses, the store teaches.
- `todo create <text…>` replaces the whole queue with the one task
  (create's semantics; the line is for the one-task case). A bare
  `todo create` passes `{"action":"create"}` to the tool, whose refusal
  (`action 'create' requires tasks: array of {text}`) teaches that the
  queue is replaced; clearing it (`tasks: []`) is a model-side call:
  the line shape has no spelling for an empty array, and that is fine.
- `todo project <path>` is a one-off read of that project's queue: the
  path resolves through the same scope law as the model's `project` tool
  field (SPEC_STATE), writes stay the model's or the session's own bare
  verbs. An unknown path's empty queue renders `(no tasks in <label>'s
  queue)`; the empty reply names the scope it read (SPEC_CORE). A bare
  `todo project` refuses naming the shape.
- the int slot is parse-checked: `todo start t1 extra` →
  `todo: start takes an id (todo start <id>)`; `scheduler runs j2 x` →
  `scheduler: "x": not an integer (scheduler runs <id> [n])`.
- a bare `todo` / `scheduler` passes `{"action":""}` to the tool: the
  tool's own `action required` / `unknown action` voice. No new verbs:
  the action vocabulary is the tool's schema, and a line the adapter
  cannot place is refused with the shape, not guessed.

The tools come from `Env.Tools`; the same instances the kernel
executes (the root puts the live `todoTool` / `schedTool` in), so a
`todo start t3` by the user and a `todo` call by the model act on the
same queue, same session attribution (the live session's id, or `anon`
unthreaded; the tools' existing behavior), same store.

- **No fleet, no `/scheduler`** (SPEC_CONFIG 12's presence rule): with
  no `workers.json` the root registers no `scheduler` tool, and the
  command refuses by name before the tool lookup: `scheduler: no
  workers configured (~/.rig/workers.json names the model)`; the same
  string the dashboard's scheduler view shows (SPEC_SERVE). The
  refusal is the command's, not the generic no-tool voice: the
  operator is told what is missing (the file, and its job; it names
  the model), not only that a tool is absent. The fleet's model and
  file path ride on the command env, so the refusal names the home the
  root actually read, not a hardcoded path.

Scheduler ids are one sequence across the single store (SPEC_STATE): a
`jN` names the same job from any directory, the grammar has no `scope`
(the tool's schema lost it), and `name` is unique store-wide. `list` is
one list grouped by the job's own `cwd`, this directory first.

### 9. One-shot and run-job: commands are interactive-only

`-p` and `run-job` do not dispatch: the one-shot frontend takes no
commands, and a command-shaped prompt is a prompt; named. `-p
"/compact"` runs the model on a user message whose content is
`/compact`; the worker's stdout stays the answer, byte-identical in
shape (7's non-goal, kept). The glass is the REPL's; the worker does not
type.

Why not dispatch in one-shot: `-p` is one `Input` that returns the
prompt and EOF; dispatching there would make `/new` close a session
that never had one and `/steer` queue into a slot that delivers once.
The worker's semantics (fault propagation, the run record, the cold
shell) are frozen by 7 and 8 and are the scheduler's ground; a command
verb under `-p` is a new worker mode, and a new worker mode is a
roadmap line, not a deliverable-9 side quest.

### 10. The compat rule, extended

- **core**: gains `Command` (a new file) and nothing else: no event, no
  wire type, no `Frontend` method. A kernel built without
  `WithCommands` is byte-identical: the loop does not read `Commands`.
- **Frontends**: dispatch is optional. A Frontend without command
  support (one-shot, the test drivers, the TUI until 10) never
  dispatches; its `/` lines are prompts, as today; nothing is
  hijacked from it.
- **The event vocabulary**: unchanged. The forced compact (3) re-uses
  the existing `Compacted` event and its existing CLI rendering; no new
  event is needed for any command. A command's output is the
  frontend's stdout; it does not land as a transcript row (the
  `Compacted` row is the exception: it is a transcript event,
  SPEC_COMPACT 5, and lands as one).
- **The recorder**: one behavioral addition, the handoff (4): a
  re-target of the retiring instance, invisible to the vocabulary.
- **The loop**: zero diff (no L9). The existing named loop cases pass
  byte-for-byte; `-p` and `run-job` are unchanged (9).

### 11. `rem`: list, show, forget: the operator's verb

**`rem`**; the bare command reads the live memories, project scope then
global, one plain line each (`m<id> · <kind> · <age> · <strength> · <first
80 chars>`; a glance, not a browse; the store caps it). **`rem show
<id>`**; the full memory row (all fields, the source, supersession).
**`rem forget <id>`**; prune-remove that id (the operator's prune, a
verb); only this project's or a global memory; ids are file-wide, and a
typo must not reach another repo's row: `rem: another project's memory:
m<id> is <label>'s; forget it from there`. **`rem project <path>`**; that
project's live memories, `rem list`'s shape (project then global, one
plain line each); the empty reply names the project (`rem: no memories in
<label>`). Show and forget stay id-addressed and file-wide; the 0.13.0
forget wall stands, so `rem project` is a read only.

Rejected, named: `rem pin <id>`. Importance is only the per-access
reinforcement multiplier, so a pin on a live row changed nothing the
operator could see (the strength kept decaying, the list line kept its
number), and an operator write that is not a prune is off the surface:
the keep-this act is the model's learn/reflect. If lived use asks for a
hold above decay, design a real one (strength reset, a marker the list
shows) then.

Refusals, named: `rem show` (no id) → `rem: show needs an id (rem show
<id>)`; `rem show a b` → `rem: show takes one id`; an unknown id →
`rem: no such memory: <id>` (show and forget both name the gap); `rem
forget` (no id) → the needs-an-id voice; `rem project` (no path) →
`rem: project takes a path (rem project <path>)`; `rem <other>` →
`rem: usage: rem [list|show|forget <id>|project <path>]`.

Why the command and not the tool: the tool's learn/recall/reflect/prune
is the model's multi-line JSON surface; the operator's read and prune
want a typed line. The root wires four closures over `store/rem`
(`List`, `Show`, `Forget`, and `Label`; the last for the empty-project
naming); `command/` stays a leaf (no store import, SPEC_COMMANDS 2).
`list` is the bare command's read under a name, and the `Sub()` hints
are `list`, `show`, `forget`, `project` (SPEC_TUI 9). `rem project` is
the same `List` closure with a project path: the root resolves it through
`store/scope` (`~` expands at the boundary), so `command/` never touches
a store.

## testing

Named cases, failing first (the standing rule). Fakes at the DI seam: a
fake `Env` for the command files, a scripted `core.Provider` for the loop
cases, real stores in `t.TempDir()` where a case names one, a fake
crontab spool for the scheduler (the e2e's existing pattern).

**The prefix rule** (command):

- `TestParseCommandShape`: `/compact` → (`compact`, ``); `/compact
  now` → (`compact`, `now`); `/compact   a  b` → args `a  b` (the
  leading run stripped, the interior verbatim); the tab separator; `/x
  y z` → (`x`, `y z`).
- `TestParsePrefixEdge`: `/` → the empty name (a command line);
  `//home/ng` → not a command (the CLI returns the prompt
  `/home/ng`); `///x` → the prompt `//x`.
- `TestUnknownCommandIsLoudNeverAPrompt`: `/bogus` prints the refusal
  naming the known list; the transcript is untouched; the next line
  runs as a prompt.

**Dispatch with a scripted kernel; the loop never sees a command:**

- `TestDispatchByPrefixLoopNeverSeesCommand`: `loop.Run` over the CLI
  with commands and a scripted provider: lines `/models`, `hello`, EOF.
  The provider is called exactly once; the request carries exactly one
  user message, `hello`; the models command's fake recorded the call;
  no `/models` anywhere in the transcript or the request.
- `TestFrontendWithoutCommandsIsUnchanged`: the CLI without
  `WithCommands`: `/models` lands as the user row `/models` (the compat
  rule, both sides).
- `TestOneShotCommandShapedPromptIsAPrompt`: `-p "/compact"` (e2e, the
  scripted `httptest` provider): the request carries the user message
  `/compact`; the env's `Compact` was never called; stdout is the
  assistant text only.

**compact:**

- `TestCompactForcesTheAction`: a fixture transcript below the trigger,
  a scripted summary provider: the transcript is rewritten to
  `[summary] + tail`, the `Compacted` event is delivered to the
  recorder (the summary row + usage row land, the tail re-lands),
  nothing lands in rem (0.13.0), the CLI renders the one `⧉` line, and the
  once budget is spent behaviorally: with nothing new since the forced
  compact, the next main call's context-length fault surfaces without
  recovery (the transcript has not grown past the compact's key).
- `TestCompactNothingToDrop`: a single-message transcript:
  `compact: nothing to drop`; the transcript untouched; no event, no
  row. Same for an empty session.
- `TestCompactSummaryInputDoesNotFit`: the loud refusal naming the
  window and the estimate.
- `TestCompactUsageRefusal`: `compact extra` → `compact: usage:
  compact`.
- `TestCompactRefusesLiveTurn`: a dispatcher reporting `LiveTurn`
  true: the refusal, the transcript untouched, no event, no row.

**new:**

- `TestNewClosesOldRowAndNextTurnLandsInFreshOne`: real state store:
  prompt, `/new`, prompt, EOF. Two session rows: the first `exit ok`
  with `ended_at` set, the second closed ok at EOF; the first prompt
  row under the first id, the second under the fresh id; the fresh
  session's projection carries only its own prompt.
- `TestNewKeepsPerProcessState`: the python tool instance and the
  guard participant are the same pointers in the kernel before and
  after (named in the spec, asserted in the test).
- `TestNewDropsTheQueuedSteer`: a steer text in the slot, then `/new`:
  the slot is empty and the next prompt is the typed line, not the
  steer text; the fresh transcript has no steer text.
- `TestNewRefusesLiveTurn`: a dispatcher reporting `LiveTurn` true:
  `new: a turn is live; steer or interrupt first`.

**sessions:**

- `TestSessionsList`: three seeded rows (an open current, an ok, a
  fault) with message rows including a `[compaction] ` user row: the
  exact lines, newest first, the current marked, the summary row
  excluded from the turns count; an empty store prints `sessions:
  none`.
- `TestSessionsShow`: a seeded transcript with multi-line content,
  assistant thinking, a tool call and its result, and a compaction
  row: the exact render (the shape in 5); no id → the usage line;
  unknown id → `sessions: no such session: <id>`; `sessions show a b`
  → the one-id refusal.
- `TestSessionsResume`: the live-turn refusal; the current-id
  refusal; the happy path (the old row closed ok, the next prompt lands
  under the resumed id, the transcript is the projection plus the new
  row, the files provenance restored); the unknown-id refusal lands
  **before** the current row is touched (assert the current row is
  still open).
- `TestSessionsUsage`: `sessions frob` → the sub-verb usage line.
- `TestSessionsSubHints`: the `Sub()` hints are `list`, `show`,
  `resume`, each one-lined (the TUI's menu door); `sessions list` is the
  bare command's read, not a usage refusal.

**rem:**

- `TestRemListProjectThenGlobal`: seeded project and global live
  memories: the bare `rem` renders one line each, project rows before
  global rows, the exact `m<id> · <kind> · <age> · <strength> · <first
  80 chars>` shape; a superseded memory is not listed.
- `TestRemShowAndForget`: `rem show <id>` renders the full row;
  `rem forget <id>` removes it (the row is gone, not superseded).
- `TestRemRefusalsByName`: `rem show`/`rem forget` (no id),
  `rem show a b`, `rem project` (no path), an unknown id, and `rem frob`
  each refuse naming the gap and the usage.
- `TestRemProjectRendersAndNames`: `rem project <path>` renders that
  project's live memories in `rem list`'s shape (project then global);
  an empty project names it (`rem: no memories in <label>`).
- `TestRemSubHints`: the `Sub()` hints are `list`, `show`, `forget`,
  `project`, each one-lined; `rem list` is the bare command's read, not
  a usage refusal.

**models:**

- `TestModelsListMarksActive`: two rows, the exact lines (window,
  max, reserve, keep, trigger), the active one carrying the marker.
- `TestModelsSwitchUnknownNamesKnown`: `models: no row for "nope"
  (known: local, qwen3.8-workers)`.
- `TestModelsSwitchTakesEffectNextTurn`: two scripted providers:
  `models <id2>`; the next prompt reaches provider two (and only
  provider two); `ActiveModel` reports the new id; the new policy was
  built with the new row (the next clamp/trigger math is the new
  row's).
- `TestModelsRuntimeTableIncludesSynthesizedRow`: a row synthesized
  from env at startup lists and switches (the root's table, 6).

**steer:**

- `TestSteerLiveTurn`: a line during a scripted stream: the turn
  breaks (`TurnEnd{interrupt}`), `steer: queued X · turn interrupted`,
  and X drives the next model call as the user message.
- `TestSteerBetweenTurns`: at a quiet prompt: no interrupt, X is the
  next user message, `steer: queued X`.
- `TestSteerEmptyInterrupts`: a live turn: the turn breaks, the slot
  keeps no steer text, `steer: interrupted`; a quiet prompt after a
  clean boundary: `steer: no live turn`.
- `TestSteerNoSeam`: `Env.Steer` nil: the loud refusal (a fake
  dispatcher that does not fill it).

**the tool-backed commands (real stores, `t.TempDir()`):**

- `TestTodoCommandRoundTrip`: `todo create write the spec` (the reply
  verbatim), `todo read` (the queue), `todo start t1` (the state
  change, verbatim), `todo start t9` (the tool's `no task 't9'`,
  verbatim), bare `todo` (the tool's `action required`), `todo start
  t1 extra` (the adapter's shape refusal naming `todo start <id>`).
- `TestSchedulerCommandRoundTrip`: `scheduler create nightly report
  0 3 * * *` (the crontab spool gains the tagged line; `list` shows
  it), `scheduler create once-job do it once 2030-01-01T00:00:00Z`
  (the once job with its at), `scheduler pause j1` / `resume j1` /
  `remove j1` (the state, verbatim), `scheduler runs j1 2` (the run
  lines or the no-runs voice), `scheduler list extra` (the shape
  refusal), `scheduler create short` (the create refusal naming the
  shape).
- `TestSchedulerCommandNoFleetRefuses`: no fleet on the env
  (`Env.Workers` absent): any `scheduler …` line refuses by name, the
  command's voice (`scheduler: no workers configured (… names the
  model)`), the file path the home the root read; the generic
  no-tool voice is not the one.
- `TestToolCommandThreadsTheLiveSession`: a fake tool asserting
  `core.SessionFrom(ctx)` returns the live session's id (post-`/new`).
- `TestCommandEnvRefusedLoud`: a `Run` with a foreign env type: the
  refusal names `*command.Env`.

**e2e (cmd/rig, the built binary, a scripted provider, a scratch config
home):**

- `TestREPLCommands`: the REPL over stdin: `/models` (the table line),
  `/todo create x` + `/todo read` (the queue), `/new` (the fresh row),
  `/sessions` (both rows, the old one closed ok), exit 0; the state
  store under the scratch home carries the two session rows.

The suite is green on a box with no model loaded: every case is
scripted, httptest, or a real store in a temp dir.

## the diffs this spec implies

PR A carries this spec file only; the diffs below land with PR B.

- **SPEC_CORE**: `core/` gains `Command` (a new file, additive: the
  compat rule's subject extended: a kernel without `WithCommands` and a
  Frontend without dispatch are both byte-identical, named); the
  kernel section gains `Commands` / `WithCommands` (the registration,
  the duplicate-name panic); the Frontend section gains the dispatch
  contract (the prefix rule, the consumption inside `Input`, the
  `//` escape, the not-a-command rule, the dispatcher filling the
  frontend-owned seams); the layout gains `command/`; the testing
  section gains the named cases. Nothing else: the loop section gains
  no L-number (zero loop change, 10).
- **SPEC_COMPACT**: one line owed: the scope's "added then" seam
  lands: `Compact(ctx) (core.Compacted, bool, error)` over the internal
  action, the shape as promised, the caller owning delivery (3).
- **SPEC_STATE**: one owed read, `ListSessions` (the rows plus the
  turns count as a scalar subquery, newest first, capped; 5), and two
  small recorder additions for the handoff, `Retarget` and the
  exported `Ensure` (4). No schema change: the rows are existing
  shapes.
- **SPEC_HARDENING**: owes no diff: the `steer` command is 7's slot
  and handle made a verb (7), the CLI's steering semantics are
  unchanged, and `LiveTurn` is the dispatcher's own state (the
  reading flag), not a new seam.
- **ROADMAP** (PR B): 9 marked done: the runtime is `v1.0.0`; the
  freeze the preamble names; 10 consumes it.

## scope

What 9 is not:

- The TUI glass (10): glyphs, footer, the render kit. The CLI's plain
  lines are the contract 10 renders; the command seam is the contract
  10 dispatches over, the way it already dispatches over the steering
  slot.
- A `rem` command (8's argument stands: the line shape is for
  token-shaped args) and a `/help` command (the refusals name the known
  set; the TUI owns discoverability).
- A command that needs the loop (1's stop condition): if 10 wants
  "pause the turn from the sidebar," that reopens 7, not this.
- Command configuration: the standard set is code: a custom command is
  one file plus one registration line, wired by the root, as every
  other extension.
- The steering mailbox: the slot is 7's, one message, latest wins: the
  `steer` command queues into it and does not widen it.

The loop at the end of 9 is the loop of the end of 8: L1–L7 (7) plus
L8 (8's anchor stamp); byte-identical through 9. 10 inherits it, per
the roadmap: if the TUI needs a loop change, 7 or 9 was incomplete and
is reopened first.
