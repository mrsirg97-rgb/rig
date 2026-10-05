package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	defaultMaxResults = 5
	searchTimeout     = 15 * time.Second
	snippetCap        = 300
	searchBodyCap     = 1 << 20
)

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

func (s *search) exec(ctx context.Context, query string, maxResults int) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	q := strings.ReplaceAll(url.QueryEscape(query), "+", "%20")
	req, err := http.NewRequestWithContext(cctx, http.MethodGet,
		s.searchURL+"?q="+q+"&format=json", nil)
	if err != nil {
		return "", fmt.Errorf("web: invalid query: %v", err)
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
		return "", fmt.Errorf("web: reading the response: %v", err)
	}
	var data struct {
		Results []searxngResult `json:"results"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("web: SearXNG did not return JSON: %v", err)
	}

	out := make([]result, 0, maxResults)
	for i := range data.Results {
		if i >= maxResults {
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
		return fmt.Sprintf("no results for %q", query), nil
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
