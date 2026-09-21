# imagemarker

## What it is

The one contract for an image a model has looked at: the marker line a
`view` result is, and the address of the bytes it names. Three packages
touch those bytes — `tool/view` writes them, `provider/openai` reads them
back into an image part, `frontend/tui` prints them — and the prompt cache
is byte-keyed, so one implementation of the format is the whole reason this
package exists. It is stdlib only and imports nothing of rig's, the way
`pathguard` is.

## What it includes

- `marker.go`: `Ref` (the parsed line), `Format` (the line, fixed key
  order), `Parse` (exactly one marker line, strict), `Find` (the first
  marker line inside a tool result), `BlobPath` (`<dir>/<hex>`, no
  extension), `BlobsDir` (`<RIG_HOME>/blobs`), `IsAddress` (64 lowercase
  hex).

## How it is consumed

- `tool/view` formats the reply and stores the bytes at `BlobPath`.
- `provider/openai` calls `Find` on a tool result it has already decided
  came from `view`, then reads and digests the blob at `BlobPath`.
- `frontend/tui` calls `Find` for the row's dimensions and size, and never
  prints the line itself.
- The store's directory is the root's decision (`<RIG_HOME>/blobs`); this
  package only names it.

## Gotchas

- `Parse` is strict where `Find` is tolerant. `Find` scans lines and keeps
  the first that parses; `Parse` accepts only the whole line, only the
  pinned key order, only the value shapes `Format` writes. A line a human
  edited is not a marker.
- `src` is last because it is the only value that may contain a space. It
  runs to the terminator, so a path carrying `]]` or `bytes=` survives —
  the fixed fields before it never can, and they are checked.
- A blob's bytes are a function of the decoded pixels, never of the source
  file's encoder or name, so `BlobPath` carries no extension and the mime
  rides in the line. Two tools that disagreed about that would write two
  blobs for one image.
- `BlobPath` returns `""` for anything that is not 64 lowercase hex rather
  than a path outside the store: a malformed address must not become a
  traversal.
- `orig` (the dimensions before rig resized) exists for the row's
  `2560x1440 -> 1568x882`. The tool result is the only channel from tool to
  frontend, so both sizes travel in the line; it sits before `bytes` and
  `src` so every pinned field keeps its position.
