# SPEC_GRAPH: store/graph — the code map

Evidence, from a session's own log: to answer "who calls gateOnce" it
greps, then reads two or three files into context. The map replaces the
grep-and-read ritual with one `pack` call over state the session already
wrote. This spec is the spine; the `PACKAGE.md` carries the tour.

One store, one sqlite file per project under the rig home
(`<home>/graph/<scope>.sqlite`), scope minted like todo's
(`store/scope`: the repo's common dir, worktrees share; a cwd-hash
fallback outside a repo). Generated through lift, metadata first
(`metadata/metadata.go` + `extra.sql`; the generated `ddl`/`domain` are
never typed by hand). Containers:

- `files`: path (project-relative — every worktree shares the file),
  sha256, language, edges_sha (the sha the file's outgoing edge rows
  were last rebuilt at: at Go extraction, or when pack resolved through
  references; the cache check is this column against the live sha).
- `symbols`: package (the import path for Go, the project-relative
  directory otherwise), name (a method carries its receiver, `T.M`),
  kind (`func`, `method`, `type`, `var`, `const`), file, line — the
  declaration line, the address a live read shows from. Natural key
  (package, name): Go forbids two package-scope objects with one name.
- `edges`: from symbol, to symbol (both natural keys, never a synthetic
  id — an in-place replace re-mints nothing, so edges one file's
  extraction wrote survive another file's), file (the using file; the
  edge's file IS the file whose extraction wrote it), line of the use.
- `meta`: schema version and the reference cache.

No source text is stored anywhere: the store holds addresses, pack
reads the live bytes.

## the Extract seam

`Extract(file) -> symbols, edges`, two implementations behind one
interface, chosen by language (extension):

- **Go, in-process.** `go/parser` + `go/types`, the package as the unit:
  `parser.ParseDir` of the file's directory, `types.Config{Importer:
  importer.Default()}.Check` over that package with `Defs`, `Uses` and
  `Selections`. Symbols always come from the AST (package-scope decls of
  the touched file), so a type-check failure keeps what resolved and
  falls back to names: edges from `Uses` and `Selections` where types
  resolved, else the syntactic fallback (a selector resolved through the
  file's own imports; a plain identifier that names a package-scope
  symbol). Edges to packages outside the module are dropped: the map is
  the project's code. `vendor`, `testdata` and dot/underscore paths are
  skipped.
- **Every other language, through a language server.** A child process
  over stdio speaking JSON-RPC (`initialize`, `documentSymbol`,
  `references`), started on the first read of a file in that language,
  one server at a time — touching another language replaces it — living
  as long as the session, never on the loop's thread. The operator
  installs the servers; rig spawns them (TypeScript first). For these
  languages symbols are stored on extraction and edges are resolved when
  pack asks, through references, cached by file sha: the `meta` key
  `refs:<package>:<name>` holds the sha the resolution ran at; a hit
  gathers the stored edges, a miss asks the server, rebuilds the edge
  rows and re-marks. The enclosing symbol of a reference is the greatest
  declaration line at or before it in the citing file's own map.

## the 2.7.0 queue shape

After a read, write or edit of a file in a mapped language, a bounded
channel (`QueueCap`) and one goroutine extract that file (its package
for Go) and replace its symbols and outgoing edges in place: the files
row upserted, symbols upserted by natural key (a symbol that moved files
moves its row, never duplicates), the file's outgoing edges deleted and
rewritten, one transaction. An unchanged sha is a no-op — the write path
answers `written=false` and touches nothing. The call never waits: a
full queue drops with one loud line, never blocks. `index` maps the
whole project the same way: a walk of the project root, every mapped
file enqueued.

## rem gains two actions

`index`: the walk above; the reply names how many files were queued.
`pack` takes a symbol (package-qualified or bare) or a file path and
replies from the live files: the definition (a bounded window at the
declaration line), each caller with its call line, the signatures of
what it calls (each callee's live declaration), then one coverage line
(packages or modules mapped of those present — distinct directories of
the files table over distinct directories of the walk). A cited file
whose sha moved is re-extracted first, synchronously, before its lines
are quoted. A bare name defined in two places refuses, naming both. The
pack is bounded by the read ceiling (`ReadCap`) and registers no
observation: it writes no file state into the session, so an edit's
drift check still demands a real read.

Rejected, named: synthetic-id edges (a replace re-mints ids and orphans
every edge another file wrote); eager edges for LSP languages (a
documentSymbol call cannot see who references a symbol; references can);
source text in the store (a second copy of the tree to invalidate);
waiting on the queue (the loop's thread is the one resource the harness
rations).

## placement

```
store/graph/           metadata + gen.json/source.json, generated
                       ddl/domain; graph.go (the store: open per scope,
                       the in-place replace), extract.go (the seam),
                       extract_go.go (Go), lsp.go + extract_lsp.go (the
                       client and the language-server implementation)
tool/file/             the hook: SetIndexer, called by read/write/edit
tool/rem/              index and pack actions, the description
cmd/rig/main.go        the store created and Run, the hook registered
```

`core/` and `loop/` are untouched; `go.mod` is unchanged (stdlib only:
`go/parser`, `go/types`, `os/exec` for the server child).

## testing

Failing test first: a read of one Go file maps its package and a second
read with the same sha writes nothing; an edit replaces the file's
symbols and outgoing edges in place; pack on a symbol shows its
definition, callers with lines and callee signatures from the current
file contents after an external change; an ambiguous bare name refuses
naming both places; a package that fails to type-check still yields its
names; pack registers no observation; a read of a non-Go file starts the
fake server once and a second language replaces it; a fake-server
documentSymbol reply lands as symbols and references resolve on pack and
are cached by sha. The generated files are pinned by the drift test
(todo's pattern).
