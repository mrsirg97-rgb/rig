package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/tool"
	"os"
	"os/exec"
	"path/filepath"
)

const DefaultSearXNG = "http://127.0.0.1:8888"

const DefaultProxy = "http://127.0.0.1:8889"

type Config struct {
	Search     SearchConfig
	Fetch      FetchConfig
	Background background
}

type background interface {
	Submit(ctx context.Context, kind, name string, bound time.Duration, work func(ctx context.Context) (string, int)) (string, error)
}

type Web interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Search(ctx context.Context, query string, maxResults int) (string, error)
	Fetch(ctx context.Context, url string, maxChars, timeoutMs int) (string, error)
}

type web struct {
	tool.Definition
	search *search
	fetch  *fetch
	bg     background
}

func New(cfg Config) Web {
	return &web{Definition: tool.Def("web"), search: newSearch(cfg.Search), fetch: newFetch(cfg.Fetch), bg: cfg.Background}
}

func NewDefault() Web {
	return New(Config{Fetch: FetchConfig{Proxy: DefaultProxy}})
}

func (w *web) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action     string `json:"action"`
		Target     string `json:"target"`
		MaxResults *int   `json:"maxResults"`
		MaxChars   *int   `json:"maxChars"`
		TimeoutMs  *int   `json:"timeoutMs"`
		Defer      bool   `json:"defer"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("web: bad args: %v", err)
	}
	switch p.Action {
	case "search":
		n := defaultMaxResults
		if p.MaxResults != nil {
			n = *p.MaxResults
		}
		return w.Search(ctx, p.Target, n)
	case "fetch":
		maxC := defaultMaxChars
		if p.MaxChars != nil {
			maxC = *p.MaxChars
		}
		if p.Defer {
			if p.TimeoutMs == nil {
				return "", errors.New(tool.DeferNeedsBound("web"))
			}
			return w.submitFetch(ctx, p.Target, maxC, *p.TimeoutMs)
		}
		timeoutMs := defaultTimeoutMs
		if p.TimeoutMs != nil {
			timeoutMs = *p.TimeoutMs
		}
		return w.Fetch(ctx, p.Target, maxC, timeoutMs)
	default:
		return "", fmt.Errorf("web: unknown action %q (search|fetch)", p.Action)
	}
}

func (w *web) Search(ctx context.Context, query string, maxResults int) (string, error) {
	if query == "" {
		return "", errors.New("web: search: no query supplied")
	}
	if maxResults < 1 || maxResults > 20 {
		return "", fmt.Errorf("web: maxResults must be between 1 and 20, got %d", maxResults)
	}
	return w.search.exec(ctx, query, maxResults)
}

func (w *web) Fetch(ctx context.Context, url string, maxChars, timeoutMs int) (string, error) {
	if url == "" {
		return "", errors.New("web: fetch: no url supplied")
	}
	if maxChars < 100 {
		return "", fmt.Errorf("web: maxChars must be at least 100, got %d", maxChars)
	}
	if timeoutMs < minTimeoutMs || timeoutMs > maxTimeoutMs {
		return "", fmt.Errorf("web: timeoutMs must be between %d and %d, got %d", minTimeoutMs, maxTimeoutMs, timeoutMs)
	}
	return w.fetch.exec(ctx, url, maxChars, timeoutMs)
}

func (w *web) submitFetch(ctx context.Context, url string, maxChars, timeoutMs int) (string, error) {
	if url == "" {
		return "", errors.New("web: fetch: no url supplied")
	}
	if maxChars < 100 {
		return "", fmt.Errorf("web: maxChars must be at least 100, got %d", maxChars)
	}
	if timeoutMs < minTimeoutMs || timeoutMs > maxTimeoutMs {
		return "", fmt.Errorf("web: timeoutMs must be between %d and %d, got %d", minTimeoutMs, maxTimeoutMs, timeoutMs)
	}
	if w.bg == nil {
		return "", errors.New("web: no worker is wired for deferred fetches")
	}
	return w.bg.Submit(ctx, core.KindFetch, tool.FirstLine(url), time.Duration(timeoutMs)*time.Millisecond, func(wctx context.Context) (string, int) {
		body, err := w.fetch.exec(wctx, url, maxChars, timeoutMs)
		if err != nil {
			if strings.TrimSpace(body) == "" {
				body = err.Error()
			}
			return body, 1
		}
		return body, 0
	})
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
