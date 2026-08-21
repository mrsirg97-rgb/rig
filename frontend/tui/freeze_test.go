package tui

import (
	"bytes"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFreezeGate is the SPEC_TUI gate, as a test: the diff against the
// branch's fork point (the merge-base with main) is confined to the
// allowlist (the named work surfaces, last the build surface —
// Makefile, .github/, .gitignore, README.md — SPEC_BUILD), core/ and
// loop/ are identical with that fork point modulo gofmt (both sides
// formatted, compared byte-exact — gofmt never touches string
// contents, so a voice's spacing still shows; strict byte-identity
// would deadlock the day a toolchain re-aligns, this absorbs it), the
// formatting-only drift commit being the named exception, and the
// CLI's goldens are still green — the CLI is the piped reference and
// this work must not change it.
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
			// the dashboard round (SPEC_SERVE): the web leaf and its
			// store verbs, beside the frontends.
			p == "frontend/web" || strings.HasPrefix(p, "frontend/web/") ||
			p == "cmd/rig" || strings.HasPrefix(p, "cmd/rig/") ||
			p == "command" || strings.HasPrefix(p, "command/") ||
			p == "store/state" || strings.HasPrefix(p, "store/state/") ||
			p == "tool/diff" || strings.HasPrefix(p, "tool/diff/") ||
			p == "tool/python" || strings.HasPrefix(p, "tool/python/") ||
			// the field-test round's work surfaces (SPEC_UX): the rem
			// door, the bash and file tools (the todo half withdrawn on
			// review - SPEC_UX 1, amended).
			p == "store/rem" || strings.HasPrefix(p, "store/rem/") ||
			p == "tool/bash" || strings.HasPrefix(p, "tool/bash/") ||
			p == "tool/file" || strings.HasPrefix(p, "tool/file/") ||
			p == "tool/rem" || strings.HasPrefix(p, "tool/rem/") ||
			// the lean-read round (SPEC_TODO_LEAN): the todo store's
			// filtered render and echo, and the tool's all surface.
			p == "store/todo" || strings.HasPrefix(p, "store/todo/") ||
			p == "tool/todo" || strings.HasPrefix(p, "tool/todo/") ||
			// the streamlined round (SPEC_STREAMLINE 1): the scheduler
			// description's de-duplication, beside its store.
			p == "tool/scheduler" || strings.HasPrefix(p, "tool/scheduler/") ||
			// the provenance rule's work surface (SPEC_SANDBOX 2): the
			// perm's one new path rule, beside the allow-list.
			p == "middleware/perm" || strings.HasPrefix(p, "middleware/perm/") ||
			p == "plugins" || strings.HasPrefix(p, "plugins/") ||
			// the reload's seam (SPEC_PLUGINS 8): the toolset table and
			// its two adapters, beside the perm's.
			p == "middleware/toolset" || strings.HasPrefix(p, "middleware/toolset/") ||
			// the approval gate (SPEC_MODES 4): the modes branch's leaf.
			p == "middleware/approve" || strings.HasPrefix(p, "middleware/approve/") ||
			// the retry guard's streak rule (SPEC_HARDENING 7, amended):
			// the corrected-call reset and its named cases.
			p == "middleware/guard" || strings.HasPrefix(p, "middleware/guard/") ||
			// the worker jail's work surface (SPEC_SANDBOX 1, 5): the
			// runner's bwrap spawn and socket proxy, the provider's
			// unix: base URL.
			p == "store/scheduler" || strings.HasPrefix(p, "store/scheduler/") ||
			p == "provider" || strings.HasPrefix(p, "provider/") ||
			p == "config" || strings.HasPrefix(p, "config/") ||
			p == "models" || strings.HasPrefix(p, "models/") ||
			p == "policy" || strings.HasPrefix(p, "policy/") ||
			p == "docs" || strings.HasPrefix(p, "docs/") ||
			p == "specs" || strings.HasPrefix(p, "specs/") ||
			p == "Makefile" || p == ".gitignore" || p == "README.md" ||
			p == ".github" || strings.HasPrefix(p, ".github/") ||
			// the distribution round (SPEC_BUILD 5): the installer and
			// the site, beside the release/pages workflows.
			p == "install.sh" || p == "site" || strings.HasPrefix(p, "site/") ||
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

	// a refactor branch (rig-*-refactor) refactors one package wholesale,
	// so neither the TUI allowlist nor the core/loop frozen-surface clause
	// applies — only the CLI goldens must stay green.
	if !strings.Contains(git("branch", "--show-current"), "-refactor") {
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

		// core/ and loop/ are identical with the fork point modulo gofmt:
		// format each side (gofmt never touches string contents), then
		// compare byte-exact. A voice's spacing still shows; an alignment
		// gofmt itself would change is absorbed (strict byte-identity
		// would deadlock on the day a toolchain re-aligns).
		for _, p := range strings.Fields(git("diff", "--name-only", base, "--", "core/", "loop/")) {
			if !strings.HasSuffix(p, ".go") {
				t.Errorf("core/ or loop/ gained a non-Go file: %s (a real change to the frozen surface)", p)
				continue
			}
			c := exec.Command("git", "show", base+":"+p)
			c.Dir = root
			oldB, oldErr := c.Output()
			newB, newErr := os.ReadFile(filepath.Join(root, p))
			if (oldErr == nil) != (newErr == nil) {
				side := "gained"
				if oldErr == nil {
					side = "lost"
				}
				t.Errorf("core/ or loop/ %s a file: %s (a real change to the frozen surface)", side, p)
				continue
			}
			if oldErr != nil {
				t.Errorf("core/ or loop/ changed beyond gofmt: %s (absent from both sides: %v; %v)", p, oldErr, newErr)
				continue
			}
			oldF, oldFErr := format.Source(oldB)
			newF, newFErr := format.Source(newB)
			if oldFErr != nil || newFErr != nil {
				t.Errorf("core/ or loop/ gofmt refused: %s (%v; %v)", p, oldFErr, newFErr)
				continue
			}
			if !bytes.Equal(oldF, newF) {
				t.Errorf("core/ or loop/ changed beyond gofmt: %s (the formatted sides differ)", p)
			}
		}
	}

	// the CLI's goldens are still green (the piped reference).
	gotest := exec.Command("go", "test", "./frontend/cli/")
	gotest.Dir = root
	if o, err := gotest.CombinedOutput(); err != nil {
		t.Errorf("the CLI's goldens are not green:\n%s", o)
	}
}
