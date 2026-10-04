package graph_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/evt"
	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/graph"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
)

func TestApplyMaintainsTheLexicalTables(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package alpha\n\nfunc Target() int { return Helper() }\n\nfunc Helper() int { return 1 }\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := graph.Open(t.TempDir(), "testscope", "main")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	sum := sha256.Sum256([]byte(src))
	if _, err := graph.Apply(ctx, db, "a.go", hex.EncodeToString(sum[:]), "go", graph.Result{
		Eager: true,
		Symbols: []graph.Symbol{
			{Package: "example.com/m/alpha", Name: "Target", Kind: "func", File: "a.go", Line: 3, EndLine: 3},
			{Package: "example.com/m/alpha", Name: "Helper", Kind: "func", File: "a.go", Line: 5, EndLine: 5},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var fts int
	if err := db.QueryRow(`SELECT count(*) FROM symbol_fts`).Scan(&fts); err != nil {
		t.Fatal(err)
	}
	if fts != 2 {
		t.Fatalf("symbol_fts holds a row per symbol, got %d", fts)
	}
	var grams int
	if err := db.QueryRow(`SELECT count(*) FROM symbol_grams`).Scan(&grams); err != nil {
		t.Fatal(err)
	}
	if grams == 0 {
		t.Fatal("symbol_grams holds the trigram shadow of each symbol's name, kind, package and file")
	}
	var pkg, name string
	if err := db.QueryRow(`SELECT package, name FROM symbol_fts WHERE symbol_fts MATCH 'target'`).Scan(&pkg, &name); err != nil {
		t.Fatalf("the fts arm answers a task token: %v", err)
	}
	if name != "Target" {
		t.Fatalf("the fts arm found %q", name)
	}
	if err := db.QueryRow(`SELECT count(*) FROM symbol_grams WHERE name = 'Target'`).Scan(&grams); err != nil {
		t.Fatal(err)
	}
	if grams == 0 {
		t.Fatal("each symbol's trigram shadow rides the gram rows")
	}
	moved := "package alpha\n\nfunc Helper() int { return 1 }\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256([]byte(moved))
	if _, err := graph.Apply(ctx, db, "a.go", hex.EncodeToString(sum[:]), "go", graph.Result{
		Eager: true,
		Symbols: []graph.Symbol{
			{Package: "example.com/m/alpha", Name: "Helper", Kind: "func", File: "a.go", Line: 3, EndLine: 3},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM symbol_fts`).Scan(&fts); err != nil {
		t.Fatal(err)
	}
	if fts != 1 {
		t.Fatalf("the replaced file's stale symbols leave the lexical tables, got %d fts rows", fts)
	}
	if err := db.QueryRow(`SELECT count(*) FROM symbol_grams WHERE name = 'Target'`).Scan(&grams); err != nil {
		t.Fatal(err)
	}
	if grams != 0 {
		t.Fatalf("the gone symbol's grams leave the shadow, got %d", grams)
	}
}

func TestTheMigrationRebuildsTheLexicalTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.sqlite")
	db, _, _, err := store.Open(path, graph.Statements(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct {
		pkg, name, file string
		line            int64
	}{
		{"example.com/m/alpha", "Target", "a.go", 3},
		{"example.com/m/alpha", "Helper", "a.go", 5},
	} {
		if _, err := db.Exec(`INSERT INTO symbols (package, name, kind, file, line, end_line) VALUES (?, ?, 'func', ?, ?, ?)`,
			s.pkg, s.name, s.file, s.line, s.line); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db2, _, report, err := store.Open(path, graph.Statements(), 2, graph.Migration())
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if !strings.Contains(report, "lexical") {
		t.Fatalf("the migration names the rebuild: %q", report)
	}
	var fts int
	if err := db2.QueryRow(`SELECT count(*) FROM symbol_fts`).Scan(&fts); err != nil {
		t.Fatal(err)
	}
	if fts != 2 {
		t.Fatalf("the migration rebuilt the fts rows, got %d", fts)
	}
	var grams int
	if err := db2.QueryRow(`SELECT count(*) FROM symbol_grams`).Scan(&grams); err != nil {
		t.Fatal(err)
	}
	if grams == 0 {
		t.Fatal("the migration rebuilt the trigram shadow")
	}
	var name string
	if err := db2.QueryRow(`SELECT name FROM symbol_fts WHERE symbol_fts MATCH 'target'`).Scan(&name); err != nil {
		t.Fatalf("the rebuilt tables answer the task tokens: %v", err)
	}
	if name != "Target" {
		t.Fatalf("the rebuilt tables found %q", name)
	}
}

func TestAStaleSymbolRowIsDroppedNotSaid(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package alpha\n\nfunc Target() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := evt.NewEngine()
	go engine.Start(context.Background())
	defer engine.Stop()
	room := broadcast.NewRoom("test", func(id int64) broadcast.Transport {
		return broadcast.NewLoopTransport(id, engine, 0)
	})
	said := make(chan string, 64)
	room.Add(-1).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if n, ok := m.Event().(core.Notice); err == nil && ok {
				said <- n.Text
			}
		}
	})
	q := graph.NewQueue(t.TempDir(), room.Add(0))
	defer q.Close()
	ctx := context.Background()
	if _, err := q.IndexProject(ctx, root); err != nil {
		t.Fatal(err)
	}
	db, err := graph.Open(q.Home(), scope.Key(root), scope.Worktree(root))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := graph.Apply(ctx, db, "gone/g.go", "deadbeef", "go", graph.Result{
		Eager:   true,
		Symbols: []graph.Symbol{{Package: "example.com/m/gone", Name: "TargetGone", Kind: "func", File: "gone/g.go", Line: 1, EndLine: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := q.Pack(ctx, root, "find the target gone function"); err != nil {
			t.Fatalf("pack %d: %v", i, err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(said); n != 0 {
		t.Fatalf("a vanished file is dropped, never said; got %d lines: %q", n, <-said)
	}
	var left int
	if err := db.QueryRow(`SELECT count(*) FROM symbols WHERE file = 'gone/g.go'`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("the stale file's rows must be dropped, %d remain", left)
	}
}

func TestTheQueueSaysASentenceOnce(t *testing.T) {
	engine := evt.NewEngine()
	go engine.Start(context.Background())
	defer engine.Stop()
	room := broadcast.NewRoom("test", func(id int64) broadcast.Transport {
		return broadcast.NewLoopTransport(id, engine, 0)
	})
	said := make(chan string, 64)
	room.Add(-1).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if n, ok := m.Event().(core.Notice); err == nil && ok {
				said <- n.Text
			}
		}
	})
	q := graph.NewQueue(t.TempDir(), room.Add(0))
	defer q.Close()
	plain := t.TempDir()
	for i := 0; i < 3; i++ {
		q.Touch(filepath.Join(plain, "x.go"))
	}
	q.Drain(context.Background())
	time.Sleep(50 * time.Millisecond)
	if n := len(said); n > 1 {
		t.Fatalf("one sentence is one line, got %d", n)
	}
}
