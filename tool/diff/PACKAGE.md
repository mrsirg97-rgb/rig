# tool/diff

## What it is

The diff engine, no tool surface: the pure Go `Diff` and the `git diff`
`Files`, the two things read and edit use. Read's `diff: true` appends
the file's git diff against the working tree (or `no changes` when
clean); edit's drift refusal diffs the remembered observation against
the on-disk bytes through `Diff`. The `diff` native tool is gone (the
wire's observation vocabulary folded into read and edit).

## What it includes

- `Diff(old, new, oldLabel, newLabel)`: two strings in, a unified diff
  out in git's layout (context 3), the empty string when identical. A
  plain LCS table (`dp[i][j]` is the LCS of the suffixes), O(N*M),
  walked back into the edit script.
- `Files(ctx, ref, paths)`: the working tree against HEAD (or `ref`;
  an empty `ref` is HEAD), via
  `git diff --no-color --no-ext-diff -U3` in the process's cwd, capped
  at 100 lines with the loud elision marker; `no changes` on a clean
  tree, a loud refusal on a non-git cwd or a git failure. The diff is
  HEAD vs the working tree, never the index: a staged edit still shows.

## How it is consumed

- `tool/file` imports the package: read appends `Files`, the drift
  refusal diffs through `Diff`.

## Gotchas

- O(N*M) is deliberate: the inputs are file contents and tool results
  (KBs) and the reply is capped, so the table is bounded by the bound
  the cap already imposes.
- `Files` is the session cwd's git: a file outside the repository
  refuses like any other git failure. `--no-ext-diff` is deliberate: a
  repository's `diff.external` is not a program rig should run for the
  model's chosen path.
