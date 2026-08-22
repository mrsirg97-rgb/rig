package state

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
)

func StorePath(home, cwd string) string {
	sum := sha1.Sum([]byte(cwd))
	return filepath.Join(home, "sessions", hex.EncodeToString(sum[:6])+".sqlite")
}
