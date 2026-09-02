package scope

import (
	"crypto/sha1"
	"encoding/hex"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

func ShortHash(s string) string {
	d := sha1.Sum([]byte(s))
	return hex.EncodeToString(d[:])[:12]
}

type cacheT struct {
	mu   sync.Mutex
	vals map[string]string
}

var cache = cacheT{vals: map[string]string{}}

func Path(cwd string) string {
	if cwd == "" {
		return ""
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if v, ok := cache.vals[cwd]; ok {
		return v
	}
	v := cwd
	if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--git-common-dir").Output(); err == nil {
		p := strings.TrimSpace(string(out))
		if p != "" && !strings.HasPrefix(p, "-") && !strings.Contains(p, "\n") {
			if !filepath.IsAbs(p) {
				p = filepath.Join(cwd, p)
			}
			v = filepath.Clean(p)
			if real, err := filepath.EvalSymlinks(v); err == nil {
				v = real
			}
		}
	}
	cache.vals[cwd] = v
	return v
}

func Key(cwd string) string {
	return ShortHash(Path(cwd))
}

func Label(cwd string) string {
	label := filepath.Base(cwd)
	if label == "." || label == "" {
		label = "root"
	}
	return label
}
