package scope

import (
	"crypto/sha1"
	"encoding/hex"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const Global = "global"

func ShortHash(s string) string {
	d := sha1.Sum([]byte(s))
	return hex.EncodeToString(d[:])[:12]
}

type memoT[V any] struct {
	mu   sync.Mutex
	vals map[string]V
}

func (m *memoT[V]) get(key string, probe func() V) V {
	m.mu.Lock()
	v, ok := m.vals[key]
	m.mu.Unlock()
	if ok {
		return v
	}
	v = probe()
	m.mu.Lock()
	if stored, ok := m.vals[key]; ok {
		v = stored
	} else {
		m.vals[key] = v
	}
	m.mu.Unlock()
	return v
}

var (
	paths = memoT[string]{vals: map[string]string{}}
	wts   = memoT[string]{vals: map[string]string{}}
	bares = memoT[bool]{vals: map[string]bool{}}
)

func Path(cwd string) string {
	if cwd == "" || cwd == Global {
		return cwd
	}
	return paths.get(cwd, func() string {
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
		return v
	})
}

func Key(cwd string) string {
	if cwd == Global {
		return Global
	}
	return ShortHash(Path(cwd))
}

func Worktree(cwd string) string {
	if cwd == "" {
		return ""
	}
	return wts.get(cwd, func() string {
		common := Path(cwd)
		v := Label(cwd)
		if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--git-dir").Output(); err == nil {
			p := strings.TrimSpace(string(out))
			if p != "" && !strings.HasPrefix(p, "-") && !strings.Contains(p, "\n") {
				if !filepath.IsAbs(p) {
					p = filepath.Join(cwd, p)
				}
				if real, err := filepath.EvalSymlinks(p); err == nil {
					p = real
				}
				if rel, err := filepath.Rel(common, p); err == nil {
					parts := strings.Split(rel, string(filepath.Separator))
					if len(parts) == 2 && parts[0] == "worktrees" {
						v = parts[1]
					}
				}
			}
		}
		return v
	})
}

func Label(cwd string) string {
	label := filepath.Base(cwd)
	if label == "." || label == "" {
		label = "root"
	}
	return label
}

func InRepo(cwd string) bool {
	if cwd == "" {
		return false
	}
	return Path(cwd) != cwd || Bare(cwd)
}

func Bare(cwd string) bool {
	if cwd == "" || cwd == Global {
		return false
	}
	return bares.get(cwd, func() bool {
		bare := false
		if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--is-bare-repository").Output(); err == nil {
			bare = strings.TrimSpace(string(out)) == "true"
		}
		return bare
	})
}
