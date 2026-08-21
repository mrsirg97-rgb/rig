package state

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
)

// StorePath resolves the workspace's state file the way the root does
// (SPEC_SERVE 2): one file per cwd under <home>/sessions, keyed by the
// first six sha1 bytes of the cwd. The root and the dashboard share this
// one source.
func StorePath(home, cwd string) string {
	sum := sha1.Sum([]byte(cwd))
	return filepath.Join(home, "sessions", hex.EncodeToString(sum[:6])+".sqlite")
}
