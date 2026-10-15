package file

import (
	"path/filepath"
)

func normalizePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		dir, base := filepath.Split(path)
		if dir == "" {
			dir = "."
		}
		if resolvedDir, derr := filepath.EvalSymlinks(dir); derr == nil {
			resolved = filepath.Join(resolvedDir, base)
			err = nil
		}
	}
	if err != nil {
		if abs, aerr := filepath.Abs(path); aerr == nil {
			return abs
		}
		return path
	}
	if abs, aerr := filepath.Abs(resolved); aerr == nil {
		return abs
	}
	return resolved
}
