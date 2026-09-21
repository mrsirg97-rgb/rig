package todo_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/store/scope"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

// The queue a session works in is recorded beside the log, and it moves
// only when the operator says so.
func TestBindingRecordsAndMovesASession(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, ok, err := todostore.BindingOf(ctx, db, "s1"); err != nil || ok {
		t.Fatalf("an unbound session has no binding: %v %v", ok, err)
	}
	if err := todostore.Bind(ctx, db, todostore.Binding{Session: "s1", Scope: "ka", Label: "rig"}); err != nil {
		t.Fatal(err)
	}
	b, ok, err := todostore.BindingOf(ctx, db, "s1")
	if err != nil || !ok || b.Scope != "ka" || b.Project().Label != "rig" {
		t.Fatalf("the binding must round-trip: %+v %v %v", b, ok, err)
	}
	if err := todostore.Bind(ctx, db, todostore.Binding{Session: "s1", Scope: "kb", Label: "loom", OutsideRepo: true}); err != nil {
		t.Fatal(err)
	}
	b, _, err = todostore.BindingOf(ctx, db, "s1")
	if err != nil || b.Scope != "kb" || !b.OutsideRepo {
		t.Fatalf("a rebind moves the session: %+v %v", b, err)
	}
	// The anonymous attribution is shared: binding it would leak one
	// caller's project onto another's.
	if err := todostore.Bind(ctx, db, todostore.Binding{Session: "anon", Scope: "kc", Label: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := todostore.BindingOf(ctx, db, "anon"); err != nil || ok {
		t.Fatalf("anon binds nothing: %v %v", ok, err)
	}
	if err := todostore.Bind(ctx, db, todostore.Binding{Session: "", Scope: "kd", Label: "x"}); err != nil {
		t.Fatalf("an unattributed bind is inert, not an error: %v", err)
	}
}

// Inside a repo the queue's name is the repo's, not the folder the call
// named: a subdirectory and a second worktree read one queue and say the
// same thing.
func TestProjectOfNamesTheRepoNotTheFolderItStartedIn(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"),
		[]byte("#!/bin/sh\necho /work/rig/.git\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	p := todostore.ProjectOf("/work/rig/frontend/web")
	if p.OutsideRepo {
		t.Fatal("a directory inside a repo is not a bucket")
	}
	if p.Label != "rig" {
		t.Fatalf("the label is the repo's name, got %q", p.Label)
	}
	if p.Key != scope.ShortHash("/work/rig/.git") {
		t.Fatalf("the key is the common dir's hash: %s", p.Key)
	}
}

// Outside a repo the queue is a bucket of that directory, marked as one,
// and spelled the same however the caller writes it: "." and its absolute
// path must not mint two buckets for one place.
func TestProjectOfMarksABucketAndKeysItAbsolutely(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	dir := t.TempDir()
	p := todostore.ProjectOf(dir)
	if !p.OutsideRepo {
		t.Fatal("no git: no repo")
	}
	if p.Key != scope.ShortHash(dir) || p.Label != filepath.Base(dir) {
		t.Fatalf("the bucket is the directory: %+v", p)
	}
	rel := "./" + filepath.Base(dir)
	t.Chdir(filepath.Dir(dir))
	if got := todostore.ProjectOf(rel); got.Key != p.Key {
		t.Fatalf("a relative spelling must reach the same bucket: %s vs %s", got.Key, p.Key)
	}
}

// A bare repository is still a repository: its common dir is the cwd
// itself, so the probe cannot tell it apart by path alone, and git says
// so when asked. It gets the repo's own identity and name, not a shared
// cwd bucket.
func TestProjectOfNamesABareRepo(t *testing.T) {
	dir := t.TempDir()
	bare := filepath.Join(dir, "bare.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Skipf("git init --bare: %v %s", err, out)
	}
	p := todostore.ProjectOf(bare)
	if p.OutsideRepo {
		t.Fatalf("a bare repo is a repo, not a bucket: %+v", p)
	}
	if p.Label != "bare.git" {
		t.Fatalf("the label is the repo's own name, got %q", p.Label)
	}
	if p.Key != scope.ShortHash(bare) {
		t.Fatalf("the key is the repo identity: %s", p.Key)
	}
}
