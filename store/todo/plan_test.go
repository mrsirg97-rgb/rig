package todo

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/store"
)

func TestEventsOfSeeksOneScopeAndTheGlobalHighWater(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "todo.sqlite"), Statements(), SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.DB.Close()
	plan := func(q string, args ...any) string {
		t.Helper()
		rows, err := db.DB.Query("EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatalf("explain %s: %v", q, err)
		}
		defer rows.Close()
		var out string
		for rows.Next() {
			var a, b, c int
			var d string
			if err := rows.Scan(&a, &b, &c, &d); err != nil {
				t.Fatal(err)
			}
			out += d + "\n"
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	scope := plan(eventsQuery, "ws")
	if !strings.Contains(scope, "events_scope_seq") {
		t.Fatalf("the fold must seek one scope through the (scope, seq) index:\n%s", scope)
	}
	if strings.Contains(scope, "TEMP B-TREE") || strings.Contains(scope, "SCAN events") {
		t.Fatalf("the fold must not scan the cross-project log:\n%s", scope)
	}
	high := plan(globalSeqQuery)
	if strings.Contains(high, "SCAN events") {
		t.Fatalf("the global high-water must ride the seq index:\n%s", high)
	}
}
