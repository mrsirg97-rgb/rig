# tool/fs: ls, find, grep

Named filesystem tools beside bash, read, write, and edit. Named tools with
small schemas are what a local model reaches for; `bash grep` is where it
fumbles quoting. One leaf package, three tools, stdlib only.

## goals

- `ls`: one directory level — entry kind, name, size; hidden optional.
- `find`: glob by name under a root; `**` spans directories.
- `grep`: Go regexp over content under a root, `path:line: text`, optional
  glob filter.
- `Description` on the wire as `function.description` — the one seam change in
  this deliverable, and what makes named tools worth having.

## non-goals

- No fuzzy or semantic search; Go regexp only, syntax errors surfaced.
- No symlink following (`filepath.WalkDir` does not).
- No content indexing, watch modes, or anything stateful.

## layout

```
tool/fs/
  fs.go         the three tools, hand-authored schemas
  fs_test.go    real filesystem in t.TempDir(), named cases
```

## the seam change (named)

`core.Tool` gains `Description() string`; `core.ToolSpec` gains
`Description string`; the loop emits it per tool; the OpenAI adapter carries
it to `function.description`. Compile-time enforced across every registered
tool and test fake; the four existing tools each gain a one-line description
in the voice of their behavior. `specs/SPEC_CORE.md`'s Tool section changes
accordingly and is named in the PR.

## decisions

- Stdlib only: `filepath.WalkDir`, `regexp`, `path/filepath.Match`, plus a
  hand-rolled `**` segment matcher on top of `path.Match`; a bare pattern (no
  `/` and no `**`) names the file name itself — the find -name reading —
  anything else globs the root-relative path.
- Skips, unconditional: `.git` subtrees under a walked root; binary content
  (a NUL byte within the first 8 KiB). Unreadable entries (an unreadable
  directory pruned, an unreadable file skipped — at walk time or at use
  time) are counted and named in the output as `[skipped: N unreadable]`
  — loud, never a silent abort and never a silent swallow. A missing or
  unreadable ROOT is a loud error, not a search that found nothing.
- Caps, hard and loud: `ls` 1000 entries, `find` 1000 paths, `grep` 500 match
  lines. Truncation is named in the output — `[truncated: N of M]` — never
  silent. Memory stays bounded: matches beyond the cap count, not
  accumulate. A matched line's text is capped at 512 bytes with a
  `[line truncated]` marker, so one match cannot carry a minified bundle.
- Paths in results are relative to the walked root, slash-separated; results
  sorted by path then line number.
- `ls` sorts within its level (ReadDir order); an empty directory prints
  `(empty)`; missing or non-directory paths are loud errors.
- `grep` long lines: reader-based line iteration; matched-line text capped
  as above.
- Cancellation: `ctx` checked at the walk boundary; a cancelled walk
  surfaces the context error, never a partial result.

## testing

Named cases against the real filesystem in `t.TempDir()`:

- ls: kind/size rendering; hidden excluded by default, included on ask; the
  cap names its truncation.
- find: nested globs including `**`; bare patterns (no `/`, no `**`) reaching
  nested files by name; missing pattern refused loud.
- walk accounting: an unreadable directory's subtree and an unreadable
  file at use time reported as `[skipped: N unreadable]`, readable matches
  still delivered (find and grep alike); a missing or unreadable root is a
  loud error from both; a cancelled context surfaces loudly from both.
- grep: the matched-line byte cap named by its marker.
- grep: multi-file `path:line: text`; glob filter; `.git` and binary skips;
  the cap marker with true totals.
- Description: present on every fs tool (asserted); the adapter's spec→wire
  mapping carries it (shape test extended).

The bash tool is not used to implement any of it.

## scope

One leaf package, one named seam change, three registration lines and the
allow-list default growing at the root. The loop itself is byte-identical
except the description field on the spec.
