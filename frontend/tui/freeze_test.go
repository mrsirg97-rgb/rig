package tui

import (
	"os/exec"
	"strings"
	"testing"
)

// TestFreezeGate is the SPEC_TUI gate, as a test: the diff against the
// branch's fork point (the merge-base with main) is confined to the
// allowlist (the named work surfaces, last the build surface —
// Makefile, .github/, .gitignore, README.md — SPEC_BUILD), core/ and
// loop/ are identical with that fork point modulo gofmt whitespace (the
// formatting-only drift commit is the named exception), and the CLI's
// goldens are still green — the CLI is the piped reference and this
// work must not change it.
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
			p == "frontend/cli" || strings.HasPrefix(p, "frontend/cli/") ||
			p == "cmd/rig" || strings.HasPrefix(p, "cmd/rig/") ||
			p == "command" || strings.HasPrefix(p, "command/") ||
			p == "store/state" || strings.HasPrefix(p, "store/state/") ||
			p == "tool/diff" || strings.HasPrefix(p, "tool/diff/") ||
			p == "tool/python" || strings.HasPrefix(p, "tool/python/") ||
			p == "plugins" || strings.HasPrefix(p, "plugins/") ||
			p == "config" || strings.HasPrefix(p, "config/") ||
			p == "docs" || strings.HasPrefix(p, "docs/") ||
			p == "specs" || strings.HasPrefix(p, "specs/") ||
			p == "Makefile" || p == ".gitignore" || p == "README.md" ||
			p == ".github" || strings.HasPrefix(p, ".github/") ||
			p == "core" || strings.HasPrefix(p, "core/") ||
			p == "CHANGELOG.md" || p == "ROADMAP.md" ||
			p == "go.mod" || p == "go.sum"
	}

	// the fork point: the branch base (the merge-base with main). main
	// may have advanced past it with unrelated commits; the freeze is
	// measured from where this work started, not from main's tip.
	base := strings.TrimSpace(git("merge-base", "main", "HEAD"))
	if base == "" {
		base = "main"
	}

	// the changed paths, worktree against the fork point (tracked,
	// committed or not) plus the untracked.
	var changed []string
	changed = append(changed, strings.Fields(git("diff", "--name-only", base, "--"))...)
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

	// core/ and loop/ are identical with the fork point modulo gofmt
	// whitespace: -w drops the alignment deltas, keeps every real change.
	if d := strings.TrimSpace(git("diff", "-w", "--stat", base, "--", "core/", "loop/")); d != "" {
		t.Errorf("core/ and loop/ must be identical with the fork point modulo gofmt whitespace:\n%s", d)
	}

	// the CLI's goldens are still green (the piped reference).
	gotest := exec.Command("go", "test", "./frontend/cli/")
	gotest.Dir = root
	if o, err := gotest.CombinedOutput(); err != nil {
		t.Errorf("the CLI's goldens are not green:\n%s", o)
	}
}
