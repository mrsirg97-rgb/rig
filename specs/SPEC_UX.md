# rig: the field-test round (the first user's findings)

The first sustained user of the harness that was not its author filed
a report: the agent that builds rig, on its experience working inside
it. The verdict was the design philosophy holds ("closed, typed seams,
boring, loud — I disappeared into the style"); the findings are five
frictions, each small, each real, each named below with the reporter's
own words as provenance. This spec wraps them into one round. No new
surfaces: every fix lands on an existing seam, `core/` and `loop/`
byte-identical.

The findings, verbatim (condensed):

> todo's create is a full replacement: two sessions in the same cwd
> can silently overwrite each other's queues. I almost did this (I
> created my queue and wiped the 43 old tasks).

> rem underused: recall has to be cheaper than re-derivation. I
> re-derived the repo's location, the ssh key quirk, the spec voice,
> the commit style — each a "learn" candidate. Cheap fix: a standing
> recall at session start.

> bash's cwd surprise: a pwd line in the failure message would have
> saved the 2 wasted calls.

> edit's drift after a bash sed: defensible, but a message that names
> the diff would save the re-read.

> the TUI's Enter-accepts: I typed /models and Enter — it filled in
> "deepriver" instead of dispatching. As a first user, the one moment
> I fought the input model rather than reading it.

## goals

- todo's replacement made loud and guarded (1): the reply counts what
  it replaced; a queue holding another session's in-progress tasks
  refuses replacement; `add` appends without replacing.
- rem recalled at session start (2): the cwd's recent notes ride the
  prompt, so recall costs zero calls and the memory becomes
  load-bearing.
- bash's failures name the cwd (3): one line, only on failure.
- edit's drift refusal names the drift (4): the diff tool's own engine
  shows what changed, capped.
- Enter dispatches on intent (5): a complete command line dispatches;
  the menu's accept requires navigation first.

## non-goals

- No per-session todo queues, no queue namespacing: the shared per-cwd
  queue is the point (the human and the agents read one plan). The fix
  is loudness and a guard, not partition.
- No rem auto-learn beyond what exists (AutoReflect at compaction
  stays the write path): the round fixes the read path. A session-end
  reflection is named as a later candidate, not built.
- No TUI one-shot mode (`--tui --once`): the reporter self-rejected it
  as scope creep; recorded here as a future idea, not a demand.
- No changes to the untouched surfaces (scheduler, python, web): the
  report notes they carried none of the round's work — "either the
  core loop is well-scoped or those surfaces need a pull I don't feel
  yet." Watched, not acted on.

## decisions

### 1. todo: WITHDRAWN on review (the finding stands, the fixes do not)

