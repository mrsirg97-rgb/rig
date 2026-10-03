// Hand-written metadata for the graph store: the containers SPEC_GRAPH
// fixes — files (the sha spine), symbols (the map's nodes), edges (the
// pointer graph), meta (versions and the reference cache). Source of
// truth; domain and ddl are generated from it, never typed by hand.
// Edges carry both endpoints as natural keys (package + name), never a
// synthetic id: an in-place replace re-mints nothing, so edges written
// by one file's extraction survive another file's, and a dangling edge
// is a row whose join finds nothing, healed by the next extraction.
package metadata

import (
	_ "embed"
	"strings"
)

// table:"meta"
type Meta struct {
	Key   string `primary:"true" alias:"name=key,nullable=false"`
	Value string `alias:"name=value,nullable=false"`
}

// table:"files"
//
// Path is project-relative so every worktree of one repo shares the
// file. Edges_sha is the sha the file's outgoing edge rows were last
// rebuilt at: Go at extraction; a lazy extractor leaves it null — the
// language-server reference cache lives in meta (refs:<package>:<name>),
// marked with the sha of the symbol's own file at resolution time.
type File struct {
	Path     string  `primary:"true" alias:"name=path,nullable=false"`
	Sha256   string  `alias:"name=sha256,nullable=false"`
	Language string  `alias:"name=language,nullable=false"`
	EdgesSha *string `alias:"name=edges_sha,nullable=true"`
}

// table:"symbols"
//
// Package is the import path for Go, the project-relative directory for
// every other language. A method's Name carries its receiver (T.M). Line
// is the declaration line, the address the live read shows from, and
// EndLine is the declaration's last line — together the definition
// window pack reads; no source text is stored anywhere.
type Symbol struct {
	Package string `primary:"true" alias:"name=package,nullable=false"`
	Name    string `primary:"true" alias:"name=name,nullable=false"`
	Kind    string `alias:"name=kind,nullable=false"`
	File    string `alias:"name=file,nullable=false"`
	Line    int64  `alias:"name=line,nullable=false"`
	EndLine int64  `alias:"name=end_line,nullable=false"`
}

// table:"edges"
//
// From and to are natural keys, file is the using file (the edge's
// file IS the file whose extraction wrote it), line is the line of the
// use. The composite primary key collapses repeated uses on one line.
type Edge struct {
	FromPackage string `primary:"true" alias:"name=from_package,nullable=false"`
	FromName    string `primary:"true" alias:"name=from_name,nullable=false"`
	ToPackage   string `primary:"true" alias:"name=to_package,nullable=false"`
	ToName      string `primary:"true" alias:"name=to_name,nullable=false"`
	File        string `primary:"true" alias:"name=file,nullable=false"`
	Line        int64  `primary:"true" alias:"name=line,nullable=false"`
}

// extra.sql — what the DDL camera cannot emit: the seek indexes pack
// and the queue's replaces ride, and the two lexical containers the
// task pack's candidate arms read — the FTS5 virtual table over name,
// kind, package and file (line and end_line unindexed), the trigram
// shadow keyed by the symbol's natural key plus its gram.
//
//go:embed extra.sql
var extraSQL []byte

// ExtraStatements: the embedded extra.sql as individual statements —
// the symbol and edge seek indexes. Comment-only fragments drop; order
// is preserved.
func ExtraStatements() []string {
	var out []string
	for _, stmt := range strings.Split(string(extraSQL), ";") {
		var lines []string
		for _, l := range strings.Split(stmt, "\n") {
			if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "--") {
				continue
			}
			lines = append(lines, l)
		}
		if len(lines) != 0 {
			out = append(out, strings.Join(lines, "\n"))
		}
	}
	return out
}
