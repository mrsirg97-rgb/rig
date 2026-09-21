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
	bare map[string]bool
}

var cache = cacheT{vals: map[string]string{}, bare: map[string]bool{}}

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

// InRepo reports whether cwd resolves to a git repository: the common
// dir differs from the cwd itself, or git says the directory is a bare
// repository (a bare layout's common dir is the cwd, so the path alone
// cannot tell it apart). Outside a repo the scope is the cwd hash and
// callers say so out loud.
func InRepo(cwd string) bool {
	if cwd == "" {
		return false
	}
	return Path(cwd) != cwd || Bare(cwd)
}

// Bare reports whether git says cwd is a bare repository. Its common dir
// is the cwd itself, so Path cannot distinguish it from a plain
// directory; the probe answers the one question the path leaves open.
func Bare(cwd string) bool {
	if cwd == "" {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if v, ok := cache.bare[cwd]; ok {
		return v
	}
	bare := false
	if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--is-bare-repository").Output(); err == nil {
		bare = strings.TrimSpace(string(out)) == "true"
	}
	cache.bare[cwd] = bare
	return bare
}
