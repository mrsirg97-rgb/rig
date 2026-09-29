package main

import (
	"os/exec"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	for _, a := range [][]string{
		{"init"},
		{"config", "user.name", "rig test"},
		{"config", "user.email", "rig@test"},
	} {
		gitIn(t, dir, a...)
	}
}

const notARepoSentence = "It is not a repo: name project on todo and rem calls, as a path to the repo the work is in."

func TestSessionSectionNamesANonRepoCwd(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	got := sessionSection(cwd, home)
	want := "The session's working directory is " + cwd + " and the session home is " + home +
		". A leading ~ in a tool path expands to the session home. " + notARepoSentence
	if got != want {
		t.Fatalf("the session line outside a repo = %q, want %q (the cwd, the home, and the name-project sentence)", got, want)
	}
}

func TestSessionSectionStaysSilentInsideARepo(t *testing.T) {
	cwd := t.TempDir()
	initRepo(t, cwd)
	home := t.TempDir()
	got := sessionSection(cwd, home)
	want := "The session's working directory is " + cwd + " and the session home is " + home +
		". A leading ~ in a tool path expands to the session home."
	if got != want {
		t.Fatalf("the session line inside a repo = %q, want %q (no name-project sentence)", got, want)
	}
	if strings.Contains(got, notARepoSentence) {
		t.Fatalf("a repo cwd must not carry the name-project sentence: %q", got)
	}
}
