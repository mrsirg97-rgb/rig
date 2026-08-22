package todo

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
)

func StorePath(home, cwd string) string {
	sum := sha1.Sum([]byte(cwd))
	return filepath.Join(home, "todo", hex.EncodeToString(sum[:12])+".sqlite")
}
