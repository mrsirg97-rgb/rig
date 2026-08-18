package config

import (
	"os"
	"path/filepath"
	"strings"
)

// readAgents is the AGENTS.md pair (6): the global (the config dir)
// then the project (the cwd), concatenated global-first with a blank
// line between them, empty segments skipped. The content is the files
// as written — no markers, no headers, no indentation (6). ENOENT is
// silent (3); every other read error (permission, a directory by that
// name, I/O) refuses with the OS reason, the path named once (3).
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
