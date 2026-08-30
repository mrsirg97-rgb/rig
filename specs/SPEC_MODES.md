# rig: the modes (effort and role)

Two session-scoped dials on the interactive brain: `/effort` moves the
model's reasoning budget between its own available levels, and `/role`
puts a stance; architect, reviewer; between the system prompt and
the operator's AGENTS.md. Both are runtime state at the root (the
models-switch machinery), both take effect next turn, neither persists
past the session, and neither touches a worker. `core/` and `loop/`
byte-identical: the effort rides a provider decorator (the
`compact.Decorator` precedent), the role rides the prompt assembly the
root already owns.

The baseline: today the main turn's request carries no effort at all
(the loop builds `core.Request{Messages, Tools}`; the row's `effort`
is the compaction summary call's alone, SPEC_CONFIG 4), and the system
prompt assembles once in `wire()` as system + AGENTS.md + guidelines
(SPEC_CONFIG 6).

## goals

- `/effort`: show the active level and the model's available ones;
  `/effort <level>` sets it for subsequent turns. The vocabulary is
  the row's, not a global list: models differ (one speaks
  low/medium/xhigh, another only max).
- `/role`: show the active role: `/role <name>` sets it:
  `default` (no injection, today's prompt exactly), `architect`,
  `reviewer`. The role's prose sits between the system prompt and
  AGENTS.md: the runtime's identity first, the stance second, the
  operator's contract third, the participants' prose last.
- Next-turn, both: the models-switch semantics (SPEC_COMMANDS 4). A
  live turn's request is already built; the change is visible on the
  next one. Neither refuses mid-turn; there is nothing to race.
- The costs said out loud (below): both dials move the request prefix,
  so both can cost a re-prefill at depth.
- The menu teaches both: `Sub()` hints carry the levels and the role
  names with one-line descriptions (SPEC_TUI 9).

## non-goals

- No persistence: effort and role are session state at the root, like
  the active model between switches. A resume does not restore them
  (the session store records messages, not dials); a `/new` starts at
  the defaults. Named, revisitable if lived use votes.
- No per-turn syntax (no `/effort high: do the thing`): a dial, not a
  prefix grammar.
- No worker reach: `run-job` workers take the job's model row as
  today. A job-level effort or role is scheduler-create surface,
  later, if wanted.
- No custom roles in v1: the vocabulary is the shipped three. A
  `~/.rig/roles/*.md` user layer is the obvious extension and is
  deliberately not built until the shipped stances prove the shape.
- No effort on the compaction summary call from this dial: that call
  keeps the row's `effort` (SPEC_CONFIG 4); the summary's budget is
  the model's configuration, not the session's mood.
- No new events, no loop line, no core line.

## decisions

### 1. The effort dial: the row's vocabulary, a provider decorator

`models.Model` gains one optional field:

```
"efforts": ["low", "medium", "xhigh"]
```

- the model's available levels, in the model's own vocabulary and
order (the template's words, not a rig-invented scale; llama.cpp's
chat templates read arbitrary strings and each model family names its
own). Absent = the dial is off for that row: `/effort` says so and
names the key. The row's existing `effort` (the summary call's) is
also the row's DEFAULT level (amended, the operator's field report):
with the dial unset the decorator stamps it onto the main turn's
request; the wire carries the level explicitly instead of leaning on
the template's silent default, and the status row's label (the same
dial-else-row fallback) is never a guess. The server never reports
the applied level back, so what rig stamps is the only truth rig can
show. A row without `effort` is today's bytes exactly, and the
summary call is untouched either way (it sets the same row value
itself).

