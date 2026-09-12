package pathguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("cwd %q: %v", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("cwd %q: %v", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd %q is not a directory", abs)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("cwd %q: %v", abs, err)
	}
	return filepath.Clean(resolved), nil
}

func Within(path, sessionCwd, rigHome string) (string, error) {
	cwd, err := Canonical(path)
	if err != nil {
		if abs, absErr := filepath.Abs(path); absErr == nil {
			if !inside(sessionCwd, abs) && !inside(rigHome, abs) {
				if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil &&
					(inside(sessionCwd, resolved) || inside(rigHome, resolved)) {
					return "", err
				}
				return "", fmt.Errorf("cwd %q is outside the session's cwd (%s) and the rig home (%s)", filepath.Clean(abs), sessionCwd, rigHome)
			}
		}
		return "", err
	}
	canonicalUnder := func(root string) bool {
		if root == "" {
			return false
		}
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(filepath.Clean(resolvedRoot), cwd)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if !canonicalUnder(sessionCwd) && !canonicalUnder(rigHome) {
		return "", fmt.Errorf("cwd %q is outside the session's cwd (%s) and the rig home (%s)", cwd, sessionCwd, rigHome)
	}
	return cwd, nil
}

// inside reports whether path is under root in the lexical form or the
// root's resolved form: a symlinked cwd accepts both spellings.
func inside(root, path string) bool {
	if under(root, path) {
		return true
	}
	if root == "" {
		return false
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	return under(resolved, path)
}

func under(root, path string) bool {
	if root == "" {
		return false
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
