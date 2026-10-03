package graph_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/store/graph"
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
