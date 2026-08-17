# rig: policy/compact — compaction as a ContextPolicy (roadmap deliverable 8)

The first non-passthrough `ContextPolicy`. Today there is none: passthrough
replays the whole transcript, and a worker on a 64k window simply hits the
wall. This is the load-bearing seam for local models, and it lands before
any tool that would hide it (the roadmap's order).

The policy wraps the passthrough: below the trigger it is the passthrough,
byte-identical; at the trigger it rewrites the older transcript into a
summary through the same `core.Provider` and returns system + summary +
kept tail. The loop is byte-identical; the policy owns when and how.

The reference for what this should feel like from the transcript side is
pane's `session_compact` hook: compaction is not a user-facing feature, it
is a transcript event — one line in the CLI, one row in the state store,
one deduped low-importance reflection in rem. `store/rem.AutoReflect` is
the port of that hook, already built and already tested; this spec wires
it.

The bug this exists not to inherit (the pi bug, seen 2026-08-15): a global
reserve larger than the worker's window fires compaction every turn,
headless `-p` included. In rig the reserve is the model's, checked against
the model's own window, and the case is impossible by construction
(decision 2).

## goals

- Compaction as a leaf: `policy/compact` plus a `models` table, registered
  at the root. The design test holds at the seams; the loop is
  byte-identical.
- A window-relative, per-model trigger from a root-owned table
  (id, window, maxTokens, reserve, keepRecent) that deliverable 9's
  `models` command reads.
- A kept tail that stays whole: the cut is a token budget at a message
  boundary, never inside a tool-call/tool-result pair.
- A named event (`Compacted`) the CLI renders as one line and the TUI (10)
  can show; the recorder lands the summary as a message row; the loop is
  byte-identical.
- The compaction summary becomes a deduped low-importance reflection in
  rem, scoped to the cwd (pane's `session_compact`, wired).
- Overflow recovery: a provider fault that names context length triggers
  one compact-and-retry, once, then surfaces. Never a silent loop.
- `-p` workers get the same policy and the same numbers; a config that
  cannot work is refused at start, not slow-dead.

## non-goals

- No loop change. If one turns out to be needed, 7 is reopened first (the
  roadmap's rule) and decision 7 is rewritten — that is the stop condition.
- No TUI, no footer: the CLI prints one line; 10 renders it.
- No chunked (multi-pass) summarization: an old prefix that does not fit
  the summary window is a loud failure (decision 3's boundary), not an
  invented algorithm.
- No new dependencies: stdlib only in `models/` and `policy/compact/`;
  the summary prompt is a file via `go:embed`.
- No config files: the table is code plus `RIG_MODEL_*` env at the root.
- No manual compact verb yet (deliverable 9's `compact` command attaches
  to the same seam, named in scope).
- No state schema change: the summary is a message row plus a usage row,
  both existing shapes (SPEC_STATE owes no diff).
- No tokenizer: bytes/4, calibrated, named approximate (decision 4).

## layout

```
models/             NEW, stdlib only
  models.go         Model, Table, Check, Defaults, Resolve
  models_test.go    the row invariants by name
policy/compact/     NEW, stdlib only
  compact.go        the policy: the Assemble path, the compact action,
                    the calibration state
  split.go          the keep-recent cut at the pair boundary
  estimate.go       bytes/4, the calibrated estimate, the max_tokens clamp
  decorator.go      the overflow decorator: the classifier, the once budget
  summary_prompt.txt  the summary instructions (go:embed)
  compact_test.go, split_test.go, estimate_test.go, decorator_test.go
core/provider.go    +Compacted event, +Request.MaxTokens (named)
core/policy.go      the ContextPolicy doc: compaction landed, named
provider/openai     +max_tokens wire mapping (named)
store/state/recorder.go  +Compacted case: the summary row + usage row,
                    the tail re-landed (named)
store/state/resume.go    +the marker restart of the projection (named)
frontend/cli        +Compacted one line (named)
frontend/oneshot    unchanged: Compacted ignored per the compat rule (named)
cmd/rig/main.go     the row resolution (loud), the wiring, the env (named)
loop/               byte-identical, no diff
```

The design test holds at the seams: compact is a policy plus a provider
decoration, three registration lines in `wire()` (named), zero loop lines.

## interfaces

```go
// models: the per-model row and the root's table.
type Model struct {
    ID         string
    Window     int // context window, tokens
    MaxTokens  int // max output tokens per request
    Reserve    int // tokens held back for the response
    KeepRecent int // token budget for the kept tail
}
func (m Model) Check() error // the row invariants, loud (decision 2)

type Table struct{ ... }
func (t Table) Get(id string) (Model, bool)
func (t Table) Known() []string // stable order, for the refusal voice
func Resolve(t Table, id string, env func(string) (string, bool)) (Model, error)
                                    // root's row resolution (decision 2)
var Defaults Table // the built-in rows (decision 2)

// policy/compact: the policy, the decorator, the marker.
const SummaryMarker = "[compaction] " // the transcript marker (decision 5)

func New(provider core.Provider, fe core.Frontend, s *core.Session,
         system string, m models.Model, opts ...Option) (*policy, error)
// *policy is unexported (lift's style); it implements core.ContextPolicy.
// provider is the inner instance (the loop's, through the decorator);
// fe is the kernel's frontend (the recorder), as the root wires it;
// s is the root's session (k.Session); system is the root's assembled
// system prompt, as the passthrough receives it today.

type policy struct{ ... }
func (p *policy) Assemble(ctx context.Context, s *core.Session) ([]core.Message, error)

func Decorator(inner core.Provider, p *policy) core.Provider // decision 7
type Option func(*options)
func WithAutoReflect(fn func(ctx context.Context, summary string) error) Option
                                       // decision 6; absent = the seam off

// core, additive (the SPEC_CORE diff at the end):
type Compacted struct {
    Summary string // the summary row's content, as the transcript carries it
    Dropped int    // estimated tokens removed from the transcript
    Kept    int    // estimated tokens kept (the tail)
    Usage   Usage  // the summary call's reported usage
}
func (Compacted) event() {}

type Request struct {
    Messages  []Message
    Tools     []ToolSpec
    MaxTokens int // 0 = the provider's default (added)
}
```

## decisions

### 1. Wrap the passthrough; the policy owns when and how

The compact policy contains the passthrough. Below the trigger its
`Assemble` output is deep-equal to `policy.Passthrough(system)` on the same
session — a named test. At the trigger it compacts and returns the
passthrough output on the new transcript:

1. split the transcript into an older prefix and a kept tail (3);
2. summarize the prefix through the provider the policy holds — the same
   `core.Provider` seam, the same inner instance the loop streams through;
   the summary call does not pass through the overflow decorator (7), so
   a fault in it surfaces as an `Assemble` error and never recursively
   compacts;
3. rewrite the session transcript to `[summary message] + tail`;
4. emit `Compacted` (5); fire `AutoReflect` (6);
5. return system + the new transcript.

Why the transcript is rewritten, not just the returned slice: a compact
that only shortens the return value re-summarizes the same older prefix at
every `Assemble` (a fresh provider call per model phase — the pi bug in a
new form), and the recorder and resume see nothing. The rewrite makes the
compaction a fact of the transcript: later assemblies are below the trigger
and passthrough; the recorder's rows (5) are the evidence; resume rebuilds
the compacted shape (5: the re-landing plus the marker restart — the
store is append-only, and the naive projection would rebuild the full
history with the summary after the tail it summarizes). The rewrite
touches `Session.Messages` only;
`Session.Files` is untouched, so drift checks survive compaction.

The policy is impure by design, named: below the trigger pure, as
passthrough; at the trigger one rewrite, one provider call, one event, one
reflection. The compact action is not idempotent — a second compaction is
a fold: the older prefix contains the first summary row and the prompt
names it (3). The reflection, by contrast, is idempotent by content
(6).

Why the loop is untouched: the `ContextPolicy` seam is documented as
"prompt assembly and, later, compaction" (SPEC_CORE) — when and how are
policy questions, and the loop sees an assembled message and a stream. A
loop that knows compaction would own the timing, and the timing is the
model's window, not the loop's state.

### 2. The trigger is per-model; the table is the root's; the reserve is structural

Compact when `calibratedEstimate(system + transcript) > Window - Reserve`.
Strict inequality, a named boundary: a fixture at exactly `Window -
Reserve` is passthrough; one over compacts.

The numbers are the model's, from a root-owned table (the new `models`
package): `Model{ID, Window, MaxTokens, Reserve, KeepRecent}`, a `Table`
with lookup, `Defaults`, and `Resolve` — the root's row resolution at
start, before any store is opened:

- the table row for the active id (`-model` / `RIG_MODEL`);
- else, if `RIG_MODEL_WINDOW` is set: a synthesized row for the active id
  from `RIG_MODEL_WINDOW` / `RIG_MODEL_MAX_TOKENS` / `RIG_MODEL_RESERVE` /
  `RIG_MODEL_KEEP_RECENT`, absent fields falling back to named defaults
  (MaxTokens 8192, Reserve Window/8, KeepRecent Window/4), then validated;
- else a loud refusal naming the id, the table's known ids, and the env:
  `models: no row for "X" (known: local, qwen3.8-workers; set
  RIG_MODEL_WINDOW to define one)`.

`Resolve` re-runs `Check` after synthesis, so an env mistake is loud at
start, not a slow death on the first turn. This is how a new model on the
swap gets a row without a code change (flags and env only, SPEC_CORE).

`Defaults` ships the worker profile under rig's default id and the
scheduler's worker id — `local` and `qwen3.8-workers`, both
Window 65536, MaxTokens 8192, Reserve 8192, KeepRecent 16384 — and the
262k brain row is one table entry (Window 262144, MaxTokens 16384,
Reserve 16384, KeepRecent 32768), added for the deployment's alias or
carried by env.

Row invariants, checked once, loud, naming the id and the fields:

- `Window > 0`, `MaxTokens > 0`;
- `0 <= Reserve < Window` — a reserve as large as the window means the
  trigger fires at every estimate (the pi bug);
- `0 <= KeepRecent < Window - Reserve` — the usable window must leave room
  for the summary beside the tail, or compaction can never help.

These are the structural guarantees, and the named case is the pi bug
seen 2026-08-15: a global reserve larger than the workers' window fires
compaction every turn, including headless `-p`. In rig there is no global
reserve: the reserve is a per-row field, checked against its own row's
window, so one table holds a 64k worker and a 262k brain and each row's
math is self-contained — the trigger fires at the model's own boundary,
never the other model's. A row with `Reserve >= Window` cannot exist
(checked), so the bug's precondition is impossible by construction. The
same check kills the slow-death family: after a compact the context is
system + summary + tail, and `KeepRecent < Window - Reserve` leaves room
for the summary — a job whose window minus reserve leaves too little to
work with is refused at start (a named refusal), not discovered as a
worker that compacts every tick and logs false successes.

Why a package and not a `core` field: the table is configuration, owned by
the root and read by 9's command — not a wire type. `core/` stays
interfaces-plus-wire; `models/` is stdlib only, like `core/`.

Why 9's switch is already provided: the `models` command reads the same
table and switches the active model by rebuilding the policy pair at the
root — `k.Policy` and `k.Provider` are the root's fields to write, and the
loop borrows them per turn (decision 8's wiring). 8 owes 9 nothing but
the table.

### 3. Keep-recent is a budget at a message boundary; the summary call is clamped; one prompt file

Keep-recent is a token budget, not a message count: the largest suffix of
the transcript whose calibrated estimate is within `KeepRecent`, never
empty (at least the last message — the budget is a ceiling, not a floor;
a single oversized last message means the older prefix is empty and the
compact is skipped, the passthrough returned, named).

The cut is at a message boundary and never inside a
tool-call/tool-result pair. The rule in one sentence: the tail's first
message is never a `RoleTool` result. A result whose call sits in the
older prefix would dangle on the wire, so when the budget lands inside a
batch the boundary slides back to the batch's assistant message; a
multi-call batch is atomic, and the tail may exceed the budget by that
batch's estimate — bounded by one batch, named. A dangling result whose
call is not in the transcript (the resume shape, SPEC_HARDENING 5) may
start the tail: there is no pair to keep whole, and the template reads
the shape as legal.

The older prefix is summarized in one provider call:
`[system: the summary prompt, older prefix verbatim]`, no tools. The
summary prompt is one file, not an inline string:
`policy/compact/summary_prompt.txt`, embedded with `go:embed` (stdlib),
reviewed and diffed as one document. Its contract: a compact factual
summary of the work so far — the task and its current state, decisions and
why, files touched and what changed, the tool results that matter (test
outcomes, errors and their fixes), open threads and what was tried and
failed — written in the third person ("the session fixed X and its
tests"), so the marked user row (5) reads as framing, not instruction
(one line in `summary_prompt.txt`). The tail may legally start with a
user message, so the transcript can carry consecutive user rows: fine on
Qwen's template, named. And, when an earlier summary is present, a fold:
keep what is still true, drop what is done — the multi-compaction
boundary: a long session compacts repeatedly, and each
compaction folds the previous summary into the new one; the prompt names
the shape and the test asserts the fold (tests).

The summary request's `max_tokens` is clamped to what the window actually
has left for this request: `min(MaxTokens, Window - est(prompt + older))`
— nothing follows in the summary call, so `Window - est` is the honest
budget, and the reserve is not subtracted twice (the main-call clamp in 8
is the same shape). If the budget is <= 0 — the input alone does not fit
the window (a single oversized message, or a calibration miss) — the
compact fails loud, naming the id, window, and the input's estimate: no
invented chunking in 8 (non-goal), and the overflow decorator (7) is the
recovery if the call still faults. The next call's fit (system + summary
+ tail in the usable window) is a separate bound: the fold (above) and
the recovery (7) carry it, layer by layer.

Why one call and no chunking: a map-reduce summary is a second algorithm
with its own cost and its own failure shapes; the boundary it rescues is
the oversized-message case, which the structural checks (2) and the loud
failure (here) already name. If it is needed, it is a named extension
with its own tests.

### 4. Estimation is stdlib, corrected by the provider's reported usage

The estimate is stdlib: for each message, the bytes of `Content` plus
`Reasoning` plus each tool call's name and args, divided by 4, rounded up.
Named approximate: it is a trigger, not an accounting.

The correction is the provider's last reported usage — the number the
server actually counted rides `Done.Usage`, already in the event
vocabulary (SPEC_HARDENING decision 3): the same wire, no new channel. The policy
carries a calibration factor, 1.0 until the first report: on every
`Done` the decorator relays (the main call's, whose `Prompt` the server
counted), `factor <- clamp(reported / estimated(request), 0.5, 4.0)`, and
every later estimate — trigger, budget, clamps — is the raw estimate times
the factor. The clamp is a guard against a server that reports a total
where a prompt is expected, not a belief about tokenizers.

The wire also carries the tool specs, which the transcript estimate does
not see — so the calibration estimate includes them: the decorator sees
`Request.Tools`, and the denominator is `estimate(messages) +
estimate(specs)` (the specs' names, descriptions, and schema bytes, /4,
same stdlib). The constant then rides both sides of the ratio and
cancels: the factor calibrates the tokenizer, not the tokenizer plus a
constant. Without this, a spec that is dense in tokens but cheap in bytes
inflates the factor on every short call — one number, learned at 10k,
stays 1.5 at 200k, and the brain compacts ~30% early; the clamp
[0.5, 4.0] does not catch it, because the inflation is honest arithmetic
on the wrong denominator. Named bias that remains: the factor is one
number for the whole transcript, so a tokenizer that is dense on JSON and
sparse on prose is read as its average — small next to the uncalibrated
case (bytes/4 off by 2x or more on CJK-heavy or code-heavy transcripts),
and the calibration is what lets one bytes/4 stay honest on a 64k worker
and a 262k brain with different tokenizers, from one shared config
(decision 2's case). The trigger itself does not see the specs (the loop
owns them), so the trigger estimate is spec-free and its error is in the
safe direction: slightly late, recovered by 7, never early and wasted.

Where the calibration lives, named: the decorator is the only place that
sees both sides of a main call — the assembled request and the reported
`Done`. The state (the factor, the compact budget key) sits in the
policy, written by the decorator's relay goroutine and read by `Assemble`
on the loop's goroutine: two goroutines, so a mutex guards the two
fields, and the loop's byte-identity is untouched (the lock is inside a
leaf).

### 5. The Compacted event; one line in the CLI; the summary is a marked row

A new event, additive per the compat rule (SPEC_CORE; events are added,
never changed — the `TestEvent` precedent):

```go
type Compacted struct {
    Summary string // the summary row's content, as the transcript carries it
    Dropped int    // estimated tokens removed from the transcript
    Kept    int    // estimated tokens kept (the tail)
    Usage   Usage  // the summary call's reported usage
}
```

`Dropped` and `Kept` are calibrated estimates in the same units as the
trigger math, so the operator can read them against the window; `Usage` is
the server's own count for the summary call.

Emission is exactly once per compaction, in order, by path:

- the trigger path (`Assemble`): the policy emits it to the frontend it
  holds — the same frontend the kernel gets (the recorder), as the root
  wires it — before returning the messages. The loop has not started the
  model call, so the event lands before the next model call's events
  (named test).
- the overflow path (7): the event rides the stream channel between the
  swallowed fault and the retry's first event, and the loop's existing
  `default` case forwards it untouched (the `TestEvent` compat path — the
  loop is byte-identical).

Both paths deliver it to the recorder, which lands it (below). The CLI
renders one line, pane's `formatTokens` shaping:

```
⧉ compact: -12.4k kept 16.0k · summary ↑812 ↓640
```

One-shot ignores it (its `Notify` switch has no default — the compat
rule), so the worker's stdout stays the answer, byte-identical in shape
(SPEC_HARDENING non-goal kept).

The recorder lands the summary as a message row: `role = "user"`,
`content` = the event's `Summary` verbatim (the marker is in the content),
plus a usage row against that row's seq carrying the event's `Usage`. The
marker is `SummaryMarker = "[compaction] "`, a constant in
`policy/compact`. Grep is the interface: the summary rows are exactly the
user rows that start with the marker.

Why a marked user row, not a new `Role`: the wire has four roles, and a
fifth is a wire-type change every provider, the recorder, and the resume
projection must learn — and the openai adapter passes role strings
through verbatim, so an unknown role is a chat-template gamble against
llama.cpp's templates. A user message with a marker is template-safe
everywhere, rides the existing `messages.role` + `content` columns (no
schema change, SPEC_STATE owes no diff), and survives resume
byte-identically (a user row projects to `RoleUser`). The model reads it
as framing between turns — a legal conversational state — and the marker
makes it self-describing. Rejected, named: `RoleSummary`.

The marker also keeps everything downstream uniform: the estimate counts
it, the split treats the summary row as an ordinary message (a legal
boundary start), and the second compaction folds it (3). One shape, no
special cases.

**Resume after compaction — the marker is the second interface.** The
recorder appends rows and never drops: after a compaction, the store
holds the full history, the original tail rows (recorded before the
compaction, their seq before the summary row's), the summary row, and the
post-compaction rows. A naive seq-order projection (SPEC_STATE's resume)
would rebuild the entire pre-compaction transcript plus a summary that
lands after the tail it summarizes — over the trigger on the first
`Assemble`, re-summarizing what was already summarized. Two schema-free
fixes, both keyed on the marker:

- the recorder, on `Compacted`, lands the summary row and re-lands the
  kept tail after it: the tail's user rows and assistant rows as fresh
  rows (fresh seqs), the assistant calls with their results folded into
  the call rows, as the projection sources them. Duplicates bounded by
  the tail (KeepRecent + one batch, 3); the earlier rows stay in the
  store as the autopsy — the store is append-only, and the duplicates
  are its shape. The re-landing reads the root's session (the recorder
  already holds it): at the `Compacted` moment, `Session.Messages` is
  exactly `[summary] + tail`, and the same loop property as 7's rewrite
  orders the read against the loop's next append.
- `Resume` starts from the last `[compaction]` row when one exists —
  the projection window is the summary row and everything after it; with
  none, the full history as today. The marker is thus the grep interface
  and the projection interface, one contract.

One named constraint from the schema: `tool_calls.id` is the sole primary
key (the provider's call id), so the tail's call rows cannot be
re-inserted under the same id — the re-landed copies carry fresh ids
(recorder-minted suffix on the original; the grep-able prefix is
preserved). The id is opaque to the model and the call/result pair stays
consistent within the copy, so the projected shape is faithful; the
alternative (widening the PK) is a schema change that reopens
SPEC_STATE — rejected here. The `err` and timing columns of re-landed
calls are copied from the original row where present (the projection
reads only id, name, args, result — faithful either way).

### 6. The summary is handed to rem's AutoReflect: pane's session_compact, wired

`store/rem.AutoReflect` already exists and is tested: the summary as a
low-importance reflection (importance 0.2, `kind = "reflection"`), scoped
to the cwd, deduped by content md5, source "session compaction"; blank
summaries inert. The root wires the seam as a function — the policy does
not import `store/rem` (a leaf calling a leaf through a named callback,
the DI seam, not a dependency):

```go
compact.WithAutoReflect(func(ctx context.Context, summary string) error {
    _, err := remstore.AutoReflect(ctx, rdb, cwd, summary)
    return err
})
```

The call is synchronous inside the compact action, after the rewrite and
the event: one short transaction (millisecond-scale), and a `-p` worker's
exit comes only after the turn completes, so the reflection is always
landed before the process dies — a goroutine would race one-shot's exit
for nothing. Fire-and-forget in pane's sense (its hook catches and
swallows: "never crash a session over a memory store"): a store failure
is swallowed and never fails the turn, and the dedup makes a replayed
compaction inert ("already known mN"). Absent option = seam off: the
compact, the event, and the row all happen, the reflection does not — the
tests use this to isolate compaction from the store.

### 7. Overflow recovery: a provider decorator, once, then surfaces

The trigger is estimate-based and the estimate is approximate: the server
can reject a request the estimate thought fit. Recovery: a provider fault
that names context length triggers one compact-and-retry, once, then
surfaces. Never a silent loop.

The mechanism is a provider decorator (`compact.Decorator`), registered
at the root around the shared inner instance:

```go
inner := openai.New(baseURL, model)
pol, err := compact.New(inner, rec, session, fullSystem, row)
// wire():
rig.WithProvider(compact.Decorator(inner, pol)),
rig.WithPolicy(pol),
```

The decorator relays the stream untouched (no buffering — incremental
rendering is preserved) and, on a classifiable fault with budget left:
runs the compact action (1 — rewrite, event, reflection), reassembles,
re-issues the same request shape (same tools) exactly once, and relays
the retry's stream. A non-classifiable fault, or an exhausted budget,
surfaces the fault as-is.

The classification is a wordlist over the fault text, stdlib
`strings` (case-folded), the common phrasings of OpenAI, llama.cpp, and
vLLM: `context length`, `context_length`, `context window`, `maximum
context`, `max context`, `prompt is too long`, `prompt too long`, `too
many tokens`, `exceeds the maximum`. Named in the tests with positives
and a negative (a timeout is not recovered).

The budget is structural, not a counter: the policy keys its last compact
to the transcript length at the time (the baseline is the transcript
length at construction — the root builds the session before the policy, so
a resumed session starts at that baseline: its first recovery is owed only
once the transcript grows past it). A compact is owed only if the
transcript has grown since: a second context-length fault against the same
transcript is no new information (there is nothing to drop) — it surfaces,
the once budget is spent. A new user message grows the transcript and owes
one more recovery. No clock, no per-turn clear to forget, no loop line to clear it
— the loop is byte-identical, and "never a silent loop" falls out of the
key, not of a retry limit that could be raised into one.

The loop property this depends on, named: the loop does not read or write
`Session` while ranging a stream — only after the channel closes (the
post-range append and the execs). The retry's rewrite runs in the relay
goroutine while the loop is ranging, and the ordering is the channel's:
the rewrite happens-before the `Compacted` event and the retry's events,
and the loop's next `session.Append` happens-after the close. A future
loop change that makes the loop peek at the session mid-stream (a
mid-stream steer reading `Files` is the candidate) races the rewrite —
that is where this breaks, and the change must name it. (SPEC_CORE names
it in the loop section; behavior unchanged.)

Why the decorator, and the two candidates rejected, named:

- **A Fault-time hook on the widened middleware seam.** The seam wraps
  `ToolExec` — a model fault never crosses it. Delivering the fault to a
  participant needs a new loop fan-out at the fault path (an L6-style
  line) for a capability the tool-chain seat does not semantically own
  (a participant of the tool chain observing the model's failure).
  Rejected: it spends the loop change this deliverable exists to avoid.
- **The loop's existing fault path (abort the turn, re-prompt).** It works
  with zero plumbing: the fault surfaces, the user re-sends, and the next
  `Assemble` compacts — the trigger is certainly past, since a fault is
  worse than a trigger. It is the floor. Rejected as the only path because
  the spec asks for the retry inside the turn, and kept as the floor: both
  paths converge on the same compact action, so the floor holds even if
  7's mechanism changes.

The named shapes, each in the tests:

- a swallowed fault leaves no fault row — the recorder sees only what is
  surfaced; the autopsy shows the `Compacted` row and, if the retry also
  faults, one fault row;
- a steering interrupt in the retry window (the turn ctx dies during the
  compact or the re-issue) reads as the loop's existing pre-stream seam:
  `Stream` returns an error, the turn ctx is dead, the loop breaks the
  turn as an interrupt, no `Fault` (SPEC_HARDENING pre-stream case —
  unchanged);
- a fault after deltas (a mid-stream rejection, rare: the
  context-length fault is normally a pre-stream 400) leaves the partial
  in the assistant text — the loop accumulates what the stream delivered,
  the retry's text follows, and the partial is not retracted. The shape
  reads as the model starting, the context compacting, and the model
  continuing. Named, not hidden.
- the summary call does not pass through the decorator (1): its faults
  surface as `Assemble` errors, and the loop's existing policy-error path
  handles them (surfaced, turn aborted, session intact).

### 8. Headless: the same wire, the start contract, the request-side reserve

`-p` workers and `run-job` get the same policy and the same numbers: they
go through the same `wire()` — the row is resolved, the decorator is
registered, the `AutoReflect` seam is wired (the job's cwd is the store's
scope, as today). One-shot's silence is unchanged (5): the worker's stdout
stays the answer.

The start contract, loud before any store is opened (the root's
`checkOneShot`-style construction checks, kept): the active row resolves
(2) — a known row, an env synthesis, or a refusal that names the id, the
known ids, and the env. A row that violates the invariants is refused at
construction (`Check`), with the id and the fields in the message. This is
decision 2's slow-death case made a construction error: a job whose window
minus reserve leaves too little to work with fails at start, before the
first tick, not as a worker that compacts every turn and logs false
successes.

The request-side reserve: the decorator also clamps the main call's
`MaxTokens` to the window minus the request: `min(row.MaxTokens, Window
- est(request))`, floor 1. The trigger already guarantees
`est <= Window - Reserve` below it, so `Window - est >= Reserve` — the
clamp hands the response exactly its reserve, and the reserve is not
subtracted twice (the wrong formula, `Window - Reserve - est`, gives a
request sitting just under the trigger `max_tokens` = floor 1: a
one-token answer at the moment the model has a full reserve of room — a
named test). The reserve becomes a request-side guarantee: the response
budget is explicit — at least the reserve, not the server's default —
and the worker is protected both against a server whose default
max_tokens walks into the wall and against one whose default `n_predict`
is small. A truncated answer (a `Done` with finish `length`) is read as
normal — a legal finish, no recovery; the estimate is approximate, the
clamp is best-effort, and the overflow recovery is the safety net.

The loop change, named: none. `Assemble`, the `Provider` seam, the loop's
default event forwarding, and the pre-stream error path all exist; the
decorator is a provider, and the design test counts a new provider as one
file and one registration line. If the implementation needs a loop line,
that reopens 7 (the roadmap's rule) and this spec's 7 is rewritten. That
is the stop condition.

## tests

Named cases, failing first (the standing rule). Scripted providers (a
fake `core.Provider` with scripted streams), fixture transcripts at known
sizes, one shared config carrying both model rows, and a real rem store
in `t.TempDir()` where a case names it.

`models`:

- the row invariants by name: empty id, `Window <= 0`, `MaxTokens <= 0`,
  `Reserve >= Window` (the pi shape), `KeepRecent >= Window - Reserve` —
  each refused, the id and the fields named in the error.
- `Resolve`: a known row; an env synthesis (absent fields take the named
  defaults, then validate); an unknown id with no env — the refusal names
  the known ids and the env.

`policy/compact`:

- `TestBelowTriggerIsPassthroughByteIdentical` — the output deep-equals
  `policy.Passthrough(system)` on the same session, at `est == Window -
  Reserve` (the boundary: no compact) — and at `est == Window - Reserve +
  1` the same fixture compacts (the trigger is strict).
- `TestTriggerMathPerModelOneConfig` — one table, one root: the 64k worker
  row and the 262k brain row; a fixture transcript over the worker's
  trigger and under the brain's — the worker compacts, the brain passes
  through byte-identically. The named pi case (2026-08-15): a
  global-reserve shape (`Reserve >= Window`) cannot exist (2's invariants).
- `TestKeepRecentCutsAtPairBoundary` — a budget landing inside a
  multi-call batch slides to the batch's assistant message (the tail never
  starts at a result); the single-call pair is atomic; the overrun is
  bounded by one batch; a tail of the last message alone (single oversized
  last message) — the older prefix is empty, the compact is skipped, the
  passthrough is returned.
- `TestSummaryMaxTokensClamped` — the scripted provider captures the
  summary request: `MaxTokens == min(row.MaxTokens, Window - est(input))`
  (3's honest budget — the reserve not subtracted twice); a budget <= 0
  fails loud, naming the row's numbers.
- `TestOverflowRecoversOnce` — a pre-stream context-length fault
  (wordlist phrasing), then a healthy stream: the frontend's order is
  `Compacted, TextDelta*, Done`; the first `Fault` never surfaces; the
  second request's messages equal system + `[summary row]` + tail; the
  session transcript is rewritten; `AutoReflect` is called.
- `TestOverflowRecoversOnceThenSurfaces` — two classifiable faults: the
  second surfaces (the `Fault` reaches the frontend, the fault row lands);
  a third call never happens; after a new user message (the transcript
  grew) one more recovery is owed and happens.
- `TestOverflowClassifier` — the wordlist's positives are recovered; a
  timeout and a non-context fault are not.
- `TestCompactedEventBeforeTheNextCall` — trigger path: the frontend sees
  the `Compacted` before the next model call's events; the fields are
  right (the `Summary` equals the transcript's summary content;
  `Dropped`/`Kept` are the calibrated estimates; the `Usage` is the
  summary call's reported usage).
- `TestCalibrationShiftsTheTrigger` — a scripted `Done` reporting 2x the
  estimate: the next trigger decision uses the corrected estimate (a
  transcript under the raw trigger compacts; the inverse, named); a
  reported ratio outside `[0.5, 4.0]` is clamped; a call carrying a large
  tool spec does not inflate the factor beyond the tokenizer ratio — the
  denominator includes the spec (4): a factor learned at 10k with a 5k
  spec stays ~1.0 at 200k, not 1.5.
- `TestMainCallMaxTokensClamped` — the pass-through stream's request
  carries the clamped `MaxTokens` (the request-side reserve, 8); a
  request just under the trigger (`est == Window - Reserve`) gets
  `MaxTokens == Reserve`, not the floor 1 (the wrong-formula case).
- `TestSecondCompactionFoldsTheFirst` — a long scripted session: the
  second compact's older prefix contains the first summary row; the
  transcript after equals `[new summary] + tail2`; the reflection dedupes
  (a new body lands a new memory, a replayed body is inert).
- `TestAutoReflectLandsDedupesAndNeverFails` — a real rem store: the
  memory row (kind reflection, importance 0.2, cwd scope, source "session
  compaction"); a store failure (a closed db) leaves the `Assemble`
  successful; the absent seam skips the call and changes nothing else.
- `TestSteerDuringRetry` — the turn ctx dies in the retry window: the
  decorator returns an error (not a `Fault`), and the loop reads it as the
  existing pre-stream interrupt path.
- `TestRecoveryRewriteRaceFree` — a full `loop.Run` through the decorator
  (first call faults with context length, second succeeds), run under
  `-race` (the gate): the rewrite and the loop's append are ordered by
  the channel (7's named loop property), and the transcript shape holds.
- `TestLoopForwardsCompactedUntouched` — the loop's compat: a `Compacted`
  between stream events forwards untouched (the `TestEvent` precedent),
  and the existing loop cases stay byte-identical.

`store/state`:

- `TestRecorderLandsCompactedSummary` — the `Compacted` event lands a
  message row (`role = "user"`, the content verbatim with the marker) plus
  a usage row (the event's `Usage`), seq before the assistant row the next
  `Done` lands.
- `TestRecorderRelandsTheKeptTail` — the `Compacted` case re-lands the
  kept tail after the summary row (5): fresh seqs, the assistant calls
  with fresh ids (the `tool_calls.id` PK), name/args/result verbatim, the
  original rows intact as the autopsy, the duplicates bounded by the
  tail.
- `TestResumeAfterCompactionRebuildsTheCompactedShape` — a session that
  compacted: the store holds the full history, the original tail rows,
  the summary, the re-landed tail, and the post-compaction rows; `Resume`
  rebuilds exactly `[summary] + tail + post-compaction` (call/result pairs
  consistent under the fresh ids), not the full history; a session that
  never compacted projects the full history, as today.

`frontend`:

- the CLI renders the one line in the exact format (5) with
  `formatTokens`; one-shot ignores `Compacted` (no stdout, no fault
  flag).

`cmd/rig` (e2e, scripted `httptest` provider):

- a `-p` run that crosses the trigger mid-turn (a large prompt that fits
  the window, then a large tool result that pushes the transcript past it;
  the next model call faults, the recovery compacts the older prefix, the
  retry succeeds): the stdout is the final assistant text only; the state
  store carries the summary row + usage row + a session closed ok; exit 0.
  (The single-oversized-prompt shape — nothing to drop — surfaces, the
  named boundary of 3; the e2e is the recoverable case.)
- the row-resolution refusal (an unknown model id, no env) is loud before
  the stores open, naming the known ids.

## the SPEC_CORE diff this spec implies

PR A carries this spec file only; the diff below lands with PR B.

- `core/provider.go`: the sealed event vocabulary gains `Compacted`
  (additive, compat rule — a Frontend must tolerate it, the default is to
  ignore it); `Request` gains `MaxTokens int` (0 = the provider's default;
  additive — a provider that does not know it ignores it).
- The streaming events section: `Compacted`, named as the third emitter
  category after providers and the loop (a policy event, emitted at
  `Assemble` or on the stream by the decorator), and the loop forwarding
  it through its existing default — byte-identical.
- The loop section: the named property the decorator depends on (7) — the
  loop does not touch `Session` while ranging a stream; a documented
  invariant, zero behavior change.
- The Provider section: `Request.MaxTokens` semantics.
- The ContextPolicy section: compaction landed; the policy may rewrite the
  session transcript (the one named mutation the seam carries; the
  passthrough stays pure); the loop is untouched.
- The testing section: the named cases above.
- `provider/openai`: the `max_tokens` wire mapping (present when non-zero,
  absent when 0 — the wire-shape test asserts both).
- SPEC_HARDENING owes no diff: its scope already says compaction consumes
  nothing new here but the provider seam — true, the decorator is a
  provider.
- SPEC_STATE owes one diff (named): the recorder's `Compacted` case
  re-lands the kept tail after the summary row (5), and the `Resume`
  projection starts from the last `[compaction]` row when one exists (5).
  No schema change: the marker is the contract, and the rows are
  existing shapes.

## scope

What 8 is not:

- Deliverable 9's verbs attach here, named: `models` reads the `models`
  table and switches by rebuilding the policy pair at the root (2);
  `compact` forces the compact action — a small exported seam on the
  policy (`Compact(ctx) (core.Compacted, bool, error)` over the same
  internal action), added then, no shape change owed now.
- Deliverable 10's footer renders `Compacted` — the context-used bar the
  event already carries (`Dropped`/`Kept` against the window).
- Chunked summarization (3's rejection), a tokenizer dependency (the
  non-goal), and parallel tool execution (a loop change, and a different
  deliverable).

The loop at the end of 8 is the loop of the end of 7, byte-identical. 9
and 10 inherit it, per the roadmap: if one of them needs a loop change, 7
was incomplete and is reopened first.
