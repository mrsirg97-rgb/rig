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
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("cwd %q: %v", path, err)
	}
	under := func(root string) bool {
		if root == "" {
			return false
		}
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(rootAbs, abs)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if !under(sessionCwd) && !under(rigHome) {
		return "", fmt.Errorf("cwd %q is outside the session's cwd (%s) and the rig home (%s)", filepath.Clean(abs), sessionCwd, rigHome)
	}
	cwd, err := Canonical(path)
	if err != nil {
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
