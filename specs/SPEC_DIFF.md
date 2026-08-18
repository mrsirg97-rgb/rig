# tool/diff: the observation diff

One leaf package, one named read arm in `store/state`, one line at the
root. The tool is a read path over state the harness already records: the
loop's recorder already lands every tool call and its result as a row
(SPEC_STATE), and `diff last` reads the two most recent rows of the same
call and shows what changed between them. `diff files` is git being
honest, not rig reinventing it. This spec refuses to add a snapshot
store: the transcript is the snapshot.

The gripe it answers, verbatim, from the model that felt it:

> no native ‘run this and diff against my previous observation'
> primitive — I get by with git and explicit state, but a
> workspace-level before/after would kill a whole class of 'did my
> change actually apply' drift.

## goals

- One tool, `diff`, two verbs (`files`, `last`) behind the Tool seam,
  registered once at the root.
- `last`: the previous result of the same tool call vs the newest, from
  this session's recorded tool calls; nothing new is written.
- `files`: the working tree against HEAD (or `ref`), via `git diff`, and
  the tool's description says so.
- The reply is a unified diff (context 3, ANSI-free, capped, loud
  marker), or the word `identical`, or `no earlier observation`.

## decisions

### 1. THE TOOL

`diff`, one tool behind the Tool seam (`core.Tool`: Name, Description,
Schema, Exec), two verbs in one schema, selected by a required `mode`
(enum `files` | `last`):

- `files`: the working tree against HEAD (or `ref`, optional). The tool
  shells out to `git diff --no-color -U3` in the session's cwd (the
  process's cwd; the tool takes no cwd argument) and says so in its
  description. A non-git cwd refuses loud, naming the reason. Optional
  `paths` restricts the diff. This is git being honest, not rig
  reinventing it.
- `last`: the previous result of the same tool call vs the newest. Args
  are `tool` (the name) and `args` (the exact args JSON, matched by
  canonical equality after normalization: sorted keys, no whitespace;
  decision 3). The two most recent `tool_calls` rows in the CURRENT
  session with that (name, canonical args) pair; the unified diff of
  result_old -> result_new, or the named replies `no earlier
  observation` / `identical`. Optional `n` for the Nth-previous (the
  decision 2 cap applies either way). Reads name, args, result,
  started_at from store/state's `tool_calls`; nothing new is written.

Rejected, named:

- Two tools (`diff_files`, `diff_last`): two wire names for one verb
  family; the description split duplicates the schema, and the model
  learns two names for one question.
- A loop primitive ("diff the last result" as a loop verb): the loop is
  frozen, and this is a read over state the loop already records.
- A command door for the operator (a `diff ...` line): the model's
  verb; the operator already has git. Named later if wanted; not this
  spec.
- A `git diff --no-index` engine under `last`: the engine is Go
  (decision 2); `last` should not need git on the box at all.

### 2. THE OUTPUT

- Both verbs reply with a unified diff, context 3, ANSI-free.
- The engine is Go, stdlib only, a pure function (two strings in, a
  diff string out), no shell-out under `last`; the layout is git's:
  `--- <old>` / `+++ <new>` / `@@ -a,b +c,d @@` hunks.
- The `last` reply is one header line, then the body. The header names
  the two observations (started_at, message_seq): the model sees which
  calls were compared, not just what changed.
- The body is capped at N lines, N = 100, a named constant; the house
  rule holds (SPEC_TUI 8): rig truncates only tool output, and does so
  loudly. Over the cap: the first N-1 lines are
  kept and the tail is the marker `… K more lines`, K the count of
  elided lines. Head kept: the first hunk is where the "did my change
  apply" answer lives; the model re-asks with `n` if it wants the end.
- An empty diff is the word `identical`, not an empty string (both
  verbs).
- `files`: the body is git's own output (its `--- a/...` / `+++ b/...`
  headers), under the same cap; an empty body is `identical`.

Rejected, named:

- Shelling out to `diff -u` or `git diff --no-index` for the engine:
  the exit-code footgun (git: 1 = files differ), the temp-file
  lifecycle, a second external voice to learn. The `files` verb already
  carries the shell-out contract; `last` does not need git.
- A third-party diff library: `go.mod` is unchanged; a dependency is
  attack surface not written here.
- An LCS DP table: O(n*m) memory over output the cap discards most of
  the time.
- ANSI in the reply: the model reads bytes; the TUI preview already
  styles what it shows (decision 6).
- A tail-keeping cap: the head is where the first hunk names the
  change; the tail is what the model re-asks for.

