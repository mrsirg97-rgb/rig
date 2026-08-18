package config_test

import (
	"os"
	"path/filepath"
	"testing"
)

// --- the named cases (SPEC_CONFIG, testing) ---

// TestAgentsGlobalThenProject (SPEC_CONFIG 6): global then project,
// concatenated global-first with a blank line between; the content is
// the files as written.
func TestAgentsGlobalThenProject(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	write(t, dir, "AGENTS.md", "G")
	write(t, cwd, "AGENTS.md", "P")
	cfg := load(t, dir, cwd)
	if cfg.Agents != "G\n\nP" {
		t.Fatalf("Agents = %q, want %q (global first, blank line between)", cfg.Agents, "G\n\nP")
	}
}

func TestAgentsProjectOnly(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "AGENTS.md", "P")
	cfg := load(t, t.TempDir(), cwd)
	if cfg.Agents != "P" {
		t.Fatalf("Agents = %q, want %q (no stray global segment)", cfg.Agents, "P")
	}
}

func TestAgentsGlobalOnly(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "AGENTS.md", "G")
	cfg := load(t, dir, t.TempDir())
	if cfg.Agents != "G" {
		t.Fatalf("Agents = %q, want %q (no stray project segment)", cfg.Agents, "G")
	}
}

// TestAgentsUnreadableRefuses (SPEC_CONFIG 3): a present-but-unreadable
// AGENTS.md refuses with the OS reason, the path named once.
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

// TestAgentsDirectoryRefuses (SPEC_CONFIG 3): a directory by that name
// is not a file — the refusal is loud.
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
