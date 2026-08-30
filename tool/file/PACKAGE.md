# tool/file

## What it is

The read, write, and edit tools. Edit is exact-match string replacement
with loud, specific failure messages; provenance from the threaded
session makes edit-after-external-change fail loudly instead of
clobbering. Read gains `offset`/`limit` line arguments (SPEC_HARDENING
decision 9): a narrower read exists to reach for when a capped result's
"re-read a narrower range" is the teaching. A read that finds a stale
observation (SPEC_CORE: "a read that finds a stale observation names it")
prepends `[changed since your observation]` before the content, so the
model is told its prior read is stale before it acts on it.

## What it includes

- `read`, `write`, `edit`: a `core.Tool` each, over `os`/`path/filepath`.
- The stale-observation note on read: compared against the recorded
  `FileState` *before* the read re-records it, so an external or
  cross-session change is named once and the fresh bytes still ride it.
- read's `offset`/`limit`: select a 0-based line range: `offset` past the
  end and a negative `offset`/`limit` refuse loud, naming the line count.
- `normalizePath`: canonicalizes at the boundary so `a.go` and `./a.go`
  are the same key (without it the drift check can be silently bypassed by
  path spelling).
- `recordState` / `stateOf`: the `FileState` provenance maintained on the
  threaded session.

## How it is consumed

- Registered at the root as native tools: `middleware/perm`'s provenance
  rule mirrors `normalizePath` so the rule's path test and the tool agree
  on the same file.
- `edit` reads `core.SessionFrom` to keep `FileState` per path.

## Gotchas

- `normalizePath` canonicalizes (absolute + clean): a symlinked path that
  bypasses the spelling still keys on the canonical string.
- The drift check refuses when the file's hash or mtime differs from the
  recorded `FileState`; edit-after-external-change never silently
  clobbers.
- A standalone exec (no threaded session) skips provenance maintenance.
- `Session.Files` is written under a package mutex (`filesMu`): reads in
  one batch run concurrently (SPEC_EVT 2a) and the session type is
  frozen, so the tool that writes it is the one that locks.