The active effort is root state, default empty (the row's default
behavior; the empty-row case is today's bytes exactly, the 0.2.0
invariant's spirit). The
plumbing is a decorator, zero loop lines:

```go
// effort.Decorator wraps the provider: Stream stamps the session's
// effort onto a request that has none. The compaction summary call
// sets its own (the row's) and is untouched. The root rebuilds the
// pair on a switch (SPEC_COMMANDS 4), so next-turn is by construction.
```

The wire is the measured door (SPEC_COMPACT: llama.cpp ignores the
top-level field; the template reads `chat_template_kwargs`): the
provider already sends both when `ReasoningEffort` is set. Nothing new
below the decorator.

The cost, named: on the swap's templates the effort switches a
template branch, which can rewrite the rendered prefix; a level
change can cost one full re-prefill at depth. A dial, not a tic.

Rejected, named:

- A loop change (the request gains the effort at the source): the
  loop is frozen, and the decorator is the pattern the freeze exists
  to force.
- A global effort vocabulary (low<medium<high<xhigh as rig's own
  scale): the words belong to the model's template; inventing a scale
  means mapping tables and the mapping is where the bugs live.
- Cycling on bare `/effort`: a state-changing no-argument command is
  a misfire magnet; bare shows, the argument sets, the menu completes.

Amended (the operator's review): the dial across a model switch. The
vocabulary is the row's, so a level the new row's `efforts` does not
name is reset on the switch; loudly, as the switch's note (`effort:
"xhigh" is not a level for deepseek; reset to server default`,
appended to the `/models` reply); never stamped silently into a
template that cannot speak it (the day-one eval lesson: an invalid
effort string manufactures failures without a sound). A level the new
row names rides the switch untouched, no note. The `SwitchModel` seam
returns the note beside the error.

### 2. The role: a stance between the system prompt and the contract

The prompt assembly (SPEC_CONFIG 6) gains one optional segment:

```
system prompt        the runtime's identity (what rig is)
[role prose]         the session's stance (how to lean, this session)
AGENTS.md            the operator's contract (how Nick builds)
guidelines           the participants' operational prose
```

Descending proximity holds: the stance colors the identity but never
outranks the contract; AGENTS.md reads after it and wins conflicts
by position and by its own rules. `default` injects nothing: the
assembled prompt is byte-identical to today's.

The role's prose is embedded, verbatim (the interfaces section), in
rig's voice, written for a small model: short, imperative, no
personality cosplay; a stance is a bias in what to do first and what
to refuse, not a costume.

A switch recomputes the assembly and rebuilds the pair at the root
(the models-switch machinery, next-turn). The status (SPEC_TUI 3,
amended twice by the operator's review) is three rows under the
input: identity; `model · used/window` (the context part once a turn
has run); the stance; `effort · role · auto|manual`; the active
effort in its ramp color (pane's footer colors, the `effort*` theme
slots; a level outside the ramp paints accent; a row naming none
drops the segment), the role abbreviated (`architect -> arch`,
`reviewer -> rev`, the default shown as `default`), and the approval
dial (decision 4; manual in the warn color; the paused posture reads
at a glance), and the usage totals. The effort shown is the session's
truth: the dial when set, else the row's configured level. The cost,
named: the role sits near the prefix's head, so a switch is a full
re-prefill at depth, always.

Rejected, named:

- The stance inside AGENTS.md (an operator-side convention): the
  point is a dial the operator flips per session without editing the
  contract; the contract stays stable, the stance moves.
- Injecting as a user message ("act as reviewer…"): message rows are
  transcript; they compact, they scroll, the model argues with them.
  The stance is prompt, not conversation.
- A persona library, temperaments, personas-as-files: two working
  stances first; the shape earns extension by use (the non-goal).

### 3. The commands

`/effort`; bare: `effort: <active or "server default"> (available:
low, medium, xhigh)`, with an argument: sets, replies `effort: xhigh
(next turn)`. An argument outside the row's list refuses naming the
list; a row without `efforts` refuses naming the key. `Sub()` carries
the levels, each described (`low; the quick pass`, in the row's
order; the descriptions are generic since the words are the model's).

`/role`; bare: `role: architect` (or `role: default`), with a name:
sets, replies `role: reviewer (next turn)`. Unknown name refuses
naming the three. `Sub()` carries the three with one-liners.

Rejected, named: `/role update <name>` (the sketched verb); the
models switch is `/models m2`, not `/models update m2`; the bare
argument is the house grammar, one word shorter, and the menu makes
it discoverable. Flagged for the operator's review since the sketch
named `update` explicitly.

### 4. The approval dial: manual asks, auto is today (AMENDED in)

The operator's third dial: `/approve auto` (today's behavior) or
`/approve manual`; every mutating tool call pauses for the
operator's y/n before it runs. Effective at the very next tool call:
the gate reads the dial at call time, and nothing rides the request
prefix; no re-prefill cost, unlike the other two dials.

The pieces, each holding a frozen surface untouched:

- **The gate** is a `ToolMiddleware` (`middleware/approve`, the perm
  chain's precedent), wired at the root with three closures; the
  dial, the ask door, and the mutating predicate. Zero core lines,
  zero loop lines. It lists after the router (first-listed is
  innermost) so the allow-list and the provenance rule are consulted
  first: the operator is only ever asked about a call that would
  actually run.
- **The ask door** is the frontend's, offered as an optional
  interface (`Ask(ctx, prompt) bool`) the root type-asserts; the
  Frontend seam in core/ is untouched. The TUI offers it: the
  question stands where the menu would (the screen's one modal,
  warn-colored, the argument preview truncated to a glance), y runs,
  n declines, Esc declines and interrupts the turn, ^C declines and
  quits; every other key is swallowed while the question stands. The
  one-shot and the plain CLI offer no door.
- **The mutating set**: the natives that change the world outside
  rig's own stores; bash, write, edit, python, scheduler,
  plugins, and every plugin (arbitrary python is mutating by
  nature). The read set (read, ls, find, grep, the web pair) and the
  store tools (todo, rem, diff) pass silently: manual is a gate, not
  a turnstile.
- **A denial teaches, never kills**: the declined call returns a
  model-visible refusal ("the operator declined bash; do not retry
  the same call; adjust, or ask what they want"), the turn continues.
  A dead context while asking (the interrupt) resolves as a decline.
- **The dial's home**: session state like the other two, but its
  default is the operator's standing choice; settings.json
  `approve` ("auto" | "manual", embedded default auto). `/new`
  resets to the settings default, not to a hardcoded auto; a resume
  keeps the current value. The asymmetry vs. effort and role, named:
  an approval posture outlives a session's mood.
- **Workers never ask**: the -p one-shot (run-job's runtime) wires no
  door, so the gate is not wired at all; the jail is the worker's
  gate (SPEC_SANDBOX), this one is the interactive session's.
  `/approve manual` on a doorless frontend refuses, naming the
  reason.

Rejected, named:

- An Ask method on `core.Frontend`: the frozen surface: the optional
  interface is the same power without the modification.
- A per-call allowlist grammar ("always allow bash"): the memory of
  approvals is a policy store, later, if lived use votes; v1 is the
  dial and the door.
- A timeout on the ask: the operator's pause is the point: a gate
  that answers itself is auto with extra steps.

## interfaces

The embedded role prose, verbatim:

`architect`:

```text
This session's stance: architect. Design before implementation, every
time. For any non-trivial ask: name the interfaces and contracts
first, list the decisions with what you rejected and why, and get the
shape agreed before writing code. Prefer the smallest structure that
holds; name what you are deliberately not building. If asked to just
implement, propose the design in three sentences, then build.
```

`reviewer`:

```text
This session's stance: reviewer. You are reviewing, not building. Hunt
defects: verify every claim against the actual code, run what can be
run, and name findings precisely (file, line, severity, the failing
scenario). Do not fix anything unless asked - report, propose, wait.
Distrust green tests you have not read. Praise at most once, and only
for something specific.
```

The command replies and refusals, verbatim:

```text
effort: xhigh (available: low, medium, xhigh)
effort: server default (available: low, medium, xhigh)
effort: xhigh (next turn)
effort: "turbo" is not a level for huihui3.8 (available: low, medium, xhigh)
effort: huihui3.8 names no levels (models.json: "efforts")
role: default
role: reviewer (next turn)
role: "pirate" is not a role (default, architect, reviewer)
```

## testing

- the decorator: a request with no effort gains the session's: the
  summary call's own effort survives untouched; empty session effort
  = today's request bytes (the golden holds with the dial unset).
- the assembly: default is byte-identical to today (the golden): a
  role sits between system and AGENTS.md, exactly; a switch rebuilds
  next-turn (the scripted provider sees the old prompt on the live
  turn, the new on the next).
- the commands: bare shows, the argument sets, the refusals carry the
  pinned voices; Sub() hints for both; the status row shows the
  non-default role and drops it on default.
- the scope: a run-job worker's request carries no session effort and
  no role (the fixture worker's wire is unchanged).
- `/new` resets both: a resume does not restore them (named).

## scope

One new leaf (`policy/effort` or `provider/effort`; the decorator,
one file), the root's two state fields and re-assembly, `command/`'s
two commands with Sub(), `models`' one optional field, the status
row's role segment, the embedded prose. `core/` and `loop/`
byte-identical; no new deps; the goldens hold with both dials at
their defaults. PR A is this file; PR B implements, after the
sandbox rounds per the roadmap's sequence.
