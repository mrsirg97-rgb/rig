# store/scope

## What it is

The shared scope identity: what a project is, and how a directory maps
to it. A queue's or memory's identity partition is the repo, not the
directory rig happened to start in, and the identity is never a
filename; it is the short sha1 of the git common dir (the
`--git-common-dir` probe, symlinks resolved, memoized), falling back
to the cwd hash outside a repo. Resolving matters because git prints
the common dir relative from the main worktree and as a realpath from
a linked one; without it a symlinked cwd splits one repo into two keys. Two worktrees of one repo share a scope; a renamed
directory keeps its identity; a subdirectory reads the repo's queue.

Beside the repo identity stands the one reserved word: `Global` is the
fixed global scope, never a hash of a path. `Path` never git-probes it
(a probe would resolve it against the process cwd — the guess this
package exists to kill), `Key` returns it verbatim, and the cwd-hash
fallback stays for a real directory that is not a repo.

## What it includes

- `scope.go`: `Global` (the reserved word and fixed key), `ShortHash`,
  `Path` (the memoized git probe with the relative-output resolution
  and the echoed-option fallback; the reserved word rides through),
  `Key` (`Global` verbatim, else `ShortHash(Path)`), `Label` (the
  display name: `filepath.Base`, `"."`/`""` → `root`), `InRepo`
  (whether a directory resolved to a repo at all, so a caller can say
  "this is a bucket, not a project" instead of implying it), and
  `Bare` (the `--is-bare-repository` probe: a bare layout's common dir
  is the cwd itself, so the path alone cannot tell it from a plain
  directory).

## How it is consumed

- `store/rem` and `store/todo` resolve their scopes through it: the rem
  and todo tools resolve the required `scope` parameter — the reserved
  word or a directory path — through it too.

## Gotchas

- The probe result is memoized per-cwd: the `PATH` it runs under is the
  caller's, so a test can point it at a fake `git`.
- A relative common-dir output is resolved against the cwd: an output
  that starts with `-` or contains a newline is an echoed option, not a
  path, and the scope falls back to the cwd.
