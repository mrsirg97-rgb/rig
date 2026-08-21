package todo

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
)

// StorePath resolves the workspace's todo file the way the root does
// (SPEC_SERVE 2): one file per cwd under <home>/todo, keyed by the first
// twelve sha1 bytes of the cwd. The root and the dashboard share this one
// source.
func StorePath(home, cwd string) string {
	sum := sha1.Sum([]byte(cwd))
	return filepath.Join(home, "todo", hex.EncodeToString(sum[:12])+".sqlite")
}
