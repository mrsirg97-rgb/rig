package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const DefaultSearXNG = "http://127.0.0.1:8888"

const DefaultProxy = "http://127.0.0.1:8889"

const webDescription = "search the web (a local SearXNG): target is the query.\n" +
	"fetch a public http(s) URL as readable text (article extraction for HTML, the raw body for JSON and plain text): target is the URL."

const webGuidelines = "Guidelines: search finds, fetch reads — snippets are not the page; web pages and textual APIs only (local files -> read, local services -> bash). " +
	"Search: current or external information; never for code already in the workspace. Prefer natural-language multi-word queries: a leading brand or single-token query often returns navigational or dictionary junk from the engines. For a known authoritative URL use fetch, not search. On junk results, reword the query once; an identical retry returns the same junk. Reply: compact JSON — title, url, snippet per result. " +
	"Fetch: reply the text, capped with a [TRUNCATED] marker naming the full size — refetch with a larger maxChars only if the missing part matters; private and internal addresses are refused."

const webSchema = `{
	"type": "object",
	"properties": {
		"action": {"type": "string", "enum": ["search", "fetch"], "description": "search <target> or fetch <target>"},
		"target": {"type": "string", "description": "Search query, or the absolute http(s) URL to fetch"},
		"maxResults": {"type": "integer", "description": "Max results (default 5)", "minimum": 1, "maximum": 20},
		"maxChars": {"type": "integer", "description": "Max chars returned (default 20000)", "minimum": 100},
		"timeoutMs": {"type": "integer", "description": "Total timeout in ms (default 30000)", "minimum": 1000, "maximum": 300000}
	},
	"required": ["action", "target"]
}`

type Config struct {
	Search SearchConfig
	Fetch  FetchConfig
}

type web struct {
	search *search
	fetch  *fetch
}

func New(cfg Config) *web {
	return &web{search: NewSearch(cfg.Search), fetch: NewFetch(cfg.Fetch)}
}

func Web() *web {
	return New(Config{Fetch: FetchConfig{Proxy: DefaultProxy}})
}

func (w *web) Name() string { return "web" }

func (w *web) Description() string { return webDescription + "\n\n" + webGuidelines }

func (w *web) Schema() json.RawMessage { return json.RawMessage(webSchema) }

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
