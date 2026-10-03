# store/graph

## What it is

The code map: one sqlite file per project under the rig home
(`<home>/graph/<scope>.sqlite`, scope minted like todo's — `store/scope`,
worktrees share), Go over the generated substrate (SPEC_GRAPH). The map
holds addresses, never source text: `files` (path, sha256, language, the
sha the file's outgoing edge rows were last rebuilt at), `symbols`
(package, name, kind, file, line), `edges` (from symbol, to symbol, file,
line of the use), and `meta` (versions, the reference cache). Paths are
project-relative, so every worktree of one repo shares the file.

## What it includes

- `metadata/metadata.go` (+ `extra.sql`): hand-written container
  metadata, the source for the generated `ddl`/`domain`. Edit and
  regenerate; never hand-edit the projections.
- `extract.go`: the seam (`Extract(file) -> symbols, edges`), the
  language table, and the project-root/package-path rules (`go.mod`
  first, then the git toplevel, then the file's directory; package =
  module path plus the directory below the root).
- `extract_go.go`: the Go extractor. `go/parser` + `go/types`, the
  package as the unit: symbols always from the package-scope AST of the
  touched file, edges from `Uses` (package-scope references, in-module
  only), `Selections` (method calls), and the name fallback when the
  type-check fails (selectors through the file's own imports, plain
  identifiers that name a package-scope symbol). Its `modImporter` checks
  module-internal imports from source, one cache per project, and
  delegates everything else to `importer.Default()` — no subprocess, no
  cwd dependence.
- `graph.go`: the store (`Statements`, `FilePath`, `Open`) and `Apply`,
  the in-place replace by file sha: the files row upserted, symbols
  upserted by natural key (a symbol that moved files moves its row), the
  file's outgoing edges deleted and rewritten, one transaction. An
  unchanged sha answers `written=false` and writes nothing. `Result.Eager`
  says the edges arrived with the extraction (Go); a lazy extractor's
  replace clears the file's outgoing edges and leaves `edges_sha` null.

## How it is consumed

- `tool/rem` (the `index` and `pack` actions) and the `tool/file`
  read/write/edit hook call it; the queue shape (bounded channel, one
  goroutine, the call never waits) lives with the wiring (SPEC_GRAPH).
- The generated files are pinned by the drift test
  (`cd <lift>/cmd && go run main.go -config=$RIG/store/graph/gen.json
  -source=$RIG/store/graph/source.json` regenerates).

## Gotchas

- Edges are natural keys, never synthetic ids: a replace re-mints
  nothing, so edges one file's extraction wrote survive another file's,
  and a dangling edge (its target's row went away) is a row whose join
  finds nothing, healed by the next extraction of the file that owns it.
- A type-check failure is not an error: names come from the AST, edges
  from whatever resolved plus the name fallback, and the extraction
  stays silent about the degradation.
- The receiver name is the map's method identity (`Rec.Move`); a receiver
  the AST cannot name (a remote or malformed type) drops the symbol
  rather than guessing.
- `Skip` refuses `vendor`, `testdata`, and dot/underscore paths by path
  component; the extractor refuses before parsing, the walk before
  enqueueing.
- Extraction parses the file's whole directory: a broken sibling costs a
  parse error the camera swallows, and a broken touched file fails the
  extraction (the store is left untouched — fail closed).
