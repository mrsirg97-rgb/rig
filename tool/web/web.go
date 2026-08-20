package web

import (
	"os"
	"os/exec"
	"path/filepath"
)

const DefaultSearXNG = "http://127.0.0.1:8888"

const DefaultProxy = "http://127.0.0.1:8889"

func DefaultTrafilatura() string {
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".pi", "agent", "kernel-venv", "bin", "trafilatura")
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	if p, err := exec.LookPath("trafilatura"); err == nil {
		return p
	}
	return ""
}
