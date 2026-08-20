# tool/file

## What it is

The read, write, and edit tools. Edit is exact-match string replacement
with loud, specific failure messages; provenance from the threaded
session makes edit-after-external-change fail loudly instead of
clobbering.

## What it includes

- `read`, `write`, `edit` — a `core.Tool` each, over `os`/`path/filepath`.
- `normalizePath` — canonicalizes at the boundary so `a.go` and `./a.go`
  are the same key (without it the drift check can be silently bypassed by
  path spelling).
- `recordState` / `stateOf` — the `FileState` provenance maintained on the
  threaded session.

## How it is consumed

- Registered at the root as native tools; `middleware/perm`'s provenance
  rule mirrors `normalizePath` so the rule's path test and the tool agree
  on the same file.
- `edit` reads `core.SessionFrom` to keep `FileState` per path.

## Gotchas

- `normalizePath` canonicalizes (absolute + clean); a symlinked path that
  bypasses the spelling still keys on the canonical string.
- The drift check refuses when the file's hash or mtime differs from the
  recorded `FileState` — edit-after-external-change never silently
  clobbers.
- A standalone exec (no threaded session) skips provenance maintenance.
