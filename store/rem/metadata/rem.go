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
type Trigram struct {
	MemoryID int64  `primary:"true" alias:"name=memory_id,nullable=false"`
	Memory   Memory `link:"from=Memory,on=id,many=true"`
	Gram     string `primary:"true" alias:"name=gram,nullable=false"`
}

//go:embed extra.sql
var extraSQL []byte

//go:embed fts.sql
var ftsSQL []byte

func ExtraStatements() []string {
	return statementsOf(string(extraSQL))
}

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
