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

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/evt"
	"github.com/mrsirg97-rgb/rig/v2/store/graph"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
)

func TestIndexMapsEveryFileOfTheProject(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		dir := filepath.Join(root, fmt.Sprintf("p%02d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 10; j++ {
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
	if !strings.Contains(reply, "mapped 400") {
		t.Fatalf("index reply must name the count mapped: %s", reply)
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
	if rows != 400 {
		t.Fatalf("files rows = %d, want 400 (the walk must never drop a file)", rows)
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
			if n, ok := m.Event().(core.Notice); err == nil && ok {
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
