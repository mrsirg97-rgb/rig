package scope

import (
	"os"
	"path/filepath"
	"testing"
)

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
