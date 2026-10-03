package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/tool"
	"os"
	"os/exec"
	"path/filepath"
)

const DefaultSearXNG = "http://127.0.0.1:8888"

const DefaultProxy = "http://127.0.0.1:8889"

type Config struct {
	Search SearchConfig
	Fetch  FetchConfig
}

type web struct {
	tool.Definition
	search *search
	fetch  *fetch
}

func New(cfg Config) *web {
	return &web{Definition: tool.Def("web"), search: NewSearch(cfg.Search), fetch: NewFetch(cfg.Fetch)}
}

func Web() *web {
	return New(Config{Fetch: FetchConfig{Proxy: DefaultProxy}})
}

func (w *web) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action     string `json:"action"`
		Target     string `json:"target"`
		MaxResults *int   `json:"maxResults"`
		MaxChars   *int   `json:"maxChars"`
		TimeoutMs  *int   `json:"timeoutMs"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("web: bad args: %v", err)
	}
	switch p.Action {
	case "search":
		if p.Target == "" {
			return "", errors.New("web: search: no query supplied")
		}
		n := 5
		if p.MaxResults != nil {
			n = *p.MaxResults
		}
		if n < 1 || n > 20 {
			return "", fmt.Errorf("web: maxResults must be between 1 and 20, got %d", n)
		}
		return w.search.exec(ctx, p.Target, n)
	case "fetch":
		if p.Target == "" {
			return "", errors.New("web: fetch: no url supplied")
		}
		maxC := maxChars
		if p.MaxChars != nil {
			maxC = *p.MaxChars
		}
		if maxC < 100 {
			return "", fmt.Errorf("web: maxChars must be at least 100, got %d", maxC)
		}
		timeoutMs := defaultTimeoutMs
		if p.TimeoutMs != nil {
			timeoutMs = *p.TimeoutMs
		}
		if timeoutMs < minTimeoutMs || timeoutMs > maxTimeoutMs {
			return "", fmt.Errorf("web: timeoutMs must be between %d and %d, got %d", minTimeoutMs, maxTimeoutMs, timeoutMs)
		}
		return w.fetch.exec(ctx, p.Target, maxC, timeoutMs)
	default:
		return "", fmt.Errorf("web: unknown action %q (search|fetch)", p.Action)
	}
}

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
