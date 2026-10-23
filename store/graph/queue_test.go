package graph_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/store/graph"
	"github.com/mrsirg97-rgb/rig/v3/store/scope"
)

func TestIndexMapsEveryFileOfTheProject(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const packages, perPackage = 8, 5
	for i := 0; i < packages; i++ {
		dir := filepath.Join(root, fmt.Sprintf("p%02d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < perPackage; j++ {
			src := fmt.Sprintf("package p%02d\n\nfunc F%d() int { return %d }\n", i, j, j)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.go", j)), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	home := t.TempDir()
	q := graph.NewQueue(home, nil)
	reply, err := q.IndexProject(context.Background(), root)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if want := fmt.Sprintf("mapped %d", packages*perPackage); !strings.Contains(reply, want) {
		t.Fatalf("index reply must name the count mapped (%s): %s", want, reply)
	}
	db, err := graph.Open(home, scope.Key(root), scope.Worktree(root))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	var rows int
	if err := db.QueryRow("SELECT count(*) FROM files").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if want := packages * perPackage; rows != want {
		t.Fatalf("files rows = %d, want %d (the walk must never drop a file)", rows, want)
	}
}

func worktreePair(t *testing.T) (main, linked string) {
	t.Helper()
	base := t.TempDir()
	main = filepath.Join(base, "wt-main")
	linked = filepath.Join(base, "wt-side")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	seed := filepath.Join(main, "p", "a.go")
	if err := os.MkdirAll(filepath.Dir(seed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seed, []byte("package p\n\nfunc A() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("-C", main, "init", "-q")
	run("-C", main, "-c", "user.email=test@rig", "-c", "user.name=rig", "add", "-A")
	run("-C", main, "-c", "user.email=test@rig", "-c", "user.name=rig", "commit", "-q", "-m", "seed")
	run("-C", main, "worktree", "add", "-q", linked, "-b", "side")
	return main, linked
}

func TestStoreSitsPerWorktree(t *testing.T) {
	main, linked := worktreePair(t)
	home := t.TempDir()
	ctx := context.Background()
	q := graph.NewQueue(home, nil)
	if _, err := q.IndexProject(ctx, main); err != nil {
		t.Fatalf("index main: %v", err)
	}
	mainPath := graph.FilePath(home, scope.Key(main), scope.Worktree(main))
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("the main checkout's store is missing (%s): %v", mainPath, err)
	}
	linkedPath := graph.FilePath(home, scope.Key(linked), scope.Worktree(linked))
	if _, err := os.Stat(linkedPath); err == nil {
		t.Fatal("the linked worktree must not share the main checkout's store")
	}
	if _, err := q.IndexProject(ctx, linked); err != nil {
		t.Fatalf("index linked: %v", err)
	}
	if _, err := os.Stat(linkedPath); err != nil {
		t.Fatalf("the linked worktree's store is missing (%s): %v", linkedPath, err)
	}
}

func TestUnboundedPackCapsRefuseAtConstruction(t *testing.T) {
	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("zero caps need the bound named")
		}
	}()
	graph.NewQueue(t.TempDir(), nil, graph.WithPackCaps(0, 1))
}

func TestIndexRefusesADirectoryThatIsNoProject(t *testing.T) {
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	q := graph.NewQueue(t.TempDir(), nil)
	_, err := q.IndexProject(context.Background(), plain)
	if err == nil || !strings.Contains(err.Error(), "is not a project") || !strings.Contains(err.Error(), plain) {
		t.Fatalf("a directory with no go.mod above it and no git worktree must refuse by name, got %v", err)
	}
}

func TestIndexStopsAtTheFirstCancelledFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%02d.go", i)), []byte("package m\n"), 0o644); err != nil {
			t.Fatal(err)
		}
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
			if n, ok := m.Signal().(evt.Notice); err == nil && ok {
				said <- n.Text
			}
		}
	})
	q := graph.NewQueue(t.TempDir(), room.Add(0))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := q.IndexProject(ctx, root)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("a cancelled index ends with the context's error once, got %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(said); n != 0 {
		t.Fatalf("a cancelled walk says nothing per file, got %d lines", n)
	}
}

func TestDropFileTakesTheLexicalRowsWithIt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package alpha\n\nfunc Target() int { return Helper() }\n\nfunc Helper() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	q := graph.NewQueue(home, nil)
	defer q.Close()
	ctx := context.Background()
	if _, err := q.IndexProject(ctx, root); err != nil {
		t.Fatal(err)
	}
	db, err := graph.Open(home, scope.Key(root), scope.Worktree(root))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ftsRows int
	if err := db.QueryRow(`SELECT count(*) FROM symbol_fts WHERE file = 'a.go'`).Scan(&ftsRows); err != nil {
		t.Fatal(err)
	}
	if ftsRows != 2 {
		t.Fatalf("the fixture must map two symbols, got %d fts rows", ftsRows)
	}
	if err := os.Remove(filepath.Join(root, "a.go")); err != nil {
		t.Fatal(err)
	}
	q.Touch(filepath.Join(root, "a.go"))
	q.Drain(ctx)
	for _, probe := range []struct{ table, column string }{
		{"symbols", "file"}, {"symbol_fts", "file"}, {"symbol_grams", "file"}, {"files", "path"},
	} {
		var left int
		if err := db.QueryRow(`SELECT count(*) FROM ` + probe.table + ` WHERE ` + probe.column + ` = 'a.go'`).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left != 0 {
			t.Fatalf("the dropped file must leave no %s rows, %d remain", probe.table, left)
		}
	}
	if _, err := q.Pack(ctx, root, "find the target helper"); err == nil || !strings.Contains(err.Error(), "no symbol in the map matches") {
		t.Fatalf("a dropped file must leave no zombie candidate, got %v", err)
	}
}

func TestARepointedServerGivesTheGoneLanguageAnotherChance(t *testing.T) {
	root, q := lspProject(t)
	defer q.Close()
	q.SetServer("typescript", filepath.Join(t.TempDir(), "no-such-language-server"), "--stdio")
	ctx := context.Background()
	q.Touch(filepath.Join(root, "src", "widget.ts"))
	q.Drain(ctx)
	db, err := graph.Open(q.Home(), scope.Key(root), scope.Worktree(root))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mapped int
	if err := db.QueryRow(`SELECT count(*) FROM symbols`).Scan(&mapped); err != nil {
		t.Fatal(err)
	}
	if mapped != 0 {
		t.Fatalf("the missing server maps nothing, got %d rows", mapped)
	}
	syms := filepath.Join(t.TempDir(), "syms.json")
	writeFake(t, syms, widgetSymbols)
	q.SetServer("typescript", fakeServer(t, "typescript", fakeLog(t), syms, "")...)
	q.Touch(filepath.Join(root, "src", "widget.ts"))
	q.Drain(ctx)
	if err := db.QueryRow(`SELECT count(*) FROM symbols WHERE name = 'Widget'`).Scan(&mapped); err != nil {
		t.Fatal(err)
	}
	if mapped == 0 {
		t.Fatalf("a re-pointed server must restart the gone language")
	}
}
