package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAllowCarriesPrefixesExactFilesGlobsAndReopens(t *testing.T) {
	data := []byte("# the gate\n\nfrontend/tui/\nAGENTS.md\n*/PACKAGE.md\nPACKAGE.md\nreopen loop/ 1.1.4\nreopen kernel.go 2.0.1\n")
	a, err := parseAllow(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"frontend/tui", "frontend/tui/x.go", "AGENTS.md", "PACKAGE.md", "store/rem/PACKAGE.md", "loop", "loop/x.go", "kernel.go"} {
		if !a.allows(p) && !a.reopened(p) {
			t.Errorf("%q must match", p)
		}
	}
	for _, p := range []string{"frontend/tuix/x.go", "frontendx/x.go", "AGENTS.md/x", "kernel.go/x", "loopx", "PACKAGE.md/x"} {
		if a.allows(p) || a.reopened(p) {
			t.Errorf("%q must not match", p)
		}
	}
	if !a.reopened("loop/x.go") || a.reopened("kernel.go/x") {
		t.Errorf("a reopen honors its line's shape: loop/ is a prefix, kernel.go is one file")
	}
}

func TestParseAllowRefusesAMalformedLineNamingIt(t *testing.T) {
	for _, bad := range []string{"*/x\n", "reopen loop/\n", "reopen\n", "reopen loop/ 1.1.4 extra\n"} {
		_, err := parseAllow([]byte(bad))
		if err == nil || !strings.Contains(err.Error(), "line") {
			t.Errorf("parseAllow(%q) must refuse naming the line, got %v", bad, err)
		}
	}
}

func TestTheFreezeFileCarriesTheGateSet(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "specs", "FREEZE.txt"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := parseAllow(data)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{
		"frontend/tui/a.go", "frontend/cli", "frontend/web/x.go", "frontend/oneshot/x.go",
		"cmd/rig/main.go", "cmd/rig/testdata/golden_020/oneshot.json", "cmd/freeze/main.go",
		"command/x.go", "broadcast/x.go", "swarm/x.go",
		"middleware/perm/x.go", "middleware/toolset/x.go", "middleware/approve/x.go",
		"middleware/cutoff/x.go", "middleware/guard/x.go", "middleware/paths/x.go", "middleware/index/x.go",
		"evt/x.go", "plugins/x.go", "testenv/x.go", "testenv/x/y.go",
		"store/x.go", "store/state/x.go", "store/todo/x.go", "store/scheduler/x.go",
		"store/scope/x.go", "store/decision/x.go", "store/rem/PACKAGE.md",
		"tool/diff/x.go", "tool/python/x.go", "tool/bash/x.go", "tool/fs/x.go", "tool/web/x.go",
		"tool/file/x.go", "tool/rem/x.go", "tool/todo/x.go", "tool/sessions/x.go",
		"tool/scheduler/x.go", "tool/delegate/x.go", "tool/verdict/x.go", "tool/view/x.go",
		"tool/execwrap/x.go", "tool/registry.go", "tool/registry.json", "tool/registry_test.go",
		"provider/x.go", "config/x.go", "models/x.go", "policy/x.go", "pathguard/x.go",
		"imagemarker/x.go", "decision/x.go",
		"core", "core/x.go", "core/x/PACKAGE.md",
		"docs/x.md", "specs/SPEC_X.md", "AGENTS.md", "Makefile", ".gitignore",
		"README.md", "SECURITY.md", "CONTRIBUTING.md", ".github/workflows/ci.yml",
		"install.sh", "site/index.html", "CHANGELOG.md", "ROADMAP.md",
		"PACKAGE.md", "tool/PACKAGE.md", "loop/PACKAGE.md",
		"go.mod", "go.sum", "scripts/wire-check",
	}
	for _, p := range allowed {
		if !a.allows(p) {
			t.Errorf("%q must sit inside the allowlist", p)
		}
	}
	refused := []string{
		"frontend/tuix/x.go", "frontendx/x.go", "cmd/other/x.go", "cmd/rigx/x.go",
		"tool/registry.json/x", "go.sum/x", "AGENTS.md/x", "vendor/x.go", "go.work",
		"loop/x.go", "kernel_test.go/x",
		"storex/x.go", "middleware2/perm/x.go", "PACKAGEmd",
	}
	for _, p := range refused {
		if a.allows(p) {
			t.Errorf("%q must reach outside the allowlist", p)
		}
	}
	reopened := []string{"loop", "loop/x.go", "kernel.go", "kernel_test.go", "core/seam_test.go", "core/session_test.go", "core/provider.go"}
	for _, p := range reopened {
		if !a.reopened(p) {
			t.Errorf("%q is reopened by name", p)
		}
	}
	for _, p := range []string{"loopx", "kernel.go/x", "core/session_test.go/x", "core/x.go"} {
		if a.reopened(p) {
			t.Errorf("%q is not a reopened path", p)
		}
	}
}

