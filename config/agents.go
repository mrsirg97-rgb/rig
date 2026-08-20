package config

import (
	"os"
	"path/filepath"
	"strings"
)

func readAgents(dir, cwd string) (string, error) {
	var parts []string
	for _, p := range []string{filepath.Join(dir, "AGENTS.md"), filepath.Join(cwd, "AGENTS.md")} {
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", readErr(p, err)
		}
		if len(data) > 0 {
			parts = append(parts, string(data))
		}
	}
	return strings.Join(parts, "\n\n"), nil
}
