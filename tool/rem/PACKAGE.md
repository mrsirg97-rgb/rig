# tool/rem

## What it is

Adapts `store/rem` to the loop's tool surface: runtime shape checks loud
at execute; session attribution from the threaded ctx (`memories.source`
defaults to the calling session id, `anon` when unthreaded, and accepts
free text when the caller passes one); replies exactly as the store shapes
them. The description carries the contract sentence (SPEC_STATE: rem is
deliberate) — every rem operation is a choice, nothing is written by a
compaction, nothing is read into the prompt by a session start.

## What it includes

- `Tool` — a `core.Tool` over the rem store's read/write operations
  (`learn`, `recall`, `reflect`, `prune`), each with an optional
  `project` (a path, resolved through `store/scope`, worktree-safe).

## How it is consumed

- Registered at the root as a native tool. The `/rem` command
  (SPEC_COMMANDS 11) is the operator's verb surface over the same store;
  the tool stays the model's multi-line surface.
- The compaction `AutoReflect` seam is cut: compaction writes nothing to
  rem (SPEC_COMPACT 6).

## Gotchas

- Session attribution comes from the threaded `core.SessionFrom`; an
  unthreaded call attributes `anon`.
- Replies are the store's shapes, verbatim — the adapter does not
  re-voice.
- The deliberate project (SPEC_STATE): when `project` is set, its path
  replaces the session cwd as the `cwd` handed to the store — the scope
  is the repo the fact belongs to, not the directory rig started in.
  `~` expands at the `middleware/paths` boundary (the `project` field is
  in its `Fields`); `project` + `scope: global` refuses by name.
