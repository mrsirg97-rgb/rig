# rig: the streamlined contract and the self-healing door

The tool contracts carry semantics the responses already teach on
contact, and the plugin door asks the model for a reload step the
machine can take for itself. This spec splits the standing context from
the on-contact teaching, and removes the step.

## goals

- The standing context (a tool's `Description`, the system prompt's
  guidelines) carries shape: what the tool is, its verbs, its arguments.
  The responses carry semantics: the state, the broken rule named, the
  facts that hold right now. That is the codebase's existing voice
  (SPEC_SANDBOX's "the refusal teaches", SPEC_STATE's lean read); the
  descriptions stop double-teaching it.
- The `plugin` door (SPEC_GROWTH 9) is total over discovery: a name the
  model has reason to believe exists is callable without a preceding
  `plugins_reload` call. The authoring dance is: write to the pending
  zone, the operator approves, the model calls it through the door.
- The two new response facts are named: the todo store names the
  compaction when its own call caused it, and the unknown-id refusal
  names the minting.

## non-goals

- No argument-surface change: every schema's properties and required
  sets are untouched.
- No change to discovery, the pending zone, the provenance rule, or the
  approve flow (SPEC_PLUGINS, SPEC_SANDBOX stand).
- No new tool, no new command, no new middleware; `loop/` and `core/`
  stay byte-frozen against the branch's base (the freeze gate, as
  usual).
- The rem, todo, and scheduler reply bodies are untouched beyond the two
  named facts.

## decisions

### 1. The contract split

A tool's `Description` is shape: the one line of what it is, the verbs,
the arguments, and one line saying the reply is the contract. A fact
that matters only when a call trips on it belongs in the response: the
error names the broken rule (the existing teaching voice), and a
state-dependent fact appears in the reply exactly when it becomes true.

The three dense contracts are trimmed to the shape:

- `tool/todo`'s description drops the state machine, the claim rules,
  the auto-start, the compaction sentence, and the batching sentence:
  each already rides an error or an echo the model reads on contact
  (`is claimed by X; fail it first to take over`, `auto-started and
  completed`, `waits on tN`).
- `tool/scheduler`'s description and guidelines de-duplicate: the cron
  shape, the once-line self-delete, the busy semantics, the id minting,
  the scope list, and the drift note each appear once.
- `tool/rem` drops the search-engine internals (fuzzy/semantic) and
  keeps the verbs, the scope rule, the k cap, and the session-start
  note.

`bash`, `read`, `write`, `edit`, `ls`, `find`, `grep` are already at
shape and stay.

### 2. The compaction fact

`store/todo`'s `maybeCompact` fires silently on the operation that
crosses the 1000-event threshold and folds the history into a snapshot.
The reply of the operation that caused it now carries the footer

    · log compacted (N events folded into the snapshot)

N is the folded count as the operation saw it. The footer rides every
reply that ran the compaction (create, start, complete, fail, retry,
move); `read` does not compact and never shows it. Below the threshold
the replies are byte-identical to today's.

The footer explains the one observable consequence the model could
otherwise mistake for state loss: the stale footer (SPEC_STATE's lean
read) goes quiet after a compaction, because every row's updated-seq is
the snapshot's seq and reads as fresh.

### 3. The minted-id voice

The todo store's unknown-id refusal, at every verb
(start/complete/fail/retry/move), is

    no task 'tN' (ids are minted by the tool; copy from a reply)

The `no task 'tN'` prefix stays, so the existing containment
assertions hold; the parenthetical is the teaching, on contact.

### 4. The door's self-heal

`plugin` and `plugin_schema` take a `redo` seam at construction:

```go
type Door struct {
    Live Live
    redo func(ctx context.Context) error
}

func NewDoor(live Live, redo func(ctx context.Context) error) *Door
```

