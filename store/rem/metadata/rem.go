// Hand-written metadata for the rem store: the containers SPEC_STATE's
// "### rem" section fixes — memories (the record), trigrams (the fuzzy
// arm's shadow), meta (versions and the id-minting counter). Pane's
// REM_SPEC D is the schema being expressed; lift's four-tag grammar is
// the language. Nullable columns are pointers. Supersession pairs a
// nullable alias with its self-link (the pairing that keeps the FK out
// of the generated INSERT is lift working as designed; the sqlite
// camera leaves foreign keys off, so the SET NULL behaviour lives in the
// store's prune, named).
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

// table:"memories"
//
// Id is minted by the store from the meta counter inside the caller's
// transaction — never reused (REM_SPEC's AUTOINCREMENT rule, kept by
// minting since the camera emits plain PRIMARY KEY).
type Memory struct {
	ID                 int64   `primary:"true" alias:"name=id,nullable=false"`
	Scope              string  `alias:"name=scope,nullable=false"`
	ScopeLabel         string  `alias:"name=scope_label,nullable=false"`
	Kind               string  `alias:"name=kind,nullable=false"`
	Content            string  `alias:"name=content,nullable=false"`
	Source             *string `alias:"name=source,nullable=true"`
	Importance         float64 `alias:"name=importance,nullable=false"`
	Strength           float64 `alias:"name=strength,nullable=false"`
	AccessCount        int64   `alias:"name=access_count,nullable=false"`
	SupersededBy       *int64  `alias:"name=superseded_by,nullable=true"`
	Superseder         *Memory `link:"from=Memory,on=id,many=false"`
	CreatedAt          string  `alias:"name=created_at,nullable=false"`
	LastAccessedAt     *string `alias:"name=last_accessed_at,nullable=true"`
	LastConsolidatedAt string  `alias:"name=last_consolidated_at,nullable=false"`
	ContentMd5         string  `alias:"name=content_md5,nullable=false"`
}

// table:"trigrams"
//
// Composite primary (memory_id + gram): the natural key of one shadow
// row, enforced by the database. Pane's table carries no key; the
// composite is the db-enforced shape of the same fact. The link column
// generates the bulk-delete accessor the prune's bookkeeping rides.
type Trigram struct {
	MemoryID int64  `primary:"true" alias:"name=memory_id,nullable=false"`
	Memory   Memory `link:"from=Memory,on=id,many=true"`
	Gram     string `primary:"true" alias:"name=gram,nullable=false"`
}

// extra.sql — what the DDL camera cannot emit (SPEC_STATE, decisions):
// the natural-key unique index (idempotent create), the recall ordering
// spine, and the trigram seek indexes — pane's schema verbatim.
//
//go:embed extra.sql
var extraSQL []byte

// fts.sql — the FTS5 virtual table, the semantic arm's substrate. Not
// expressible in the four-tag grammar (a virtual table, not a container);
// its bookkeeping is written in code in the same transaction, never by
// triggers (REM_SPEC E).
//
//go:embed fts.sql
var ftsSQL []byte

// ExtraStatements: the embedded extra.sql as individual statements —
// pane's indexes, what the camera cannot emit. Comment-only fragments
// drop; order is preserved.
func ExtraStatements() []string {
	return statementsOf(string(extraSQL))
}

// FtsStatements: the embedded fts.sql, individually.
func FtsStatements() []string {
	return statementsOf(string(ftsSQL))
}

func statementsOf(sql string) []string {
	var out []string
	for _, stmt := range strings.Split(sql, ";") {
		var lines []string
		for _, l := range strings.Split(stmt, "\n") {
			if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "--") {
				continue
			}
			lines = append(lines, l)
		}
		if len(lines) > 0 {
			out = append(out, strings.Join(lines, "\n"))
		}
	}
	return out
}
