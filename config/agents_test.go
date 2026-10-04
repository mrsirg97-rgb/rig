package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/config"
)

func TestAgentsIsTheOperatorsFileAlone(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	write(t, dir, "AGENTS.md", "G")
	write(t, cwd, "AGENTS.md", "P")
	cfg := load(t, dir, cwd)
	if cfg.Agents != "G" {
		t.Fatalf("Agents = %q, want the operator's file alone; the project's follows the workspace", cfg.Agents)
	}
}

func TestProjectAgentsReadsTheWorkspacesOwnFile(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "AGENTS.md", "P")
	got, err := config.ProjectAgents(t.TempDir(), cwd)
	if err != nil || got != "P" {
		t.Fatalf("ProjectAgents = (%q, %v), want the cwd's file", got, err)
	}
}

func TestProjectAgentsWalksUpToTheRepoRootAndNoFurther(t *testing.T) {
	above := t.TempDir()
	write(t, above, "AGENTS.md", "ABOVE")
	repo := filepath.Join(above, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "AGENTS.md", "REPO")
	deep := filepath.Join(repo, "store", "graph")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := config.ProjectAgents(t.TempDir(), deep)
	if err != nil || got != "REPO" {
		t.Fatalf("from a subdirectory the repo root's file loads, got (%q, %v)", got, err)
	}
	os.Remove(filepath.Join(repo, "AGENTS.md"))
	got, err = config.ProjectAgents(t.TempDir(), deep)
	if err != nil || got != "" {
		t.Fatalf("the walk stops at the repo root; a file above it is not the project's, got (%q, %v)", got, err)
	}
}

func TestProjectAgentsNearestWinsInsideTheRepo(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "AGENTS.md", "REPO")
	sub := filepath.Join(repo, "cmd")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, sub, "AGENTS.md", "SUB")
	got, err := config.ProjectAgents(t.TempDir(), sub)
	if err != nil || got != "SUB" {
		t.Fatalf("the nearest file wins, got (%q, %v)", got, err)
	}
}

func TestProjectAgentsOutsideARepoReadsOnlyTheCwd(t *testing.T) {
	parent := t.TempDir()
	write(t, parent, "AGENTS.md", "PARENT")
	cwd := filepath.Join(parent, "plain")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := config.ProjectAgents(t.TempDir(), cwd)
	if err != nil || got != "" {
		t.Fatalf("a directory that is no repo reads its own file only, got (%q, %v)", got, err)
	}
}

func TestProjectAgentsNeverDoublesTheOperatorsFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "AGENTS.md", "G")
	got, err := config.ProjectAgents(dir, dir)
	if err != nil || got != "" {
		t.Fatalf("a session in the rig home does not load the operator's file twice, got (%q, %v)", got, err)
	}
}

func TestAgentsUnreadableRefuses(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not refuse")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(p, []byte("G"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o644) })
	err := loadErr(t, dir, t.TempDir())
	if err.Error() != "config: "+p+": permission denied" {
		t.Fatalf("the voice = %q, want the OS reason with the path named once", err)
	}
}

func TestAgentsDirectoryRefuses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "AGENTS.md")
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	err := loadErr(t, dir, t.TempDir())
	if err.Error() != "config: "+p+": is a directory" {
		t.Fatalf("the voice = %q, want the OS reason with the path named once", err)
	}
}
