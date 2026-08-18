package tui

import (
	"os/exec"
	"strings"
	"testing"
)

// TestFreezeGate is the SPEC_TUI gate, as a test: the diff against
// main is confined to the allowlist (frontend/tui, cmd/rig, docs,
// go.mod, go.sum), core/ and loop/ are byte-identical with main, and
// the CLI's goldens are still green — the CLI is the piped reference
// and this work must not change it.
func TestFreezeGate(t *testing.T) {
	// the repo root from the test's cwd (frontend/tui).
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	root := strings.TrimSpace(string(out))

	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = root
		o, err := c.CombinedOutput()
		if err != nil {
			t.Skipf("git %s: %v\n%s", strings.Join(args, " "), err, o)
		}
		return string(o)
	}

	allow := func(p string) bool {
		return p == "frontend/tui" || strings.HasPrefix(p, "frontend/tui/") ||
			p == "cmd/rig" || strings.HasPrefix(p, "cmd/rig/") ||
			p == "docs" || strings.HasPrefix(p, "docs/") ||
			p == "go.mod" || p == "go.sum"
	}

	// the changed paths, worktree against main (tracked, committed or
	// not) plus the untracked.
	var changed []string
	changed = append(changed, strings.Fields(git("diff", "--name-only", "main", "--"))...)
	for _, line := range strings.Split(git("status", "--porcelain"), "\n") {
		if strings.HasPrefix(line, "?? ") {
			changed = append(changed, strings.TrimSpace(line[3:]))
		}
	}
	for _, p := range changed {
		if !allow(p) {
			t.Errorf("the freeze diff reaches outside the allowlist: %s", p)
		}
	}

	// core/ and loop/ are byte-identical with main.
	if d := strings.TrimSpace(git("diff", "--stat", "main", "--", "core/", "loop/")); d != "" {
		t.Errorf("core/ and loop/ must be byte-identical with main:\n%s", d)
	}

	// the CLI's goldens are still green (the piped reference).
	gotest := exec.Command("go", "test", "./frontend/cli/")
	gotest.Dir = root
	if o, err := gotest.CombinedOutput(); err != nil {
		t.Errorf("the CLI's goldens are not green:\n%s", o)
	}
}
