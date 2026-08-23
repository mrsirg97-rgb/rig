# store/scope

## What it is

The shared scope identity: what a project is, and how a directory maps
to it. A queue's or memory's identity partition is the repo, not the
directory rig happened to start in, and the identity is never a
filename — it is the short sha1 of the git common dir (the
`--git-common-dir` probe, memoized), falling back to the cwd hash
outside a repo. Two worktrees of one repo share a scope; a renamed
directory keeps its identity; a subdirectory reads the repo's queue.

## What it includes

- `scope.go` — `ShortHash`, `Path` (the memoized git probe with the
  relative-output resolution and the echoed-option fallback), `Key`
  (`ShortHash(Path)`), and `Label` (the display name: `filepath.Base`,
  `"."`/`""` → `root`).

## How it is consumed

- `store/rem` and `store/todo` resolve their scopes through it; the rem
  and todo tools resolve an explicit `project` path through it too.

## Gotchas

- The probe result is memoized per-cwd; the `PATH` it runs under is the
  caller's, so a test can point it at a fake `git`.
- A relative common-dir output is resolved against the cwd; an output
  that starts with `-` or contains a newline is an echoed option, not a
  path, and the scope falls back to the cwd.
