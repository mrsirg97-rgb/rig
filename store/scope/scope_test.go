package scope

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("-C", dir, "init", "-q")
	run("-C", dir, "-c", "user.email=test@rig", "-c", "user.name=rig", "add", "-A")
	run("-C", dir, "-c", "user.email=test@rig", "-c", "user.name=rig", "commit", "-q", "-m", "seed")
}

func TestWorktreeNamesTheCheckout(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "wt-main")
	linked := filepath.Join(base, "wt-side")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, main)
	if out, err := exec.Command("git", "-C", main, "worktree", "add", "-q", linked, "-b", "side").CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v (%s)", err, out)
	}
	if got := Worktree(main); got != "wt-main" {
		t.Fatalf("the main checkout keys %q, want wt-main", got)
	}
	if got := Worktree(linked); got != "wt-side" {
		t.Fatalf("the linked worktree keys %q, want wt-side (the worktrees leaf)", got)
	}
	if Key(main) != Key(linked) {
		t.Fatalf("the two worktrees must share the repo scope: %q != %q", Key(main), Key(linked))
	}
	plain := filepath.Join(base, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Worktree(plain); got != "plain" {
		t.Fatalf("outside a repo the worktree is the directory: %q, want plain", got)
	}
}

func TestShortHashIsTheCwdFallback(t *testing.T) {
	if Key("/some/non-repo/dir") != ShortHash("/some/non-repo/dir") {
		t.Fatal("outside a repo the scope is the cwd, hashed")
	}
}

func TestScopeResolvesRelativeGitOutput(t *testing.T) {
	bin := t.TempDir()
	fake := filepath.Join(bin, "git")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho ../.git\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cwd := filepath.Join(t.TempDir(), "sub")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean(filepath.Join(cwd, "../.git"))
	if got := Path(cwd); got != want {
		t.Fatalf("a relative common dir must resolve against the cwd: %q != %q", got, want)
	}
	if !filepath.IsAbs(Path(cwd)) {
		t.Fatal("the scope path must be absolute")
	}
}

func TestScopeIgnoresEchoedOptions(t *testing.T) {
	bin := t.TempDir()
	fake := filepath.Join(bin, "git")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho -- --path-format=absolute\necho .git\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cwd := t.TempDir()
	if got := Path(cwd); got != cwd {
		t.Fatalf("an echoed option is not a path; the scope must fall back to the cwd: %q", got)
	}
}

func TestLabel(t *testing.T) {
	if Label("") != "root" || Label(".") != "root" {
		t.Fatal("the empty and dot labels must read root")
	}
	if Label("/a/b") != "b" {
		t.Fatal("the label is the path's base")
	}
}

func TestScopeResolvesSymlinksToOneKey(t *testing.T) {
	bin := t.TempDir()
	fake := filepath.Join(bin, "git")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho .git\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	base := t.TempDir()
	real := filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(real, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if Key(link) != Key(real) {
		t.Fatalf("a symlinked cwd must share the repo's key: %q != %q", Path(link), Path(real))
	}
	if got, err := filepath.EvalSymlinks(real); err == nil && Path(real) != filepath.Join(got, ".git") {
		t.Fatalf("the scope path must be the real path: %q", Path(real))
	}
}

func TestGlobalIsAFixedKeyNeverAHash(t *testing.T) {
	if Key(Global) != Global {
		t.Fatalf("the reserved word is the fixed global key, got %q", Key(Global))
	}
	if Key(Global) == ShortHash(Global) {
		t.Fatal("global must never be hashed")
	}
	if Path(Global) != Global {
		t.Fatalf("the reserved word must not be git-probed, got %q", Path(Global))
	}
	if Label(Global) != "global" {
		t.Fatalf("the global label is global, got %q", Label(Global))
	}
}
