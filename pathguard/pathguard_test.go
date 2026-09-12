package pathguard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/pathguard"
)

func realRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCanonicalResolvesSymlinksAndRequiresADirectory(t *testing.T) {
	root := realRoot(t)
	dir := filepath.Join(root, "work")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := pathguard.Canonical(dir)
	if err != nil {
		t.Fatalf("Canonical(%q): %v", dir, err)
	}
	if got != dir {
		t.Fatalf("a real directory canonicalizes to itself: %q, want %q", got, dir)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	got, err = pathguard.Canonical(link)
	if err != nil {
		t.Fatalf("Canonical(%q): %v", link, err)
	}
	if got != dir {
		t.Fatalf("a symlink canonicalizes to its target: %q, want %q", got, dir)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pathguard.Canonical(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("a file must refuse naming the directory rule, got %v", err)
	}
	if _, err := pathguard.Canonical(filepath.Join(root, "missing")); err == nil {
		t.Fatal("a missing path must refuse")
	}
}

func TestWithinAcceptsAChildOfTheSessionCwdAndTheRigHome(t *testing.T) {
	session := realRoot(t)
	work := filepath.Join(session, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := pathguard.Within(work, session, filepath.Join(session, "nowhere"))
	if err != nil || got != work {
		t.Fatalf("a child of the session cwd must canonicalize inside, got %q, %v", got, err)
	}
	rigHome := realRoot(t)
	got, err = pathguard.Within(rigHome, session, rigHome)
	if err != nil || got != rigHome {
		t.Fatalf("the rig home itself must be accepted, got %q, %v", got, err)
	}
}

func TestWithinAcceptsBothFormsOfASymlinkedSessionCwd(t *testing.T) {
	root := realRoot(t)
	real := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(real, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	rigHome := realRoot(t)
	got, err := pathguard.Within(child, link, rigHome)
	if err != nil || got != child {
		t.Fatalf("the resolved form under a symlinked session cwd must accept: %q, %v", got, err)
	}
	got, err = pathguard.Within(filepath.Join(link, "child"), real, rigHome)
	if err != nil || got != child {
		t.Fatalf("the symlink form under a resolved session cwd must accept: %q, %v", got, err)
	}
}

func TestWithinRefusesOutsideTheRoots(t *testing.T) {
	session := realRoot(t)
	rigHome := realRoot(t)
	_, err := pathguard.Within("/etc", session, rigHome)
	if err == nil || !strings.Contains(err.Error(), "outside the session's cwd") {
		t.Fatalf("a path outside both roots must refuse naming the rule, got %v", err)
	}
}

func TestWithinRefusesASymlinkEscape(t *testing.T) {
	session := realRoot(t)
	outside := realRoot(t)
	link := filepath.Join(session, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	_, err := pathguard.Within(link, session, filepath.Join(session, "nowhere"))
	if err == nil || !strings.Contains(err.Error(), "outside the session's cwd") {
		t.Fatalf("a lexical child that resolves outside must refuse, got %v", err)
	}
}

func TestWithinRefusesAFile(t *testing.T) {
	session := realRoot(t)
	file := filepath.Join(session, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := pathguard.Within(file, session, filepath.Join(session, "nowhere"))
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("a file must refuse naming the directory rule, got %v", err)
	}
}
