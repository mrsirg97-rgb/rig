# tool/fs

## What it is

Named filesystem tools: `ls`, `find`, `grep`. Named tools with small
schemas are what a local model reaches for; `bash grep` is where it
fumbles quoting. Stdlib only: `WalkDir`, `regexp`, `path.Match`.

## What it includes

- `ls`, `find`, `grep`: a `core.Tool` each, over the stdlib fs surface.
  An empty reply names its scope: the directory, or the pattern and the
  absolute root (and glob) searched (SPEC_CORE).

## How it is consumed

- Registered at the root as native tools.

## Gotchas

- Results are bounded: `grep` uses `regexp` (the model's pattern), and the
  output is surfaced verbatim.