func TestOutsideNamesThePathAndTheFileToEdit(t *testing.T) {
	a, err := parseAllow([]byte("frontend/tui/\nAGENTS.md\n*/PACKAGE.md\n"))
	if err != nil {
		t.Fatal(err)
	}
	g := gate{allow: a, allowFile: "specs/FREEZE.txt", show: func(string) ([]byte, error) { return nil, os.ErrNotExist }, read: func(string) ([]byte, error) { return nil, os.ErrNotExist }}
	outsides := g.outside([]string{"frontend/tui/a.go", "AGENTS.md", "cmd/other/x.go"})
	if len(outsides) != 1 {
		t.Fatalf("refusals = %v, want the one path outside", outsides)
	}
	if !strings.Contains(outsides[0], "cmd/other/x.go") || !strings.Contains(outsides[0], "specs/FREEZE.txt") {
		t.Fatalf("the refusal must name the path and the file to edit: %q", outsides[0])
	}
}

func TestImpureRefusesAModifiedFrozenFileAndPassesAPureAddition(t *testing.T) {
	oldSrc := []byte("package x\n\nfunc a() int {\n\treturn 1\n}\n")
	addSrc := []byte("package x\n\nfunc a() int {\n\treturn 1\n}\n\nfunc b() int {\n\treturn 2\n}\n")
	modSrc := []byte("package x\n\nfunc a() int {\n\treturn 3\n}\n")
	commentSrc := []byte("package x\n\nfunc a() int {\n\t// a note\n\treturn 1\n}\n")
	g := gate{
		allowFile: "specs/FREEZE.txt",
		show:      func(string) ([]byte, error) { return oldSrc, nil },
		read:      func(string) ([]byte, error) { return addSrc, nil },
	}
	if outsides := g.impure([]string{"core/new.go"}); len(outsides) != 0 {
		t.Fatalf("a pure addition must pass: %v", outsides)
	}
	commentFile := "core/commented.go"
	g.read = func(path string) ([]byte, error) {
		if path == commentFile {
			return commentSrc, nil
		}
		return addSrc, nil
	}
	if outsides := g.impure([]string{commentFile}); len(outsides) != 0 {
		t.Fatalf("a comment-only change must pass: %v", outsides)
	}
	modFile := "core/modified.go"
	g.read = func(path string) ([]byte, error) {
		if path == modFile {
			return modSrc, nil
		}
		return addSrc, nil
	}
	outsides := g.impure([]string{modFile})
	if len(outsides) != 1 {
		t.Fatalf("a modified frozen file must refuse: %v", outsides)
	}
	if !strings.Contains(outsides[0], modFile) || !strings.Contains(outsides[0], "modified, not extended") {
		t.Fatalf("the refusal must name the file and the rule: %q", outsides[0])
	}
}

func TestImpureRefusesANonGoFileAndAGainedFileOnTheFrozenSurface(t *testing.T) {
	g := gate{
		allowFile: "specs/FREEZE.txt",
		show:      func(string) ([]byte, error) { return nil, os.ErrNotExist },
		read:      func(string) ([]byte, error) { return []byte("x"), nil },
	}
	if outsides := g.impure([]string{"core/notes.md"}); len(outsides) != 1 || !strings.Contains(outsides[0], "non-Go") {
		t.Fatalf("a non-Go file on the frozen surface must refuse: %v", outsides)
	}
	outsides := g.impure([]string{"core/gained.go"})
	if len(outsides) != 1 || !strings.Contains(outsides[0], "gained") {
		t.Fatalf("a gained file must refuse: %v", outsides)
	}
}
