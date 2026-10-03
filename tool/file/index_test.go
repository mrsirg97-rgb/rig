package file_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/store/graph"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
	"github.com/mrsirg97-rgb/rig/v2/tool/file"
)

const indexSrc = `package alpha

func Alpha() int { return 1 }
`

func indexModule(t *testing.T) (string, *graph.Queue) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "alpha", "a.go"), []byte(indexSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, graph.NewQueue(t.TempDir(), nil)
}

func mapRows(t *testing.T, home, root, rel string) map[string]string {
	t.Helper()
	db, err := graph.Open(home, scope.Key(root), scope.Worktree(root))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name, kind FROM symbols WHERE file = ?`, rel)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var n, k string
		if err := rows.Scan(&n, &k); err != nil {
			t.Fatal(err)
		}
		out[n] = k
	}
	return out
}

func TestReadOfGoFileMapsItsPackage(t *testing.T) {
	root, q := indexModule(t)
	file.SetIndexer(q)
	defer file.SetIndexer(nil)
	ctx := context.Background()
	a := filepath.Join(root, "alpha", "a.go")
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": a})); err != nil {
		t.Fatalf("read: %v", err)
	}
	q.Drain(ctx)
	got := mapRows(t, q.Home(), root, "alpha/a.go")
	if got["Alpha"] != "func" {
		t.Fatalf("map = %v, want Alpha/func", got)
	}
}

func TestSecondReadWithSameShaWritesNothing(t *testing.T) {
	root, q := indexModule(t)
	file.SetIndexer(q)
	defer file.SetIndexer(nil)
	ctx := context.Background()
	a := filepath.Join(root, "alpha", "a.go")
	for i := 0; i < 2; i++ {
		if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": a})); err != nil {
			t.Fatalf("read: %v", err)
		}
		q.Drain(ctx)
	}
	got := mapRows(t, q.Home(), root, "alpha/a.go")
	if len(got) != 1 {
		t.Fatalf("map = %v, want the one symbol", got)
	}
}

func TestEditReplacesSymbolsAndEdgesInPlace(t *testing.T) {
	root, q := indexModule(t)
	file.SetIndexer(q)
	defer file.SetIndexer(nil)
	ctx := context.Background()
	a := filepath.Join(root, "alpha", "a.go")
	if _, err := file.Read().Exec(ctx, argsJSON(t, map[string]any{"path": a})); err != nil {
		t.Fatalf("read: %v", err)
	}
	q.Drain(ctx)
	if _, err := file.Edit().Exec(ctx, argsJSON(t, map[string]any{
		"path": a,
		"edits": []map[string]string{
			{"old": "func Alpha() int { return 1 }", "new": "func Beta() int { return 1 }"},
		},
	})); err != nil {
		t.Fatalf("edit: %v", err)
	}
	q.Drain(ctx)
	got := mapRows(t, q.Home(), root, "alpha/a.go")
	if got["Beta"] != "func" {
		t.Fatalf("map = %v, want Beta/func after the edit", got)
	}
	if _, dead := got["Alpha"]; dead {
		t.Fatalf("the edit left Alpha in the map: %v", got)
	}
}