The review's live smoke discovered the premise false: `create` never
replaced. The fold upserts by text (main and the branch alike;
verified on main: two creates, the reply says "queue replaced with 1
tasks" while the queue grows to 2) — the tool's description and reply
have claimed replacement since the store shipped, and the field
report's "I wiped the 43 old tasks" was the claim believed: nothing
was wiped. The reporter's footgun was the reply lying in the safe
direction.

The operator's call: revert this decision's fixes whole — the count
and the guard decorated drops that cannot happen, and `add` duplicated
what `create` truthfully already does (upsert one task by text). The
behavior was fine; the voice was the bug, and the voice fix is a later
one-liner if lived use asks ("plan upserted", not "queue replaced").
The event-sourced fold cannot change retroactively regardless (replay
compatibility); the one genuinely destructive verb remains the empty
create, unguarded as before, named here for the day it bites.

The original decision, kept for the record:

#### 1 (as specced, not implemented): the replacement counted, guarded, add

`create` keeps its semantics — the full-replacement plan is the
planning gesture, and changing it under the model's feet breaks the
tool's own description — but it stops being silent or unguarded:

- The reply counts the loss: `queue replaced: 5 tasks (dropped 43: 40
  open, 3 in progress)`. The count is the confirmation the reporter
  wanted; "queue replaced" alone hid the 43.
- The guard: a replacement that would drop another session's
  in-progress tasks refuses loud, naming them and the owner:
  `todo: create would drop 3 in-progress tasks owned by session
  1a01ad… (t7, t12, t19): finish or fail them first, or add instead`.
  The ownership vocabulary already exists (complete refuses foreign
  claims); the guard is the same rule at the replacement door. Own
  in-progress tasks do not refuse (replacing your own plan mid-flight
  is a choice, counted in the reply).
- `add` (new verb, both doors — the tool's action and the command's
  Sub()): append one task to the queue, no replacement semantics at
  all. The parallel-session-safe verb.

Rejected, named: create-as-merge (silently unioning two plans makes a
plan nobody wrote); per-session queues with a shared view (partition
kills the one-plan property that makes the queue worth reading).

### 2. rem: the recall at session start

The session-start prompt assembly (SPEC_CONFIG 6, SPEC_MODES 2's
ordering) gains one optional segment, after AGENTS.md, before the
guidelines:

```
remembered (this directory):
- <the cwd's most recent K rem notes, one line each>
```

- K = 8, capped at 1500 characters total (oldest trimmed first); the
  cwd's notes only (rem is already cwd-scoped); absent notes = absent
  segment (today's bytes exactly).
- Recall is a store read at session start and the refresh points
  (new, resume), never per turn. The segment rides the prefix: its
  cost is prefix tokens, named — 1500 chars is the cap because the
  memory must stay cheaper than what it saves.
- The model's own habit fix rides the description: rem's tool
  description gains one line — "your notes from this directory are in
  your context at session start; learn what the next session should
  not re-derive."

Rejected, named: a recall tool call at session start (a turn spent on
what the prompt can carry); injecting all rems (the cap is the point);
a global recall (the cwd is the scope rem already chose).

### 3. bash: the failing reply names the cwd

A nonzero exit's reply gains one trailing line: `(cwd /home/ng)`. A
success stays byte-identical (the piped goldens hold). One line, one
condition; the two wasted calls the reporter counted were both "where
am I" probes after a path error.

### 4. edit: the drift refusal names the drift

The refusal today says the file changed since the read. Amended: it
appends the unified diff of the remembered read against the current
bytes — the diff tool's own engine (`tool/diff`'s Diff, a pure
function, already in the tree), capped at 20 lines with the loud
elision marker. The re-read the reporter paid becomes unnecessary
exactly when the drift is small (a bash sed), and the cap keeps a
rewritten file from flooding the reply.

Rejected, named: naming the drift's author ("external: bash sed") —
the tool cannot know who changed the bytes, and guessing is a lie
with a confident voice.

### 5. the menu: Enter dispatches on intent (SPEC_TUI 9 amended)

The reporter's collision: `/models` + Enter accepted the menu's first
candidate (deepriver) instead of dispatching the complete command.
The fix is the navigation-intent rule:

- Enter ACCEPTS the selection only when the operator has navigated
  the menu this input (Tab, Shift-Tab, or an arrow moved the
  selection). Navigation is the intent to pick.
- Enter with no navigation DISPATCHES when the typed line is a
  complete command (with or without arguments) — `/models` + Enter
  lists the models; `/todo add x` + Enter runs it — and otherwise
  keeps today's behavior (the ghost's Enter completes-then-submits,
  SPEC_TUI 9; an incomplete prefix with a menu open and no navigation
  dispatches the unknown-command refusal it always meant).
- The menu's hint row (the `… N more` tail's row, or a dim trailing
  segment) names the rule: `tab/↓ pick · enter runs`.

This amends the pinned "Enter accepts, never dispatches" — the safe
first cut, revisited with field data: the first user fought it, and
the navigation-intent rule keeps the safety (no accidental dispatch
of a half-picked candidate: picking requires navigation, and
navigation disarms dispatch) while making the common case (type the
whole command, hit Enter) do the obvious thing.

## interfaces

The voices, verbatim:

```text
queue replaced: 5 tasks (dropped 43: 40 open, 3 in progress)
todo: create would drop 3 in-progress tasks owned by session 1a01ad…
(t7, t12, t19): finish or fail them first, or add instead
→ t44 added: wire the guard
(cwd /home/ng)
edit: the file changed since the read:
--- as read
+++ on disk
@@ -3,2 +3,2 @@
 …
… 12 more lines
remembered (this directory):
- the repo lives in ~/Projects/rig; push with GIT_SSH_COMMAND=…
```

## testing

- todo: the replacement reply's counts (open and in-progress split);
  the foreign-in-progress refusal naming tasks and owner; own
  in-progress replaced without refusal, counted; add appends and
  never drops; both doors byte-equal (decision 6's rule).
- rem: the segment present with notes and absent without (the golden
  holds); the K and character caps; the refresh points re-read; the
  cwd scoping (another directory's notes never ride).
- bash: the failure carries the cwd line; the success is
  byte-identical to the golden.
- edit: the refusal carries the capped diff; a one-line drift shows
  whole; a rewrite elides loudly; the diff engine is the shared one
  (no second differ).
- the menu: /models + Enter with no navigation dispatches; Tab then
  Enter accepts; arrow then Enter accepts; the ghost's
  complete-then-submit unchanged; the hint row names the rule.

## scope

`tool/todo` + `store/todo` (the count, the guard, add), the root's
prompt assembly (the rem segment), `tool/bash` (one line),
`tool/file` (the drift diff, importing `tool/diff`'s engine),
`frontend/tui` (the navigation-intent rule), `command/` (add's Sub()
row). `core/` and `loop/` byte-identical; no new dependencies. One
round, one branch (ux), spec-first as always; the sandbox rounds
follow it per the roadmap.
