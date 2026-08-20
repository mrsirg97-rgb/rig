# tool/diff

## What it is

The observation diff (SPEC_DIFF): one leaf tool, two verbs.
`files`: the working tree against HEAD (or ref), via `git diff` — the
shell-out is the files verb's contract (decision 1). `last`: the previous
observation of the same tool call (this session only) from the state
store — a read path over the transcript. The diff itself is the pure Go
engine (`Diff`), stdlib only.

## What it includes

- `Tool` — a `core.Tool` with the `files` and `last` verbs.
- `engine.go` — the pure `Diff`: two strings in, a unified diff out in
  git's layout (context 3), the empty string when identical. A plain LCS
  table (`dp[i][j]` is the LCS of the suffixes), O(N*M), walked back into
  the edit script.

## How it is consumed

- Registered at the root as a native tool; the `files` verb shells out to
  `git diff`, the `last` verb reads the previous observation from the
  store.

## Gotchas

- O(N*M) is deliberate: the inputs are tool results (KBs) and the reply is
  capped at 100 lines, so the table is bounded by the bound the cap
  already imposes.
- `files` on a non-git working tree or a git failure is a loud refusal;
  the diff is applied/verified by the cap.
- `last` is this-session only; a no-session call renders a named absence.
