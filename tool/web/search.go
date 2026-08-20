package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	searchTimeout = 15 * time.Second
	snippetCap    = 300
	searchBodyCap = 1 << 20
)

const searchDescription = "Search the web via SearXNG instance. Returns compact JSON: title, url, snippet per result."

const searchGuidelines = "Guidelines: " +
	"current or external info -> web_search; never for code already in the workspace."

const searchSchema = `{
	"type": "object",
	"properties": {
		"query": {"type": "string", "description": "Search query"},
		"maxResults": {"type": "integer", "description": "Max results (default 5)", "minimum": 1, "maximum": 20}
	},
	"required": ["query"]
}`

var (
	searchTag = regexp.MustCompile(`<[^>]+>`)
	searchWS  = regexp.MustCompile(`\s+`)
)

type SearchConfig struct {
	BaseURL string
	Do      func(*http.Request) (*http.Response, error)
}

type search struct {
	searchURL string
	do        func(*http.Request) (*http.Response, error)
}

func Search() *search { return NewSearch(SearchConfig{}) }

func NewSearch(cfg SearchConfig) *search {
	base := cfg.BaseURL
	if base == "" {
		base = DefaultSearXNG
	}
	s := &search{searchURL: strings.TrimSuffix(base, "/") + "/search"}
	if cfg.Do != nil {
		s.do = cfg.Do
		return s
	}
	s.do = (&http.Client{Timeout: searchTimeout}).Do
	return s
}

func (s *search) Name() string { return "web_search" }

func (s *search) Description() string { return searchDescription + "\n\n" + searchGuidelines }

func (s *search) Schema() json.RawMessage { return json.RawMessage(searchSchema) }

type searxngResult struct {
	Title   *string `json:"title"`
	URL     *string `json:"url"`
	Content *string `json:"content"`
}

type result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func (s *search) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Query      string `json:"query"`
		MaxResults *int   `json:"maxResults"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("web_search: bad args: %v", err)
	}
	if p.Query == "" {
		return "", errors.New("web_search: no query supplied")
	}
	n := 5
	if p.MaxResults != nil {
		n = *p.MaxResults
	}

	cctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	q := strings.ReplaceAll(url.QueryEscape(p.Query), "+", "%20")
	req, err := http.NewRequestWithContext(cctx, http.MethodGet,
		s.searchURL+"?q="+q+"&format=json", nil)
	if err != nil {
		return "", fmt.Errorf("web_search: invalid query: %v", err)
	}
	req.Header.Set("Accept", "application/json")

	res, err := s.do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("SearXNG search failed: HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, searchBodyCap))
	if err != nil {
		return "", fmt.Errorf("web_search: reading the response: %v", err)
	}
	var data struct {
		Results []searxngResult `json:"results"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("web_search: SearXNG did not return JSON: %v", err)
	}

	out := make([]result, 0, n)
	for i := range data.Results {
		if i >= n {
			break
		}
		r := data.Results[i]
		out = append(out, result{
			Title:   deref(r.Title),
			URL:     deref(r.URL),
			Snippet: snippet(deref(r.Content)),
		})
	}
	if len(out) == 0 {
		return "no results", nil
	}
	b, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func snippet(s string) string {
	s = searchTag.ReplaceAllString(s, " ")
	s = searchWS.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > snippetCap {
		s = string(r[:snippetCap])
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
