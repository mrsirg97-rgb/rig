package todo

import (
	"path/filepath"

	"github.com/mrsirg97-rgb/rig/v2/store/scope"
)

func ProjectOf(dir string) Project {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	ident := scope.Path(dir)
	bare := scope.Bare(dir)
	if !scope.InRepo(dir) {
		return Project{Key: scope.ShortHash(ident), Label: scope.Label(ident), OutsideRepo: true, Dir: dir}
	}
	return Project{Key: scope.ShortHash(ident), Label: repoName(ident, bare), Dir: dir}
}

func Global() Project {
	return Project{Key: scope.Global, Label: scope.Global, Dir: scope.Global}
}

func repoName(commonDir string, bare bool) string {
	dir := commonDir
	if !bare {
		dir = filepath.Dir(commonDir)
	}
	name := filepath.Base(dir)
	if name == "." || name == "" || name == string(filepath.Separator) {
		return "repo"
	}
	return name
}
