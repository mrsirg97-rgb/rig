// Package web provides pane's web tools: web_search (the local SearXNG
// instance over net/http) and web_fetch (a guarded HTTP reader with
// trafilatura extraction and a stdlib text pass). Stdlib only — no
// third-party Go client, no new venv.
package web

import (
	"os"
	"os/exec"
	"path/filepath"
)

// DefaultSearXNG is pane's PI_SEARXNG_URL default: the web-tools compose
// (~/docker/web-tools) publishes SearXNG on loopback :8888.
const DefaultSearXNG = "http://127.0.0.1:8888"

// DefaultProxy is pane's PI_WEB_FETCH_PROXY default: the same compose's
// tinyproxy on loopback :8889, the egress path web_fetch is routed through.
const DefaultProxy = "http://127.0.0.1:8889"

// DefaultTrafilatura resolves the extraction binary the way the default
// path prefers it: the shared agent venv first (interop with the python
// kernel's venv, pane's interop story), then PATH. Empty when neither has
// it — the stdlib text pass carries the work and says so.
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
