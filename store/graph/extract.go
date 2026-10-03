package graph

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const (
	KindFunc   = "func"
	KindMethod = "method"
	KindType   = "type"
	KindVar    = "var"
	KindConst  = "const"
)

type Symbol struct {
	Package string
	Name    string
	Kind    string
	File    string
	Line    int64
}

type Edge struct {
	FromPackage string
	FromName    string
	ToPackage   string
	ToName      string
	File        string
	Line        int64
}

type Result struct {
	Symbols []Symbol
	Edges   []Edge
	Eager   bool
}

type Extractor interface {
	Extract(ctx context.Context, abs string) (Result, error)
}

var languages = map[string]string{
	".go":  "go",
	".ts":  "typescript",
	".tsx": "typescript",
	".mts": "typescript",
	".cts": "typescript",
	".js":  "typescript",
	".jsx": "typescript",
	".mjs": "typescript",
	".cjs": "typescript",
	".py":  "python",
}

func LanguageOf(abs string) string {
	return languages[strings.ToLower(filepath.Ext(abs))]
}

func Skip(abs string) bool {
	for _, c := range strings.Split(filepath.ToSlash(filepath.Clean(abs)), "/") {
		if c == "vendor" || c == "testdata" || (strings.HasPrefix(c, ".") && len(c) > 1) || (strings.HasPrefix(c, "_") && len(c) > 1) {
			return true
		}
	}
	return false
}

func ProjectOf(abs string) (root, module string, err error) {
	return RootOf(filepath.Dir(abs))
}

func RootOf(dir string) (root, module string, err error) {
	start := dir
	for ; ; dir = filepath.Dir(dir) {
		b, rerr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if rerr == nil {
			return dir, moduleLine(b), nil
		}
		up := filepath.Dir(dir)
		if up == dir {
			break
		}
	}
	if out, gerr := exec.Command("git", "-C", start, "rev-parse", "--show-toplevel").Output(); gerr == nil {
		p := strings.TrimSpace(string(out))
		if p != "" && !strings.HasPrefix(p, "-") && !strings.Contains(p, "\n") {
			if !filepath.IsAbs(p) {
				p = filepath.Join(start, p)
			}
			if real, err := filepath.EvalSymlinks(p); err == nil {
				p = real
			}
			return p, "", nil
		}
	}
	return start, "", nil
}

func moduleLine(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), "\"`")
		}
	}
	return ""
}

func PackagePath(root, module, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if module != "" {
		if rel == "." {
			return module
		}
		return module + "/" + rel
	}
	if rel == "." {
		return path.Base(dir)
	}
	return rel
}
