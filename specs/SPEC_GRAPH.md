# SPEC_GRAPH: store/graph — the code map

Evidence, from a session's own log: to answer "who calls gateOnce" it
greps, then reads two or three files into context. The map replaces the
grep-and-read ritual with one `pack` call over state the session already
wrote. This spec is the spine; the `PACKAGE.md` carries the tour.

One store per worktree under the rig home
(`<home>/graph/<repo scope>/<worktree>.sqlite`): the repo scope minted
like todo's (`store/scope`: the repo's common dir; a cwd-hash fallback
outside a repo), the worktree from `git rev-parse --git-dir` — the
`worktrees/<name>` leaf for a linked worktree, the checkout directory's
name otherwise. Branches never share a map; a merge needs no step, since
main's worktree re-extracts its changed files by sha. Generated through
lift, metadata first (`metadata/metadata.go` + `extra.sql`; the
generated `ddl`/`domain` are never typed by hand). Containers:

- `files`: path (project-relative; each worktree's store maps its own
  tree), sha256, language, edges_sha (the sha the file's outgoing edge
  rows were last rebuilt at: Go at extraction; a lazy extractor leaves
  it null — the language-server reference cache is the meta key
  `refs:<package>:<name>`, marked with the sha of the symbol's own file
  at resolution time).
- `symbols`: package (the import path for Go, the project-relative
  directory otherwise; a file declared `package x_test` keys
  `<importpath>_test`, so its symbols never overwrite the package's),
  name (a method carries its receiver, `T.M`), kind (`func`, `method`,
  `type`, `var`, `const`), file, line — the declaration line, the
  address a live read shows from — and end_line, the declaration's last
  line (Go from the declaration's End(), the language server from the
  symbol range's end). Natural key (package, name): Go forbids two
  package-scope objects with one name.
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
  symbol). A file declared `package x_test` is checked and keyed as
  `<importpath>_test`, and its edges still name it as the caller. Edges
  to packages outside the module are dropped: the map is the project's
  code. `vendor`, `testdata` and dot/underscore paths are skipped.
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
full queue drops with one loud line, never blocks. `index` does not ride
the channel: it walks the project root and extracts every mapped file on
the call's own thread, nothing dropped, and replies with the count
mapped when done — it pays the loop's thread once, by request.

## rem gains two actions

`index`: the walk above; the reply names the count mapped when done.
`pack` takes a symbol (package-qualified or bare) or a file path and
replies from the live files: the definition (the lines line..end_line,
bounded by the read ceiling), each caller with its call line, the
signatures of what it calls (each callee's live signature lines, to the
opening brace), then one coverage line (packages or modules mapped of
those present — distinct directories of the files table over distinct
directories of the walk). A cited file whose sha moved is re-extracted
first, synchronously, before its lines are quoted. A bare name defined
in two places refuses, naming both; a qualified name whose package has
no such symbol refuses naming where the map has it (`no Kernel in
package loop; the map has v2.Kernel`); an import path (`github.com/…/v2.Kernel`)
refuses and names the package tail, since a target with a slash is a
file (2.9.3). The pack is bounded by the read
ceiling (`ReadCap`) and registers no observation: it writes no file
state into the session, so an edit's drift check still demands a real
read.

Rejected, named: synthetic-id edges (a replace re-mints ids and orphans
every edge another file wrote); eager edges for LSP languages (a
documentSymbol call cannot see who references a symbol; references can);
source text in the store (a second copy of the tree to invalidate);
waiting on the queue (the loop's thread is the one resource the harness
rations).

## the 2.10.0 task pack

`pack` also takes a task: a target with a space that names no file is a
sentence. The candidates are the symbols whose name, kind, package or
file match the task through the two lexical arms recall uses — an FTS5
virtual table (`symbol_fts`) and a trigram shadow (`symbol_grams`) over
the same four fields, in extra.sql, the generated ddl and domain
untouched — fused as recall fuses them (reciprocal rank, k=60; the
trigram arm keeps recall's containment floor). The arms are bounded by
the read ceiling of candidate lines: the candidate list is cut so the
items — each candidate's live signature to the opening brace at
file:line — stay under the ceiling before any request goes out.

With the decision server wired, the candidates ride the exported
fan-out (one yes/no per candidate, "Does this symbol matter for the
task?", the kernel's Parallel the bound), the state on the wire the
task and the item. No more candidates are scored than the pack could
load: the scored set is the rank prefix whose items fit the load cap,
the candidates' own sizes the measure, and the tail rides the lexical
fill unscored. The yes set loads live in rank order: the lexical rank
is the spine and the server's yes promotes within it, never re-orders
it. The unsure rule is decide's — an answer whose confidence is under
one half is unsure, and the unsure are listed by name at the end so
the model can pack one by hand instead of being loaded; a confident no
and a candidate the server never answered are not listed. The load cap
is the result cap the root passes in (the kernel cuts tool results at
the same cap), not the read ceiling: the affirmations spend it first,
then the lexical top of the rank fills the rest — the server's
judgment promotes within the rank, never censors, so a scored pack is
never worse than an unscored one. Every answered candidate is one pending row (site `pack`,
the server as decider, the task and the item as state) the 2.7.0
reviewer settles as it settles bash rows, a deny naming the right
answer. Without a server the lexical candidates load alone, in rank
order, and nothing is recorded. No automatic pack: the model asks; one
guideline joins the system prompt only when `decisionUrl` is set.

The lexical containers are schema version 2: the migration rebuilds
`symbol_fts` and `symbol_grams` from `symbols` on open, because a
replace whose sha is unchanged is a no-op and a store written by
version 1 would leave its symbols unsearchable. The map's extra writes
per symbol (the fts row, the gram rows) ride the same replace
transaction; a stale candidate whose live read fails is skipped
loudly.

## placement

```
store/graph/           metadata + gen.json/source.json, generated
                       ddl/domain; graph.go (the store: open per scope,
                       the in-place replace), extract.go (the seam),
                       extract_go.go (Go), lsp.go + extract_lsp.go (the
                       client and the language-server implementation)
middleware/index/      the hook: a link in the canonical chain, innermost,
                       that touches the path of a read, write or edit
                       that returned without error
tool/rem/              index and pack actions; the words in tool/registry.json
cmd/rig/main.go        the queue created and Run, its notices through the
                       frontend (core.Notice, source graph), the link added
                       to canonicalMiddleware
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
are cached by sha. Index of a 400-file project maps 400. Contains in
busy.go and contains in core_test.go are two symbols, and pack of
gateOnce shows exactly its 17 lines — its callee's signature stopping at
the opening brace. The store sits per worktree under the repo scope.
The generated files are pinned by the drift test (todo's pattern).

The task pack (2.10.0): a task matching three symbols packs the two the
server says yes to and lists the wobbly third as unsure; a confident no
is hidden and the lexical top fills the rest of the budget; the scored
set is the rank prefix the load could hold and the pack rides the
lexical spine; a candidate list past the read ceiling is cut before any
request; unset decisionUrl
packs the lexical candidates in rank order and writes no row; each
scored candidate writes one pending row and the reviewer's deny stores
the corrected answer; the lexical tables hold a row per symbol, follow
a replace (a gone symbol's rows leave with it) and the version 2
migration rebuilds them from a version 1 store's symbols on open.