### 3. CANONICALIZATION

The args-equality rule, stated once: two args JSONs are the same
observation iff they decode to the same JSON value. Key order and
whitespace do not matter; values do. The canonical form is the decoded
value re-encoded with object keys sorted and no whitespace (Go's
`json.Marshal` of the decoded value; array element order is preserved).
`1` and `"1"` are different values; `{"a":1}` and `{"a":null}` are
different; `{"a":1}` and `{}` are different.

One function owns the rule: `state.CanonicalArgs` in `store/state`,
the home of the args column. The recorder applies it at write time
(`RecordToolCall`; the one write-path change this spec makes to the
state store), and the diff tool applies it at query time. Consequence:
the `args` column holds the canonical form, the store query's `args = ?`
equality is exact, and the `LIMIT 2` in the interfaces below is exact:
the rows returned are, by construction, the two most recent
observations of that call.

A `bash` call with the same command IS the same observation; that is
the point.

Edge named: an args string that fails to decode at write time (it
should not; the provider sends JSON) lands raw, and the recorder says
so loud: the row always lands (the autopsy is total), and the row is
simply not findable by a canonical query.

Rejected, named:

- Raw string equality: whitespace and key order are model noise, and
  the model will retype a call's args with both.
- A wider `LIMIT` plus a Go-side canonical scan: the scan window is
  arbitrary, and interleaved calls of the same tool with different args
  push the pair out of the window.
- JSON normalization in SQL: modernc's json1 cannot re-encode with
  sorted keys, and the query becomes a monster the store owns.

### 4. SESSION SCOPE

`last` looks within the current session only: the query is scoped by
`messages.session_id` to the session ID the loop threads
(`core.WithSession`, recovered with `core.SessionFrom`). A resumed
session is the same session: `state.Resume` projects under the same ID
and the recorder re-attaches to it, so the rows before the resume are
in scope.

Cross-session is REJECTED, named: observations from another session are
a different world. The same (tool, args) in another session never
enters this one's query.

Rejected, named:

- Cross-session lookback ("the last observation of this call,
  anywhere"): the model's context is per-session, and mixed worlds are
  how "did my change apply" becomes "did some other world's call
  apply".
- A silent global scan when the ctx has no session: a loud refusal
  beats an answer the caller did not ask for.

### 5. NON-GOALS, NAMED

- No snapshot store: the tool is a read path over state the harness
  already records. A snapshot store is a second transcript to keep in
  sync with the first, and the sync is the bug factory.
- No "before" verb: capture-before, apply, compare is two tools
  pretending to be one. The observation is what the call returned;
  there is no capture to add.
- No watch: no tailing, no polling, no subscription. One call, one
  reply.
- No diff of arbitrary strings: that is `python`; this tool names its
  inputs by tool + args, not by pasting blobs into the wire.
- No diffing across compaction: the `[compaction]` marker is a message
  row, not a `tool_calls` row, and the tail compaction re-lands keeps
  name/args/result verbatim under fresh seqs (SPEC_STATE), so those
  rows diff as ordinary rows. The rows survive compaction, the store
  keeps them; only the model's context forgot. The tool is exactly the
  memory.

### 6. THE TUI

The result renders through the existing tool block
(`RenderToolBlock`'s default path): the accent-dot opening, the
head-6/tail-2 preview, the success/error outcome glyph and the plain
duration. Decision success/error is the one allowed decoration; a diff
is already its own color.

If the block renderer needs a per-tool hook, name it as an additive
renderer registration, not a special case. For this tool that is at
most one entry in the existing `toolDetail` per-tool table (a
`case "diff"` line showing the verb): additive to a table that already
degrades to name-only for unknown tools, not a new branch in
`RenderToolBlock` (the `todo`/`scheduler` special case stays as is).

Rejected, named:

- A diff-specific shared renderer in tools_render.go: it parses the
  reply back into a structure and re-derives the diff; the
  parse-failure degrade (raw text) is the worst of both for a reply
  that is already plain.
- A per-tool hook in the loop: a loop change against the freeze.
- A new theme slot for diffs: retro is texture, never information
  (SPEC_TUI 8).

### 7. PLACEMENT

```
tool/diff/
  diff.go         the tool surface: both verbs, the canonical call, the
                  git shell-out (files only), the diff engine, the cap;
                  stdlib only
  diff_test.go    the named cases (testing, PR B)
store/state/      CanonicalArgs (the one function, decision 3),
                  RecordToolCall applying it (the one write-path
                  change), RecentToolCalls (the one named read arm,
                  beside ListSessions and mintSeq)
cmd/rig/main.go   the tools map gains "diff": diff.New(sdb); the
                  WithTools list gains r.tools["diff"]
config/settings.json  the embedded allow-list default grows by diff,
                  and the pinned default-list test grows with it
                  (SPEC_PYTHON's pattern)
cmd/rig/testdata/golden_020/   the wire fixtures (oneshot, repl,
                  runjob) regenerate with the diff entry
frontend/tui/     at most the one toolDetail entry (decision 6)
```

- Registered at the root, once, at the seam: `diff.New(sdb)` takes the
  state DB the way `todo.New(tdb)` does; the session comes from ctx,
  so the tool takes no session argument.
- The freeze holds: `core/` and `loop/` byte-identical; no
  middleware, no new loop events, no command.
- The tool description text is part of the wire and is pinned by a
  golden: the `golden_020` fixtures carry the request bodies
  byte-exact (description + schema), and PR B regenerates them with
  the diff entry. A description change is a wire change, and the
  goldens change with it.
- `go.mod` is unchanged: stdlib only. git is an environment
  dependency, named in the description, and of the `files` verb only.
- No loop, no middleware, no new event, no new store file. The state
  store gains one read arm, one function, and one write-path line that
  applies it.

Rejected, named:

- A middleware that "remembers the last call": the loop already
  records it; a middleware would be a second recorder writing what the
  first already wrote.
- A package under `store/` for the diff: a read is an arm of the store
  that owns the transcript, not a store of its own.
- A loop hook for observation pairing: the pairing is (name, canonical
  args, session), and all three are already in the row.

## interfaces

### the wire (description and schema, verbatim)

Description:

```text
diff a tool call's result against its previous observation, or the working tree against HEAD.

mode 'files': the tool shells out to `git diff` and says so: the working tree against HEAD (or ref, optional), optional paths; a non-git cwd refuses loud, naming the reason.

mode 'last': the recorded tool calls of this session only (a resumed session is the same session; another session is another world): the newest result of the same call (tool name + exact args; key order and whitespace do not matter, values do) against its n-th previous (n optional, default 1). a read path over state the harness already recorded; nothing new is written.

the reply is a unified diff (context 3, ANSI-free, capped, '… K more lines'), or the word 'identical', or 'no earlier observation'.

Guidelines: 'did my change actually apply' -> last, with the tool and args of the call that made the change; tree against HEAD -> files; diff of arbitrary strings -> python, not this.
```

Schema:

```json
{
  "type": "object",
  "required": ["mode"],
  "properties": {
    "mode": {
      "type": "string",
      "enum": ["files", "last"],
      "description": "files: the working tree against HEAD (git diff); last: the previous observation of the same tool call"
    },
    "ref": {
      "type": "string",
      "description": "files only: the ref to diff against (default HEAD)"
    },
    "paths": {
      "type": "array",
      "items": {"type": "string"},
      "description": "files only: restrict the diff to these paths"
    },
    "tool": {
      "type": "string",
      "description": "last only: the tool name of the observed call"
    },
    "args": {
      "type": "object",
      "description": "last only: the exact args of that call; matched by canonical equality (key order and whitespace do not matter, values do)"
    },
    "n": {
      "type": "integer",
      "minimum": 1,
      "description": "last only: the n-th previous observation (default 1)"
    }
  }
}
```

### the store (Go surface and query, verbatim)

```go
// store/state: the one canonical function (decision 3), applied at
// write time by RecordToolCall and at query time by the diff tool.
func CanonicalArgs(args string) (string, error)

// store/state: the one named read arm (beside ListSessions and
// mintSeq): the last verb's pair read. Returns the n+1 most recent
// completed calls of (name, canonical args) in sessionID, newest
// first; fewer than n+1 means no earlier observation. Completed means
// result IS NOT NULL: the in-flight call's result has not landed.
type Observation struct {
	Result    string
	StartedAt time.Time // RFC3339 UTC, as stored
	Seq       int64     // the row's message_seq
}
func RecentToolCalls(ctx context.Context, db store.DB, sessionID, name, args string, n int) ([]Observation, error)

// tool/diff: the tool surface, registered at the root (decision 7).
func New(db store.DB) core.Tool
```

The query (the n=1 base, verbatim; `n` generalizes to `LIMIT n + 1`,
with `old` the last row and `new` the first):

```sql
SELECT tc."result", tc."started_at", tc."message_seq"
FROM "tool_calls" tc
JOIN "messages" m ON m."seq" = tc."message_seq"
WHERE m."session_id" = ?
  AND tc."name" = ?
  AND tc."args" = ?
  AND tc."result" IS NOT NULL
ORDER BY tc."started_at" DESC, tc."message_seq" DESC, tc."id" DESC
LIMIT 2
```

The order is total: started_at is second precision, message_seq breaks
ties across messages, id breaks ties within one (a multi-call message
in a single second).

### the reply strings (verbatim)

Success:

- `identical` (both verbs; the empty diff)
- `no earlier observation` (`last`; zero or one matching row)
- the header, then the body:

```text
diff last bash {"command":"ls"} · old 2026-08-16T09:58:12Z seq 14 · new 2026-08-16T10:02:31Z seq 57

--- 2026-08-16T09:58:12Z
+++ 2026-08-16T10:02:31Z
@@ -1,3 +1,3 @@
 context line
-old line
+new line
```

  The header's args are the canonical form of the query's args
  (decision 3), so the reply is reproducible byte-for-byte.
- the cap tail: `… K more lines` (K = the elided count)

Refusals (loud, naming the reason):

- `diff files: not a git repository (cwd /path/to/cwd)`
- `diff files: <git's first stderr line, trimmed>` (other git
  failures, e.g. `diff files: fatal: ambiguous argument 'v9'`)
- `diff last: no session in context (the loop threads one)`
- `diff: mode required (files|last)`
- `diff last: tool and args required`
- `diff last: n must be >= 1`

## testing

Failing test first, per decision; the cases named by name, in PR
order.

### PR A: the store arms (store/state)

- canonical form ignores key order and whitespace, and values matter:
  a property over key permutations and re-spacing of one JSON (all
  equal), and over one value changed (unequal)
- `1` vs `"1"` is different; `{"a":1}` vs `{"a":null}` is different;
  `{"a":1}` vs `{}` is different
- RecordToolCall stores the canonical form: `{"b":1, "a":2}` lands as
  `{"a":2,"b":1}`
- an args string that fails to decode lands raw, and the recorder says
  so loud (the row always lands)
- RecentToolCalls returns the n+1 most recent completed rows, newest
  first, for (session, name, canonical args)
- a row whose result has not landed (result NULL) is invisible
- a row in another session with the same (name, args) is invisible
- interleaving: other tools' calls, and the same tool with different
  args, between the pair do not displace it (the LIMIT-2 exactness
  that makes decision 3's write-time canonicalization load-bearing)

### PR B: the tool (tool/diff)

files:

- a clean tree replies `identical`
- a dirty tree's body is capped at 100 lines, the `… K more lines`
  marker exact, K counting the elided lines
- a non-git cwd refuses loud, naming the reason (the cwd in the voice)
- a ref is honored (the diff is against the named ref, not HEAD)
- paths are honored (the diff is restricted to them)
- a git failure (an unknown ref) passes the stderr line through,
  prefixed

last:

- two calls with the same (tool, args) diff newest against previous,
  and the header names both observations
- an identical pair replies `identical`
- exactly one matching call replies `no earlier observation`
- zero matching calls replies `no earlier observation`
- n=2 picks the second-previous row; an n beyond what is available
  replies `no earlier observation`
- key order and whitespace in the query args do not matter; the
  gripe's case named: a `bash` call with the same command IS the same
  observation
- a value changed (`ls` vs `ls -la`) is a different observation: no
  match
- a failed call (err set, result set) still participates
- no session in ctx is a loud refusal, not a global scan
- a re-landed tail after compaction (fresh seqs, verbatim
  name/args/result) diffs as an ordinary row
- the engine's layout is git's: a cross-check case diffs a fixture
  pair and compares the body against `git diff`'s (the test may shell
  out; the runtime may not)

the wire:

- the `golden_020` fixtures (oneshot, repl, runjob) carry the diff
  entry, description and schema byte-exact; a one-byte description
  change fails the golden

the TUI (only if decision 6's table entry is taken):

- the diff block's opening shows the verb; the rest of the block is
  the default path (preview, outcome, duration), unchanged

## scope

One leaf package (one file + its tests), one named read arm plus one
canonical function plus one write-path line in `store/state`, one
tools-map entry and one WithTools name at the root, one allow-list name
in the embedded config, the regenerated wire goldens, and at most one
line in the TUI's detail table. `core/` and `loop/` byte-identical;
`go.mod` unchanged; git is the one environment dependency, named in
the description. PR A is the store, shippable alone; PR B is the tool,
registered and pinned.
