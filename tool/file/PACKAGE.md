# tool/file

## What it is

The read, write, and edit tools. Edit is exact-match string replacement
with loud, specific failure messages; provenance from the threaded
session makes edit-after-external-change fail loudly instead of
clobbering. Read gains `offset`/`limit` line arguments (SPEC_HARDENING
decision 9): a narrower read exists to reach for when a capped result's
"re-read a narrower range" is the teaching. Read's `diff: true` appends
the file's git diff against HEAD (`tool/diff`'s `Files`), or
`no changes` when clean, so the model sees what it is about to edit. A
read that finds a stale observation (SPEC_CORE: "a read that finds a
stale observation names it") prepends `[changed since your observation]`
before the content, so the model is told its prior read is stale before
it acts on it.

## What it includes

- `read`, `write`, `edit`: a `core.Tool` each, over `os`/`path/filepath`.
- The edit license, narrowed to the drift check: edit takes `path` and
  `edits`, a list of `{old, new}` applied in order; a single change is a
  list of one, and the top-level `old`/`new` are gone from the schema.
  Every hunk is validated against the content as the earlier hunks leave
  it, each matching exactly once, before anything writes — all or none;
  a later hunk may match text an earlier one created. An edit of a path
  with no recorded `FileState` applies when every hunk matches exactly
  once (an exact-once match cannot come from a model that never saw the
  bytes); on any miss the reply is the file's text exactly as a read
  returns it — the same cap and truncation marker, taught once — ending
  with `[edit: <path> was not read this session; its text is above, now
  edit it]`, and the reply records the observation, so the edit that
  follows is drift-checked like any other. A file the session has read
  refuses by name: the first missing hunk, its match count, and what it
  found. The bounds stand ahead of any I/O: at most 32 hunks, total old
  plus new under read's ceiling, no zero-width old. `read` or `write`
  still mints the license, an external change
  invalidates it, and a standalone exec carries no session and so no
  license to check.
- The stale-observation note on read: compared against the recorded
  `FileState` *before* the read re-records it, so an external or
  cross-session change is named once and the fresh bytes still ride it.
- read's `offset`/`limit`: select a 0-based line range: `offset` past the
  end and a negative `offset`/`limit` refuse loud, naming the line count.
- read's `diff`: append the git diff of the read file (HEAD vs working
  tree), `no changes` when clean; a non-git workspace refuses loud, naming the
  reason, like the deleted `diff` tool's `files` verb did.
- read streams the file once: every byte is hashed for provenance while
  only the requested window is captured, capped at one byte past the
  output cap, so a huge file is never materialised through a read. The
  returned bytes follow the split-join contract exactly (the
  trailing-newline line count included).
- read's cap marker: a capped reply is cut at a line boundary when one
  exists inside the cap, so the reply ends at a complete line, and the
  marker carries the next step — `[output truncated: N of M lines;
  continue at offset X]`, X the offset the next read needs (offset+N,
  N the complete lines in the reply and M the file's line count, the
  same "lines" the offset and the past-the-end refusal use) — so a
  model reading a big file in ranges walks it exactly. A single line
  longer than the cap falls back to a rune boundary, so no rune is ever
  split, and gets its own marker — `[output truncated: line N is longer
  than the 1 MiB cap; slice it with bash]` — because a read can never
  make progress on it: bash owns that line.
- `normalizePath`: canonicalizes at the boundary so `a.go` and `./a.go`
  are the same key (without it the drift check can be silently bypassed by
  path spelling).
- `recordState` / `stateOf`: the `FileState` provenance maintained on the
  threaded session; `SnapshotFiles` returns a copy under the package
  lock, so a persister that upserts the states cannot race the tools'
  concurrent records.

## How it is consumed

- Registered at the root as native tools: `middleware/perm`'s provenance
  rule mirrors `normalizePath` so the rule's path test and the tool agree
  on the same file.
- `edit` reads `core.SessionFrom` to keep `FileState` per path.

## Gotchas

- `normalizePath` canonicalizes (absolute + clean): a symlinked path that
  bypasses the spelling still keys on the canonical string.
- The teaching reply is read's bytes by construction (`readWindow` plus
  `capReply`), never a second renderer; the zero-width `old` refusal and
  the missing-file error stand ahead of it, because both are about the
  call, not the observation.
- The drift check refuses when the file's hash or mtime differs from the
  recorded `FileState`; edit-after-external-change never silently
  clobbers.
- The hunks are applied by a pure function over the file's bytes
  (`applyHunks`): no I/O, no session, and the miss report (hunk index,
  match count) comes out of it, so the imperative shell only reads,
  validates, and writes.
- The remembered bytes a drift refusal diffs against live in a bounded
  cache keyed by session ID and path (16 MiB, FIFO-evicted): two
  sessions sharing one process each diff against their own observation,
  never each other's, and no session or file's bytes are pinned beyond
  the cap (an evicted observation degrades to the header-only refusal).
- The cache never holds a session object, only its ID: a finished
  session's transcript is released with it.
- A standalone exec (no threaded session) skips provenance maintenance.
- `Session.Files` is written under a package mutex (`filesMu`): reads in
  one batch run concurrently (SPEC_EVT 2a) and the session type is
  frozen, so the tool that writes it is the one that locks.
