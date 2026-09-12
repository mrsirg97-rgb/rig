# pathguard

## What it is

The one cwd-containment rule: canonicalize a working directory and refuse
one outside the caller's project or the rig home. `tool/delegate`,
`tool/scheduler` (create and update), and the `store/scheduler` runner's
fire-time revalidation all use it, so the rule cannot drift between the
tools and the jail.

## What it includes

- `pathguard.go`: `Canonical` (absolute, must exist and be a directory,
  symlinks resolved) and `Within` (`Canonical` plus containment under the
  session cwd or the rig home, checked canonically).

## How it is consumed

- `tool/delegate` and `tool/scheduler` call `Within` at the boundary; the
  `store/scheduler` runner calls `Canonical` at fire time and requires the
  stored cwd to still resolve to itself (a replaced, moved, or deleted
  cwd skips the fire).
- The errors carry no tool prefix: the callers wrap with their own name.

## Gotchas

- `Within` refuses an empty root: the caller must supply both roots.
- The containment is canonical: the session cwd and the path may disagree
  about symlink form — a resolved path under a symlinked cwd, or the
  reverse — and both forms accept; a symlink escape still resolves
  outside and refuses.
- The containment is rechecked at the runner's fire because the jail
  rw-binds the cwd: the create-time check is not the fire-time path.
- A file is not a cwd: `Canonical` refuses one (the delegate's old copy
  accepted it and failed at spawn).
