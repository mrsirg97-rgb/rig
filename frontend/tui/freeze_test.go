package tui

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreezeGate(t *testing.T) {

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

			p == "frontend/web" || strings.HasPrefix(p, "frontend/web/") ||
			p == "cmd/rig" || strings.HasPrefix(p, "cmd/rig/") ||
			p == "command" || strings.HasPrefix(p, "command/") ||
			p == "store/state" || strings.HasPrefix(p, "store/state/") ||
			p == "tool/diff" || strings.HasPrefix(p, "tool/diff/") ||
			p == "tool/python" || strings.HasPrefix(p, "tool/python/") ||
			p == "tool/bash" || strings.HasPrefix(p, "tool/bash/") ||
			p == "tool/fs" || strings.HasPrefix(p, "tool/fs/") ||
			p == "tool/web" || strings.HasPrefix(p, "tool/web/") ||

			p == "store" || strings.HasPrefix(p, "store/") ||
			p == "tool/bash" || strings.HasPrefix(p, "tool/bash/") ||
			p == "tool/file" || strings.HasPrefix(p, "tool/file/") ||
			p == "tool/rem" || strings.HasPrefix(p, "tool/rem/") ||

			p == "store/todo" || strings.HasPrefix(p, "store/todo/") ||
			p == "tool/todo" || strings.HasPrefix(p, "tool/todo/") ||

			p == "tool/scheduler" || strings.HasPrefix(p, "tool/scheduler/") ||

			p == "tool/delegate" || strings.HasPrefix(p, "tool/delegate/") ||

			p == "middleware/perm" || strings.HasPrefix(p, "middleware/perm/") ||
			p == "plugins" || strings.HasPrefix(p, "plugins/") ||

			p == "middleware/toolset" || strings.HasPrefix(p, "middleware/toolset/") ||

			p == "middleware/approve" || strings.HasPrefix(p, "middleware/approve/") ||

			p == "evt" || strings.HasPrefix(p, "evt/") ||

			p == "middleware/guard" || strings.HasPrefix(p, "middleware/guard/") ||

			p == "store/scheduler" || strings.HasPrefix(p, "store/scheduler/") ||
			p == "provider" || strings.HasPrefix(p, "provider/") ||
			p == "config" || strings.HasPrefix(p, "config/") ||
			p == "models" || strings.HasPrefix(p, "models/") ||
			p == "policy" || strings.HasPrefix(p, "policy/") ||
			p == "docs" || strings.HasPrefix(p, "docs/") ||
			p == "specs" || strings.HasPrefix(p, "specs/") ||
			p == "AGENTS.md" ||
			p == "Makefile" || p == ".gitignore" || p == "README.md" ||
			p == ".github" || strings.HasPrefix(p, ".github/") ||

			p == "install.sh" || p == "site" || strings.HasPrefix(p, "site/") ||
			p == "core" || strings.HasPrefix(p, "core/") ||
			p == "CHANGELOG.md" || p == "ROADMAP.md" ||
			p == "PACKAGE.md" || strings.HasSuffix(p, "/PACKAGE.md") ||
			p == "go.mod" || p == "go.sum"
	}

	base := strings.TrimSpace(git("merge-base", "main", "HEAD"))
	if base == "" {
		base = "main"
	}

	if !strings.Contains(git("branch", "--show-current"), "-refactor") {

		var changed []string
		changed = append(changed, strings.Fields(git("diff", "--name-only", base, "--"))...)
		for _, line := range strings.Split(git("status", "--porcelain"), "\n") {
			if strings.HasPrefix(line, "?? ") {
				changed = append(changed, strings.TrimSpace(line[3:]))
			}
		}
		for _, p := range changed {
			if commentOnly(root, base, p) {
				continue
			}
			if !allow(p) {
				t.Errorf("the freeze diff reaches outside the allowlist: %s", p)
			}
		}

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
			oldF, oldFErr := stripped(oldB)
			newF, newFErr := stripped(newB)
			if oldFErr != nil || newFErr != nil {
				t.Errorf("core/ or loop/ gofmt refused: %s (%v; %v)", p, oldFErr, newFErr)
				continue
			}
			if !bytes.Equal(oldF, newF) {
				t.Errorf("core/ or loop/ changed beyond gofmt: %s (the formatted sides differ)", p)
			}
		}
	}

	gotest := exec.Command("go", "test", "./frontend/cli/")
	gotest.Dir = root
	if o, err := gotest.CombinedOutput(); err != nil {
		t.Errorf("the CLI's goldens are not green:\n%s", o)
	}
}

func stripped(src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil, err
	}
	f.Comments = nil
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.GenDecl:
			x.Doc = nil
		case *ast.FuncDecl:
			x.Doc = nil
		case *ast.Field:
			x.Doc, x.Comment = nil, nil
		case *ast.TypeSpec:
			x.Doc, x.Comment = nil, nil
		case *ast.ValueSpec:
			x.Doc, x.Comment = nil, nil
		case *ast.ImportSpec:
			x.Doc, x.Comment = nil, nil
		}
		return true
	})
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, err
	}
	return format.Source(buf.Bytes())
}

func commentOnly(root, base, p string) bool {
	if !strings.HasSuffix(p, ".go") {
		return false
	}
	c := exec.Command("git", "show", base+":"+p)
	c.Dir = root
	oldB, err := c.Output()
	if err != nil {
		return false
	}
	newB, err := os.ReadFile(filepath.Join(root, p))
	if err != nil {
		return false
	}
	oldF, err := stripped(oldB)
	if err != nil {
		return false
	}
	newF, err := stripped(newB)
	if err != nil {
		return false
	}
	return bytes.Equal(oldF, newF)
}
