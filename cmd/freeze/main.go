package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type entry struct {
	line   string
	reopen bool
}

type allowlist []entry

func parseAllow(data []byte) (allowlist, error) {
	var a allowlist
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if fields[0] == "reopen" {
			if len(fields) != 3 {
				return nil, fmt.Errorf("line %d: reopen takes a path and a version: %q", i+1, line)
			}
			if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(fields[2]) {
				return nil, fmt.Errorf("line %d: the reopening's version must be semver: %q", i+1, line)
			}
			a = append(a, entry{line: fields[1], reopen: true})
			continue
		}
		if strings.Contains(line, "*") && line != "*/PACKAGE.md" {
			return nil, fmt.Errorf("line %d: the one glob is */PACKAGE.md: %q", i+1, line)
		}
		a = append(a, entry{line: line})
	}
	return a, nil
}

func matchLine(line, p string) bool {
	if line == "*/PACKAGE.md" {
		return p == "PACKAGE.md" || strings.HasSuffix(p, "/PACKAGE.md")
	}
	if strings.HasSuffix(line, "/") {
		return p == strings.TrimSuffix(line, "/") || strings.HasPrefix(p, line)
	}
	return p == line
}

func (a allowlist) allows(p string) bool {
	for _, e := range a {
		if !e.reopen && matchLine(e.line, p) {
			return true
		}
	}
	return false
}

func (a allowlist) reopened(p string) bool {
	for _, e := range a {
		if e.reopen && matchLine(e.line, p) {
			return true
		}
	}
	return false
}

type gate struct {
	allow     allowlist
	allowFile string
	show      func(rev string) ([]byte, error)
	read      func(path string) ([]byte, error)
}

func (g gate) outside(changed []string) []string {
	var bad []string
	for _, p := range changed {
		if g.allow.reopened(p) || g.commentOnly(p) {
			continue
		}
		if !g.allow.allows(p) {
			bad = append(bad, fmt.Sprintf("the freeze diff reaches outside the allowlist: %s (edit %s: one path per line; a reopening is a one-line diff)", p, g.allowFile))
		}
	}
	return bad
}

func (g gate) impure(frozen []string) []string {
	var bad []string
	for _, p := range frozen {
		if g.allow.reopened(p) || strings.HasSuffix(p, "/PACKAGE.md") {
			continue
		}
		if !strings.HasSuffix(p, ".go") {
			bad = append(bad, fmt.Sprintf("core/ or loop/ gained a non-Go file: %s (a real change to the frozen surface)", p))
			continue
		}
		oldB, oldErr := g.show(p)
		newB, newErr := g.read(p)
		if (oldErr == nil) != (newErr == nil) {
			side := "gained"
			if oldErr == nil {
				side = "lost"
			}
			bad = append(bad, fmt.Sprintf("core/ or loop/ %s a file: %s (a real change to the frozen surface)", side, p))
			continue
		}
		if oldErr != nil {
			bad = append(bad, fmt.Sprintf("core/ or loop/ changed beyond gofmt: %s (absent from both sides: %v; %v)", p, oldErr, newErr))
			continue
		}
		oldF, oldFErr := stripped(oldB)
		newF, newFErr := stripped(newB)
		if oldFErr != nil || newFErr != nil {
			bad = append(bad, fmt.Sprintf("core/ or loop/ gofmt refused: %s (%v; %v)", p, oldFErr, newFErr))
			continue
		}
		if !pureAddition(oldF, newF) {
			bad = append(bad, fmt.Sprintf("core/ or loop/ modified, not extended: %s (the frozen surface is open to pure addition, closed to modification; the named change goes in the PR and SPEC_CORE)", p))
		}
	}
	return bad
}

func (g gate) commentOnly(p string) bool {
	if !strings.HasSuffix(p, ".go") {
		return false
	}
	oldB, err := g.show(p)
	if err != nil {
		return false
	}
	newB, err := g.read(p)
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

func pureAddition(oldF, newF []byte) bool {
	o := strings.Split(string(oldF), "\n")
	n := strings.Split(string(newF), "\n")
	i := 0
	for _, line := range n {
		if i < len(o) && line == o[i] {
			i++
		}
	}
	return i == len(o)
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

func gitOut(args ...string) ([]byte, error) {
	return exec.Command("git", args...).Output()
}

func changedPaths(base string) ([]string, error) {
	out, err := gitOut("diff", "--name-only", base, "--")
	if err != nil {
		return nil, fmt.Errorf("git diff: %v", err)
	}
	changed := strings.Fields(string(out))
	st, err := gitOut("status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git status: %v", err)
	}
	for _, line := range strings.Split(string(st), "\n") {
		if strings.HasPrefix(line, "?? ") {
			changed = append(changed, strings.TrimSpace(line[3:]))
		}
	}
	return changed, nil
}

func main() {
	baseFlag := flag.String("base", "origin/main", "the ref the merge-base resolves against")
	allowFlag := flag.String("allow", "specs/FREEZE.txt", "the allowlist file")
	branchFlag := flag.String("branch", "", "the branch under review; one carrying -refactor skips the gate")
	flag.Parse()

	branch := *branchFlag
	if branch == "" {
		if out, err := gitOut("branch", "--show-current"); err == nil {
			branch = strings.TrimSpace(string(out))
		}
	}
	if strings.Contains(branch, "-refactor") {
		fmt.Printf("freeze: branch %s carries -refactor; the gate is skipped (the PR names the reopening)\n", branch)
		return
	}

	root, err := gitOut("rev-parse", "--show-toplevel")
	if err != nil {
		fmt.Fprintf(os.Stderr, "freeze: not a git checkout: %v\n", err)
		os.Exit(1)
	}
	rootDir := strings.TrimSpace(string(root))

	out, err := gitOut("merge-base", *baseFlag, "HEAD")
	if err != nil {
		fmt.Fprintf(os.Stderr, "freeze: merge-base %s HEAD refused: %v (fetch-depth: 0 carries %s)\n", *baseFlag, err, *baseFlag)
		os.Exit(1)
	}
	base := strings.TrimSpace(string(out))

	data, err := os.ReadFile(*allowFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "freeze: %s: %v\n", *allowFlag, err)
		os.Exit(1)
	}
	a, err := parseAllow(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "freeze: %s: %v\n", *allowFlag, err)
		os.Exit(1)
	}

	changed, err := changedPaths(base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "freeze: %v\n", err)
		os.Exit(1)
	}

	g := gate{
		allow:     a,
		allowFile: *allowFlag,
		show: func(rev string) ([]byte, error) {
			return gitOut("show", base+":"+rev)
		},
		read: func(path string) ([]byte, error) {
			return os.ReadFile(filepath.Join(rootDir, path))
		},
	}

	bad := g.outside(changed)
	if frozenOut, err := gitOut("diff", "--name-only", base, "--", "core/", "loop/"); err == nil {
		bad = append(bad, g.impure(strings.Fields(string(frozenOut)))...)
	}
	if len(bad) > 0 {
		for _, line := range bad {
			fmt.Fprintf(os.Stderr, "freeze: %s\n", line)
		}
		os.Exit(1)
	}
	fmt.Printf("freeze: %d paths checked against %s\n", len(changed), *allowFlag)
}
