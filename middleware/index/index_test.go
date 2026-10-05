package index_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/middleware/index"
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
	read, _ := mapped(q)
	ctx := context.Background()
	a := filepath.Join(root, "alpha", "a.go")
	if _, err := read(ctx, core.ToolCall{Name: "read", Args: argsJSON(t, map[string]any{"path": a})}); err != nil {
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
	read, _ := mapped(q)
	ctx := context.Background()
	a := filepath.Join(root, "alpha", "a.go")
	for i := 0; i < 2; i++ {
		if _, err := read(ctx, core.ToolCall{Name: "read", Args: argsJSON(t, map[string]any{"path": a})}); err != nil {
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
	read, edit := mapped(q)
	ctx := context.Background()
	a := filepath.Join(root, "alpha", "a.go")
	if _, err := read(ctx, core.ToolCall{Name: "read", Args: argsJSON(t, map[string]any{"path": a})}); err != nil {
		t.Fatalf("read: %v", err)
	}
	q.Drain(ctx)
	if _, err := edit(ctx, core.ToolCall{Name: "edit", Args: argsJSON(t, map[string]any{
		"path": a,
		"old":  "func Alpha() int { return 1 }",
		"new":  "func Beta() int { return 1 }",
	})}); err != nil {
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

func mapped(q *graph.Queue) (core.ToolExec, core.ToolExec) {
	mw := index.Middleware(q)
	return mw.Wrap(execOf(file.Read())), mw.Wrap(execOf(file.Edit()))
}

func execOf(tool core.Tool) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		return tool.Exec(ctx, call.Args)
	}
}

func argsJSON(t *testing.T, args map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOtherToolsAndFailuresTouchNothing(t *testing.T) {
	_, q := indexModule(t)
	touched := &recorder{}
	mw := index.Middleware(touched)
	ok := mw.Wrap(func(ctx context.Context, call core.ToolCall) (string, error) { return "ran", nil })
	bad := mw.Wrap(func(ctx context.Context, call core.ToolCall) (string, error) { return "", os.ErrNotExist })
	ctx := context.Background()
	args := argsJSON(t, map[string]any{"path": "/tmp/x.go"})
	if _, _ = ok(ctx, core.ToolCall{Name: "bash", Args: args}); len(touched.paths) != 0 {
		t.Fatalf("bash is not a mapped tool, touched %v", touched.paths)
	}
	if _, _ = bad(ctx, core.ToolCall{Name: "read", Args: args}); len(touched.paths) != 0 {
		t.Fatalf("a failed read touches nothing, touched %v", touched.paths)
	}
	if _, _ = ok(ctx, core.ToolCall{Name: "write", Args: args}); len(touched.paths) != 1 || touched.paths[0] != "/tmp/x.go" {
		t.Fatalf("a written path is touched once, got %v", touched.paths)
	}
	if _, _ = ok(ctx, core.ToolCall{Name: "read", Args: json.RawMessage(`{"offset": 3}`)}); len(touched.paths) != 1 {
		t.Fatalf("no path, no touch, got %v", touched.paths)
	}
	_ = q
}

type recorder struct{ paths []string }

func (r *recorder) Touch(p string) { r.paths = append(r.paths, p) }
