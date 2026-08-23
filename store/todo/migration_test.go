package todo_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/scope"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "-c", "user.email=test@rig", "-c", "user.name=rig", "add", "-A")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v (%s)", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "-c", "user.email=test@rig", "-c", "user.name=rig", "commit", "-q", "-m", "seed")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v (%s)", err, out)
	}
}

func proj(cwd string) todostore.Project {
	return todostore.Project{Key: scope.Key(cwd), Label: scope.Label(cwd)}
}

func TestQueueReadsIdenticallyAcrossRepoSubdirAndWorktree(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(repo, "wt")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v (%s)", err, out)
	}
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, proj(repo), []item{{Text: "shared plan"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{repo, sub, wt} {
		reply, err := todostore.Read(ctx, db, proj(cwd), "s1")
		if err != nil {
			t.Fatalf("read from %s: %v", cwd, err)
		}
		if !strings.Contains(reply, "shared plan") {
			t.Fatalf("a queue created at the repo root must read from %s:\n%s", cwd, reply)
		}
	}
	if scope.Key(repo) != scope.Key(sub) || scope.Key(repo) != scope.Key(wt) {
		t.Fatalf("subdir and worktree must share the repo scope")
	}
}

func TestNonRepoDirKeepsItsOwnQueue(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	d1 := filepath.Join(t.TempDir(), "one")
	d2 := filepath.Join(t.TempDir(), "two")
	os.MkdirAll(d1, 0o755)
	os.MkdirAll(d2, 0o755)
	if _, err := todostore.Create(ctx, db, proj(d1), []item{{Text: "only in one"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	reply, err := todostore.Read(ctx, db, proj(d2), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reply, "only in one") {
		t.Fatalf("d2 saw d1's tasks:\n%s", reply)
	}
	if scope.Key(d1) != scope.ShortHash(d1) {
		t.Fatalf("outside a repo the scope is the cwd, hashed: %q != %q", scope.Key(d1), scope.ShortHash(d1))
	}
}

func TestIdsArePerScope(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	pA := todostore.Project{Key: "scope-a", Label: "a"}
	pB := todostore.Project{Key: "scope-b", Label: "b"}
	if _, err := todostore.Create(ctx, db, pA, []item{{Text: "a-one"}, {Text: "a-two"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Create(ctx, db, pB, []item{{Text: "b-one"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	ra, err := todostore.Read(ctx, db, pA, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ra, "t1") || !strings.Contains(ra, "t2") {
		t.Fatalf("scope a ids lost:\n%s", ra)
	}
	rb, err := todostore.Read(ctx, db, pB, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rb, "t1") {
		t.Fatalf("two projects must both mint a t1:\n%s", rb)
	}
	if strings.Contains(rb, "t2") {
		t.Fatalf("scope b must not see scope a's t2:\n%s", rb)
	}
}

func legacyDDL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS "events" (
  "seq" INTEGER NOT NULL,
  "args" TEXT NOT NULL,
  "op" TEXT NOT NULL,
  "session" TEXT,
  "ts" TEXT NOT NULL,
  PRIMARY KEY ("seq")
)`,
		`CREATE TABLE IF NOT EXISTS "meta" (
  "key" TEXT NOT NULL,
  "value" TEXT NOT NULL,
  PRIMARY KEY ("key")
)`,
	}
}

func seedLegacy(t *testing.T, dir, hash string, texts []string) {
	t.Helper()
	path := filepath.Join(dir, hash+".sqlite")
	db, _, _, err := store.Open(path, legacyDDL(), 1)
	if err != nil {
		t.Fatalf("seed legacy %s: %v", hash, err)
	}
	var tasks []map[string]any
	for _, text := range texts {
		tasks = append(tasks, map[string]any{"text": text})
	}
	args, _ := json.Marshal(map[string]any{"tasks": tasks})
	if _, err := db.DB.Exec("INSERT INTO events (ts, op, args, session) VALUES ('2025-01-01T00:00:00Z', 'create', ?, 's1')", string(args)); err != nil {
		t.Fatalf("seed legacy event: %v", err)
	}
	if err := db.DB.Close(); err != nil {
		t.Fatal(err)
	}
}

func eventsCount(t *testing.T, db store.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM events").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFoldPreservesBothQueuesByteForByteAndIsNoOpOnSecondOpen(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir, 0o755)
	seedLegacy(t, dir, "aaaa1111bbbb", []string{"from-a", "a-two"})
	seedLegacy(t, dir, "cccc2222dddd", []string{"from-b"})

	path := filepath.Join(dir, "todo.sqlite")
	db, _, report, err := store.Open(path, todostore.Statements(), todostore.SchemaVersion, todostore.Migration("", dir))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	if !strings.Contains(report, "folded 2 events from 2 stores") {
		t.Fatalf("migration report %q, want the fold counted", report)
	}
	ctx := context.Background()
	ra, err := todostore.Read(ctx, db, todostore.Project{Key: "aaaa1111bbbb", Label: "a"}, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ra, "from-a") || !strings.Contains(ra, "a-two") || !strings.Contains(ra, "t1") || !strings.Contains(ra, "t2") {
		t.Fatalf("queue a lost:\n%s", ra)
	}
	rb, err := todostore.Read(ctx, db, todostore.Project{Key: "cccc2222dddd", Label: "b"}, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rb, "from-b") || !strings.Contains(rb, "t1") {
		t.Fatalf("queue b lost:\n%s", rb)
	}
	if strings.Contains(rb, "a-two") {
		t.Fatalf("queue b must not see queue a:\n%s", rb)
	}
	for _, name := range []string{"aaaa1111bbbb.sqlite.migrated", "cccc2222dddd.sqlite.migrated"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("old store not moved aside: %s", name)
		}
	}

	db2, _, report2, err := store.Open(path, todostore.Statements(), todostore.SchemaVersion, todostore.Migration("", dir))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.DB.Close()
	if report2 != "" {
		t.Fatalf("the fold must be a no-op on the second open, got %q", report2)
	}
	if eventsCount(t, db2) != 2 {
		t.Fatalf("a second open must not fold again: %d events", eventsCount(t, db2))
	}
}

func TestLazyRescopeMovesCwdHashQueueToRepoOnce(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	cwd := filepath.Join(repo, "src")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.MkdirAll(dir, 0o755)
	oldScope := scope.ShortHash(cwd)
	seedLegacy(t, dir, oldScope, []string{"old queue"})

	path := filepath.Join(dir, "todo.sqlite")
	db, _, report, err := store.Open(path, todostore.Statements(), todostore.SchemaVersion, todostore.Migration(cwd, dir))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	if !strings.Contains(report, "re-scoped 1 events") {
		t.Fatalf("the migration must count the re-scope once: %q", report)
	}
	ctx := context.Background()
	reply, err := todostore.Read(ctx, db, proj(cwd), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "old queue") {
		t.Fatalf("the cwd-hash queue must move to the repo scope:\n%s", reply)
	}
	if scope.Key(cwd) == oldScope {
		t.Fatalf("the test needs a repo scope distinct from the cwd hash")
	}

	db2, _, report2, err := store.Open(path, todostore.Statements(), todostore.SchemaVersion, todostore.Migration(cwd, dir))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.DB.Close()
	if report2 != "" {
		t.Fatalf("the re-scope must be idempotent, second open reports %q", report2)
	}
}
