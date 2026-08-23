package todo_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/store"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

func TestFilePathIsOneStoreUnderTodo(t *testing.T) {
	home := t.TempDir()
	path := todostore.FilePath(home)
	if filepath.Base(path) != "todo.sqlite" {
		t.Fatalf("path %q, want one todo.sqlite", path)
	}
	if filepath.Dir(path) != filepath.Join(home, "todo") {
		t.Fatalf("path %q, want it under %s/todo", path, home)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, _, _, err := store.Open(path, todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	ctx := context.Background()
	p := todostore.Project{Key: "scope-a", Label: "a"}
	if _, err := todostore.Create(ctx, db, p, []todostore.CreateItem{{Text: "a task"}}, "seed"); err != nil {
		t.Fatal(err)
	}
	text, err := todostore.Read(ctx, db, p, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "a task") {
		t.Fatalf("round-trip read %q, want the created task", text)
	}
}