`Live` is unchanged (`PluginNames`, `Tool`). On an unknown name the
door runs `redo` once (the root's existing reload: List, Discover,
Check, the atomic swap), re-resolves, and if the name is still absent
refuses with the live list; a failed `redo` is named in the refusal
(`re-discovery failed: ...`). `redo` runs at most once per call and
never on a known name. A nil `redo` is today's behavior: the refusal
with the live list, no discovery.

The root wires `redo` to the error half of `reloadPlugins`, and only
when it has a plugins home (`pluginsHome` non-empty); no home, no redo.
`middleware/toolset` is untouched: the callback rides the door, not the
table.

The door's description gains one line naming the self-heal, so the
model knows an out-of-band install (the operator dropped a file, no
reload) is callable without a reload call.

Named costs: an unknown-name call costs one full discovery (one
directory read, one kernel cell over the files, the swap). The retry
bound (SPEC_PLUGINS' guard) still caps a model that keeps calling a
name that is not there. A `redo` that fails on a kernel error names the
failure instead of pretending the table is the truth.

### 5. The authoring dance loses the reload step

`/plugins create`'s template ends with the operator's approve and the
door's call, not `plugins_reload`:

    author a plugin: %s; the contract is DESCRIPTION, SCHEMA,
    run(args) -> str; write it SELF-CONTAINED to the pending
    directory (SPEC_SANDBOX); the operator installs it with
    /plugins approve; then call it through the plugin door and test
    it with one call.

`plugins_reload` stays a native, in the allow-list default: it is the
operator's explicit verb (removal detection, the collision report), not
the authoring flow's.

## testing

Named cases, failing first, in the leaves that carry them.

**store/todo:**

- `TestCompactNamesTheSnapshotInTheReply` — seed past the threshold
  with real operations; the next operation's reply carries the compact
  footer with the folded count, the event table folds to the snapshot,
  and the tasks with their statuses are intact; the following operation,
  below the threshold, carries no footer.
- `TestUnknownIdNamesMinting` — each verb's unknown-id refusal
  (start, complete, fail, retry, move) carries the minted-id voice.

**plugins (fake kernel and fake Live, no python):**

- `TestDoorRedisoversOnceOnUnknownName` — Live misses the name at
  first, `redo` installs it, the call runs with the args verbatim, and
  `redo` ran exactly once.
- `TestDoorSkipsRedoOnKnownName` — a known name never runs `redo`.
- `TestDoorNamesRedoFailure` — a failing `redo` refuses with the
  re-discovery failure named.
- `TestDoorNilRedoKeepsTheRefusal` — a nil `redo` refuses an unknown
  name with the live list, the voice from SPEC_GROWTH 9.
- The schema door carries the same cases under one test.

**cmd/rig (the root over the real loop, the fake kernel at the DI
seam, SPEC_PLUGINS 8's harness):**

- `TestDoorSelfHealsAnOutOfBandInstall` — a plugin file lands in
  the home after the wire and no `plugins_reload` is called; the model
  calls `plugin {name}` directly; the door re-discovers, executes, and
  the result rides back verbatim; the following request carries the
  name in the door's enum.

**Goldens (cmd/rig):**

- The three pinned request bodies carry the trimmed descriptions and
  the door's self-heal line; the fixtures are regenerated in place, the
  SPEC_PLUGINS 8 precedent.

## scope

- `specs/SPEC_STREAMLINE.md` (this file).
- `store/todo`: the footer and the voice; `maybeCompact` returns the
  note; the five operation closures append it.
- `plugins/door.go`: the `redo` seam on both doors; `command/plugins.go`
  template; the root's wiring.
- `tool/todo`, `tool/scheduler`, `tool/rem`: the trimmed descriptions;
  `tool/scheduler`'s guidelines de-duplicated.
- `cmd/rig/testdata/golden_020`: regenerated in place.
- `PACKAGE.md`: store/todo, plugins, command, tool/todo,
  tool/scheduler, tool/rem.
- `CHANGELOG.md`: the 0.9.2 entry.
- `loop/` and `core/` frozen; the middleware set unchanged; the native
  set unchanged (17).
