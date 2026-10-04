package config

import (
	"os"
	"path/filepath"
)

func readAgents(dir string) (string, error) {
	return readFile(filepath.Join(dir, "AGENTS.md"))
}

func ProjectAgents(dir, cwd string) (string, error) {
	if cwd == "" {
		return "", nil
	}
	own := filepath.Join(dir, "AGENTS.md")
	for _, at := range projectChain(cwd) {
		p := filepath.Join(at, "AGENTS.md")
		if dir != "" && sameFile(p, own) {
			continue
		}
		text, err := readFile(p)
		if err != nil || text != "" {
			return text, err
		}
	}
	return "", nil
}

func projectChain(cwd string) []string {
	chain := []string{cwd}
	for at := cwd; ; {
		if isRepoRoot(at) {
			return chain
		}
		up := filepath.Dir(at)
		if up == at {
			return chain[:1]
		}
		at = up
		chain = append(chain, at)
	}
}

func isRepoRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func sameFile(a, b string) bool {
	ra, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	rb, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	return ra == rb
}

func readFile(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", readErr(p, err)
	}
	return string(data), nil
}
