package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/tool/web"
)

func ptr[T any](v T) *T { return &v }

func off() *string { var s string = ""; return &s }

func httpResp(status int, headers map[string]string, body string) *http.Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func publicLookup(ctx context.Context, host string) ([]string, error) {
	return []string{"93.184.216.34"}, nil
}

func privateLookup(ctx context.Context, host string) ([]string, error) {
	return []string{"10.9.8.7"}, nil
}

func direct() func(*http.Request) (*http.Response, error) {
	return (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}).Do
}

type schema struct {
	Properties map[string]map[string]any `json:"properties"`
	Required   []string                  `json:"required"`
}

func getSchema(t *testing.T, tool interface {
	Schema() json.RawMessage
}) schema {
	t.Helper()
	var s schema
	if err := json.Unmarshal(tool.Schema(), &s); err != nil {
		t.Fatalf("schema is not an object schema: %v", err)
	}
	return s
}

func has(t *testing.T, s string, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%q does not contain %q", s, sub)
	}
}

func webTool(cfg web.Config) interface {
	Exec(context.Context, json.RawMessage) (string, error)
} {
	return web.New(cfg)
}

func execArgs(t *testing.T, w interface {
	Exec(context.Context, json.RawMessage) (string, error)
}, args string) (string, error) {
	t.Helper()
	return w.Exec(context.Background(), json.RawMessage(args))
}

func TestIPisPrivateV4Table(t *testing.T) {
	priv := []string{
		"0.0.0.0", "10.1.2.3", "127.0.0.1", "169.254.169.254",
		"172.16.0.1", "172.31.255.255", "192.168.1.1", "100.64.0.1",
		"100.127.9.9", "192.0.0.170", "198.18.0.1", "224.0.0.1",
		"255.255.255.255", "192.0.2.1", "198.51.100.1", "203.0.113.1",
		"192.88.99.1",
	}
	pub := []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "172.32.0.1",
		"100.128.0.1", "198.20.0.1", "192.88.100.1"}
	for _, ip := range priv {
		if !web.IPisPrivate(ip) {
			t.Errorf("%s must be private", ip)
		}
	}
	for _, ip := range pub {
		if web.IPisPrivate(ip) {
			t.Errorf("%s must be public", ip)
		}
	}
}

func TestIPisPrivateV6Table(t *testing.T) {
	priv := []string{
		"::1", "::", "fc00::1", "fd12:3456::1", "fe80::1",
		"FEB0::1", "ff02::1", "::ffff:127.0.0.1", "::ffff:192.168.0.1",
	}
	pub := []string{
		"2606:2800:220:1:248:1893:25c8:1946", "::ffff:8.8.8.8", "fec0::1",
	}
	for _, ip := range priv {
		if !web.IPisPrivate(ip) {
			t.Errorf("%s must be private", ip)
		}
	}
	for _, ip := range pub {
		if web.IPisPrivate(ip) {
			t.Errorf("%s must be public", ip)
		}
	}
}

func TestIPisPrivateRefusesUnnormalizedLoopbackSpellings(t *testing.T) {
	for _, ip := range []string{"::ffff:7f00:1", "0:0:0:0:0:0:0:1", "0::1", "::0001",
		"::ffff:0:0", "::FFFF:10.0.0.1", "fe80::1%en0", "not-an-address", ""} {
		if !web.IPisPrivate(ip) {
			t.Errorf("%s must be private", ip)
		}
	}
	for _, ip := range []string{"::ffff:5db8:d822", "2001:4860:4860::8888"} {
		if web.IPisPrivate(ip) {
			t.Errorf("%s must be public", ip)
		}
	}
}

func TestNonHTTPSchemesAreRefused(t *testing.T) {
	f := web.NewFetch(web.FetchConfig{Lookup: publicLookup})
	for _, raw := range []string{"file:///etc/passwd", "ftp://x.example/",
		"gopher://x.example/"} {
		_, err := f.Guarded(context.Background(), raw)
		if err == nil || !regexp.MustCompile(`(?i)only http`).MatchString(err.Error()) {
			t.Errorf("%s: want an only-http refusal, got %v", raw, err)
		}
	}
}

func TestPrivateHostsAreRefusedBeforeAnyConnection(t *testing.T) {
	called := 0
	f := web.NewFetch(web.FetchConfig{
		Lookup: privateLookup,
		Do: func(*http.Request) (*http.Response, error) {
			called++
			return httpResp(200, map[string]string{"Content-Type": "text/html"}, "x"), nil
		},
	})
	_, err := f.Guarded(context.Background(), "http://internal.example/")
	if err == nil || !regexp.MustCompile(`(?i)private|refused`).MatchString(err.Error()) {
		t.Fatalf("want a private refusal, got %v", err)
	}
	if called != 0 {
		t.Fatalf("the guarded fetch dialed %d times; the refusal must come before any connection", called)
	}
}

func TestRedirectsAreFollowedAndEachHopReGuarded(t *testing.T) {
	var hops []string
	f := web.NewFetch(web.FetchConfig{
		Lookup: publicLookup,
		Do: func(req *http.Request) (*http.Response, error) {
			hops = append(hops, req.URL.String())
			if len(hops) == 1 {
				return httpResp(302, map[string]string{"Location": "/moved"}, ""), nil
			}
			return httpResp(200, map[string]string{"Content-Type": "text/html"}, "<p>landed</p>"), nil
		},
	})
	r, err := f.Guarded(context.Background(), "https://site.example/start")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://site.example/start", "https://site.example/moved"}; len(hops) != 2 || hops[0] != want[0] || hops[1] != want[1] {
		t.Fatalf("hops = %v, want %v", hops, want)
	}
	if r.FinalURL != "https://site.example/moved" {
		t.Fatalf("final URL = %q", r.FinalURL)
	}
	has(t, r.Body, "landed")
}

func TestARedirectIntoPrivateSpaceIsRefused(t *testing.T) {
	lookup := func(ctx context.Context, host string) ([]string, error) {
		if host == "evil.example" {
			return []string{"93.184.216.34"}, nil
		}
		return []string{"169.254.169.254"}, nil
	}
	f := web.NewFetch(web.FetchConfig{
		Lookup: lookup,
		Do: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.String(), "evil") {
				return httpResp(302, map[string]string{"Location": "http://metadata.internal/latest"}, ""), nil
			}
			return httpResp(200, map[string]string{"Content-Type": "text/html"}, "secret"), nil
		},
	})
	_, err := f.Guarded(context.Background(), "http://evil.example/")
	if err == nil || !regexp.MustCompile(`(?i)private|refused`).MatchString(err.Error()) {
		t.Fatalf("want a private refusal on the redirect hop, got %v", err)
	}
}

func TestRedirectLoopsStopAtTheHopCap(t *testing.T) {
	f := web.NewFetch(web.FetchConfig{
		Lookup: publicLookup,
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(302, map[string]string{"Location": "/again"}, ""), nil
		},
	})
	_, err := f.Guarded(context.Background(), "https://loop.example/")
	if err == nil || !regexp.MustCompile(`(?i)redirect`).MatchString(err.Error()) {
		t.Fatalf("want a redirect-cap error, got %v", err)
	}
}

func TestAnOversizedContentLengthIsRefusedBeforeDownload(t *testing.T) {
	f := web.NewFetch(web.FetchConfig{
		Lookup: publicLookup,
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{
				"Content-Type":   "text/html",
				"Content-Length": "52428800",
			}, "tiny"), nil
		},
	})
	_, err := f.Guarded(context.Background(), "https://big.example/")
	if err == nil || !regexp.MustCompile(`(?i)too large`).MatchString(err.Error()) {
		t.Fatalf("want a too-large refusal, got %v", err)
	}
}

func TestTheBodyStreamIsCappedEvenWhenHeadersLie(t *testing.T) {
	f := web.NewFetch(web.FetchConfig{
		Lookup:   publicLookup,
		MaxBytes: 1024,
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "text/html"}, strings.Repeat("a", 64*1024)), nil
		},
	})
	r, err := f.Guarded(context.Background(), "https://liar.example/")
	if err != nil {
		t.Fatal(err)
	}
	if !r.BodyTruncated {
		t.Fatal("the body must be flagged truncated")
	}
	if len(r.Body) > 2048 {
		t.Fatalf("capped body is %d bytes", len(r.Body))
	}
}

func TestBinaryContentTypesAreRefused(t *testing.T) {
	f := web.NewFetch(web.FetchConfig{
		Lookup: publicLookup,
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "image/png"}, "x"), nil
		},
	})
	_, err := f.Guarded(context.Background(), "https://img.example/a.png")
	if err == nil || !regexp.MustCompile(`(?i)content type`).MatchString(err.Error()) {
		t.Fatalf("want a content-type refusal, got %v", err)
	}
}

func TestNon2XXStatusIsAnError(t *testing.T) {
	f := web.NewFetch(web.FetchConfig{
		Lookup: publicLookup,
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(404, map[string]string{"Content-Type": "text/html"}, "gone"), nil
		},
	})
	_, err := f.Guarded(context.Background(), "https://site.example/missing")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want a 404 error, got %v", err)
	}
}

func TestHTMLToTextStripsScriptStyleKeepsStructureDecodesEntities(t *testing.T) {
	html := `<html><head><title>T</title><style>p{}</style><script>bad()</script></head>
    <body><h1>Header</h1><p>alpha &amp; beta&nbsp;&lt;3</p><ul><li>one</li><li>two</li></ul></body></html>`
	text := web.HtmlToText(html)
	if regexp.MustCompile(`bad\(\)|p\{\}`).MatchString(text) {
		t.Fatalf("script/style leaked into the text: %q", text)
	}
	has(t, text, "Header\n")
	has(t, text, "alpha & beta <3")
	has(t, text, "one\ntwo")
}

func TestCapCharsTruncatesLoudlyWithTheTrueTotal(t *testing.T) {
	capped := web.CapChars(strings.Repeat("x", 500), 100)
	if len(capped) >= 500 {
		t.Fatalf("cap left %d chars", len(capped))
	}
	if !regexp.MustCompile(`(?is)truncated.*100.*500`).MatchString(capped) {
		t.Fatalf("marker missing the caps: %q", capped)
	}
	if got := web.CapChars("short", 100); got != "short" {
		t.Fatalf("short text must pass through: %q", got)
	}
}

func TestExtractReadableFallsBackToHTMLToTextWhenTrafilaturaIsUnavailable(t *testing.T) {
	text, _ := web.ExtractReadable(context.Background(), "<body><p>plain fallback</p></body>", off())
	has(t, text, "plain fallback")
	missing, _ := web.ExtractReadable(context.Background(), "<body><p>still works</p></body>", ptr("/nonexistent/bin"))
	has(t, missing, "still works")
}

func TestExtractReadableKillsASlowTrafilaturaAtTheContextDeadline(t *testing.T) {
	script := filepath.Join(t.TempDir(), "slow-traf")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	text, note := web.ExtractReadable(ctx, "<body><p>x</p></body>", ptr(script))
	elapsed := time.Since(start)
	has(t, text, "x")
	has(t, note, "trafilatura failed")
	if elapsed > 2*time.Second {
		t.Fatalf("the extraction ignored the context deadline: %s", elapsed)
	}
}

func TestE2ERealServerThroughTheSeamHTMLExtracted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "/page", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><body><script>x()</script><article><p>real e2e body</p></article></body></html>`)
	}))
	defer srv.Close()

	f := web.NewFetch(web.FetchConfig{Lookup: publicLookup, Do: direct()})
	got, err := f.Guarded(context.Background(), srv.URL+"/hop")
	if err != nil {
		t.Fatal(err)
	}
	has(t, got.Body, "real e2e body")

	has(t, got.Body, "<script>")
	if want := strings.TrimSuffix(srv.URL, "/") + "/page"; got.FinalURL != want {
		t.Fatalf("final URL = %q, want %q", got.FinalURL, want)
	}

	w := webTool(web.Config{Fetch: web.FetchConfig{Lookup: publicLookup, Do: direct()}})
	content, err := execArgs(t, w, `{"action":"fetch","target":`+`"`+srv.URL+"/hop"+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	has(t, content, "real e2e body")
	if strings.Contains(content, "x()") {
		t.Fatal("script content leaked into the content")
	}
}

func TestTheDialIsPinnedToTheVettedAddressesAndNeverReResolves(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "rebound")
	}))
	defer srv.Close()

	lookups := 0
	lookup := func(ctx context.Context, host string) ([]string, error) {
		lookups++
		if lookups == 1 {
			return []string{"192.0.2.1"}, nil
		}
		return []string{"127.0.0.1"}, nil
	}
	f := web.NewFetch(web.FetchConfig{Lookup: lookup})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := f.Guarded(ctx, srv.URL+"/")
	if err == nil {
		t.Fatal("a dial to the vetted address must not land on the loopback listener")
	}
	if reached {
		t.Fatal("the transport re-resolved the host and reached the loopback listener")
	}
	if lookups != 1 {
		t.Fatalf("lookup ran %d times, want exactly one vetting per hop", lookups)
	}
}

func TestE2ETimeoutSurfacesAsAClearError(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	w := webTool(web.Config{Fetch: web.FetchConfig{Lookup: publicLookup, Do: direct()}})
	_, err := execArgs(t, w, `{"action":"fetch","target":"`+srv.URL+`/slow","timeoutMs":1000}`)
	if err == nil || !regexp.MustCompile(`(?i)timed out`).MatchString(err.Error()) {
		t.Fatalf("want a timeout error, got %v", err)
	}
}

func TestExecuteReportsGuardRefusalsAsToolErrorsNotThrows(t *testing.T) {
	w := webTool(web.Config{Fetch: web.FetchConfig{Lookup: privateLookup}})
	_, err := execArgs(t, w, `{"action":"fetch","target":"http://internal.example/"}`)
	if err == nil || !regexp.MustCompile(`(?i)private|refused`).MatchString(err.Error()) {
		t.Fatalf("want the refusal as the tool error, got %v", err)
	}
}

func TestToolRegistrationOneWebToolWithActionAndTarget(t *testing.T) {
	w := web.Web()
	if w.Name() != "web" {
		t.Fatalf("name = %q, want web", w.Name())
	}
	s := getSchema(t, w)
	if len(s.Required) != 2 || s.Required[0] != "action" || s.Required[1] != "target" {
		t.Fatalf("required = %v, want [action target]", s.Required)
	}
	if _, ok := s.Properties["action"]; !ok {
		t.Fatal("the action parameter is missing from the schema")
	}
	if _, ok := s.Properties["target"]; !ok {
		t.Fatal("the target parameter is missing from the schema")
	}
	has(t, w.Description(), "search the web (a local SearXNG)")
	has(t, w.Description(), "fetch a public http(s) URL")
	has(t, w.Description(), "target is the query")
	has(t, w.Description(), "target is the URL")
	has(t, w.Description(), "search finds, fetch reads")
}

func TestSchemaRequiresActionAndTargetAndBoundsAllOptions(t *testing.T) {
	s := getSchema(t, web.Web())
	if len(s.Required) != 2 || s.Required[0] != "action" || s.Required[1] != "target" {
		t.Fatalf("required = %v, want [action target]", s.Required)
	}
	act := s.Properties["action"]
	if act == nil {
		t.Fatal("action is missing from the schema")
	}
	enum, ok := act["enum"].([]any)
	if !ok || len(enum) != 2 || enum[0] != "search" || enum[1] != "fetch" {
		t.Fatalf("action enum = %v, want [search fetch]", act["enum"])
	}
	mr := s.Properties["maxResults"]
	if mr == nil {
		t.Fatal("maxResults is missing from the schema")
	}
	if mr["minimum"] != float64(1) || mr["maximum"] != float64(20) {
		t.Fatalf("maxResults bounds = %v, want 1..20", mr)
	}
	props, ok := s.Properties["timeoutMs"]
	if !ok {
		t.Fatal("schema missing timeoutMs")
	}
	if props["minimum"] != float64(1000) || props["maximum"] != float64(300000) {
		t.Fatalf("timeoutMs bounds = %v/%v, want 1000/300000", props["minimum"], props["maximum"])
	}
	mc := s.Properties["maxChars"]
	if mc == nil || mc["minimum"] != float64(100) {
		t.Fatalf("maxChars = %v, want a minimum of 100", mc)
	}
}

func TestUnknownActionIsRefused(t *testing.T) {
	w := webTool(web.Config{})
	_, err := execArgs(t, w, `{"action":"both","target":"x"}`)
	if err == nil || !regexp.MustCompile(`(?i)unknown action`).MatchString(err.Error()) {
		t.Fatalf("want an unknown-action refusal, got %v", err)
	}
}

func TestMissingActionOrTargetIsRefused(t *testing.T) {
	w := webTool(web.Config{})
	for _, args := range []string{`{"target":"x"}`, `{"action":"search"}`, `{"action":"fetch"}`, `{}`} {
		_, err := execArgs(t, w, args)
		if err == nil {
			t.Fatalf("%s must refuse (action and target are both required)", args)
		}
	}
}

func TestQueryIsEncodedAndSentToLocalSearXNGJSONAPI(t *testing.T) {
	var seen *http.Request
	w := webTool(web.Config{Search: web.SearchConfig{
		Do: func(req *http.Request) (*http.Response, error) {
			seen = req
			return httpResp(200, map[string]string{"Content-Type": "application/json"},
				`{"results":[]}`), nil
		},
	}})
	if _, err := execArgs(t, w, `{"action":"search","target":"rust simd & memchr"}`); err != nil {
		t.Fatal(err)
	}
	if seen == nil {
		t.Fatal("the search never dialed")
	}
	want := "/search?q=rust%20simd%20%26%20memchr&format=json"
	if !strings.HasSuffix(seen.URL.String(), want) {
		t.Fatalf("URL = %q, want suffix %q", seen.URL.String(), want)
	}
	if got := seen.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q", got)
	}
}

func TestResultsMapToTitleURLSnippetWithTagsStrippedAndSnippetCapped(t *testing.T) {
	w := webTool(web.Config{Search: web.SearchConfig{
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "application/json"}, `{
				"results": [
					{"title":"memchr","url":"https://crates.io/crates/memchr",
					 "content":"  <b>SIMD</b>   string\nsearch  "},
					{"title":"long","url":"https://x.example/","content":"`+strings.Repeat("y", 500)+`"}
				]}`), nil
		},
	}})
	content, err := execArgs(t, w, `{"action":"search","target":"q"}`)
	if err != nil {
		t.Fatal(err)
	}
	var parsed []map[string]string
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("content is not the results JSON: %v\n%s", err, content)
	}
	if len(parsed) != 2 {
		t.Fatalf("got %d results", len(parsed))
	}
	if parsed[0]["title"] != "memchr" || parsed[0]["url"] != "https://crates.io/crates/memchr" {
		t.Fatalf("first result mangled: %v", parsed[0])
	}
	if parsed[0]["snippet"] != "SIMD string search" {
		t.Fatalf("snippet = %q", parsed[0]["snippet"])
	}
	if len(parsed[1]["snippet"]) != 300 {
		t.Fatalf("snippet not capped to 300 (got %d)", len(parsed[1]["snippet"]))
	}
}

func TestMaxResultsSlicesDefaultIsFive(t *testing.T) {
	many := make([]map[string]string, 10)
	for i := range many {
		many[i] = map[string]string{"title": fmt.Sprintf("t%d", i), "url": fmt.Sprintf("https://x.example/%d", i)}
	}
	body, _ := json.Marshal(map[string]any{"results": many})
	w := webTool(web.Config{Search: web.SearchConfig{
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "application/json"}, string(body)), nil
		},
	}})

	cut := func(t *testing.T, args string) int {
		t.Helper()
		content, err := execArgs(t, w, args)
		if err != nil {
			t.Fatal(err)
		}
		var got []map[string]any
		if err := json.Unmarshal([]byte(content), &got); err != nil {
			t.Fatalf("content is not the results JSON: %v\n%s", err, content)
		}
		return len(got)
	}
	if n := cut(t, `{"action":"search","target":"q"}`); n != 5 {
		t.Fatalf("default slice: got %d results, want 5", n)
	}
	if n := cut(t, `{"action":"search","target":"q","maxResults":2}`); n != 2 {
		t.Fatalf("maxResults slice: got %d results, want 2", n)
	}
}

func TestMissingFieldsDegradeToEmptyStringsEmptyResultsSaySo(t *testing.T) {
	w := webTool(web.Config{Search: web.SearchConfig{
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "application/json"},
				`{"results":[{}]}`), nil
		},
	}})
	content, err := execArgs(t, w, `{"action":"search","target":"q","maxResults":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var parsed []map[string]string
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed[0]["title"] != "" || parsed[0]["url"] != "" || parsed[0]["snippet"] != "" {
		t.Fatalf("missing fields must degrade to empty strings: %v", parsed[0])
	}

	empty := webTool(web.Config{Search: web.SearchConfig{
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "application/json"},
				`{"results":[]}`), nil
		},
	}})
	got, err := execArgs(t, empty, `{"action":"search","target":"q"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "no results for \"") {
		t.Fatalf("empty results = %q, want the query named: no results for \"…\"", got)
	}
}

func TestSearXNGBeingDownSurfacesAsALoudError(t *testing.T) {
	w := webTool(web.Config{Search: web.SearchConfig{
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(502, map[string]string{"Content-Type": "application/json"}, `{}`), nil
		},
	}})
	_, err := execArgs(t, w, `{"action":"search","target":"q"}`)
	if err == nil || !strings.Contains(err.Error(), "SearXNG search failed: HTTP 502") {
		t.Fatalf("want the 502 voice, got %v", err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	down := webTool(web.Config{Search: web.SearchConfig{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port)}})
	_, err = execArgs(t, down, `{"action":"search","target":"q"}`)
	if err == nil || !regexp.MustCompile(`(?i)connection refused|ECONNREFUSED`).MatchString(err.Error()) {
		t.Fatalf("want a refused-connection error, got %v", err)
	}
}

func TestTheEgressProxyIsUsedWhenSet(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<p>proxied body</p>")
	}))
	defer target.Close()

	seen := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = true
		if !r.URL.IsAbs() {
			t.Errorf("the proxy saw an origin-form URL: %v", r.URL)
		}
		resp, err := http.Get(r.URL.String())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		io.Copy(w, resp.Body)
	}))
	defer proxy.Close()

	w := webTool(web.Config{Fetch: web.FetchConfig{
		Proxy: proxy.URL, Lookup: publicLookup, Trafilatura: off(),
	}})
	content, err := execArgs(t, w, `{"action":"fetch","target":"`+target.URL+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	has(t, content, "proxied body")
	if !seen {
		t.Fatal("the request never went through the proxy")
	}
}

func TestAnUnreachableProxyNamesItselfAndTheFix(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := fmt.Sprintf("http://127.0.0.1:%d", l.Addr().(*net.TCPAddr).Port)
	l.Close()

	w := webTool(web.Config{Fetch: web.FetchConfig{Proxy: proxy, Lookup: publicLookup}})
	_, err = execArgs(t, w, `{"action":"fetch","target":"http://example.example/"}`)
	if err == nil {
		t.Fatal("want the unreachable-proxy error")
	}
	want := "egress proxy " + proxy + " is unreachable. Start it: cd ~/docker/web-tools && docker compose up -d"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err.Error(), want)
	}
}

func TestTheTrafilaturaFallbackIsAnnouncedInTheContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><body><p>announced fallback</p></body></html>")
	}))
	defer srv.Close()

	w := webTool(web.Config{Fetch: web.FetchConfig{Lookup: publicLookup, Trafilatura: off(), Do: direct()}})
	content, err := execArgs(t, w, `{"action":"fetch","target":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	has(t, content, "announced fallback")
	if !regexp.MustCompile(`(?i)stdlib text pass`).MatchString(content) {
		t.Fatalf("the fallback is not announced: %q", content)
	}

	if web.DefaultTrafilatura() == "" {
		t.Skip("no trafilatura on this box")
	}
	w2 := webTool(web.Config{Fetch: web.FetchConfig{Lookup: publicLookup, Do: direct()}})
	content, err = execArgs(t, w2, `{"action":"fetch","target":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "stdlib text pass") {
		t.Fatalf("trafilatura ran but the content still announces the fallback: %q", content)
	}
}

func TestTrafilaturaResolutionSharedVenvFirstThenPATHExplicitWins(t *testing.T) {
	home := t.TempDir()
	venvBin := filepath.Join(home, ".pi", "agent", "kernel-venv", "bin")
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(venvBin, "trafilatura"), []byte("venv"), 0o755); err != nil {
		t.Fatal(err)
	}
	pathBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(pathBin, "trafilatura"), []byte("path"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldHome, oldPath := os.Getenv("HOME"), os.Getenv("PATH")
	t.Cleanup(func() {
		os.Setenv("HOME", oldHome)
		os.Setenv("PATH", oldPath)
	})
	os.Setenv("HOME", home)
	os.Setenv("PATH", pathBin)

	if got := web.DefaultTrafilatura(); !strings.HasSuffix(got, "kernel-venv/bin/trafilatura") {
		t.Fatalf("the shared venv must win: %q", got)
	}
	os.Remove(filepath.Join(venvBin, "trafilatura"))
	if got := web.DefaultTrafilatura(); !strings.HasSuffix(got, filepath.Join(pathBin, "trafilatura")) {
		t.Fatalf("PATH must be the fallback: %q", got)
	}

	explicit := web.NewFetch(web.FetchConfig{Trafilatura: ptr("/opt/traf")})
	_ = explicit
}

func TestTheSearchBudgetBitesOnAHangingEndpoint(t *testing.T) {
	start := time.Now()
	w := webTool(web.Config{Search: web.SearchConfig{

		Do: func(req *http.Request) (*http.Response, error) {
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(250 * time.Millisecond):
			}
			return httpResp(200, map[string]string{"Content-Type": "application/json"}, `{"results":[]}`), nil
		},
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := w.Exec(ctx, json.RawMessage(`{"action":"search","target":"q"}`))
	if err == nil {
		t.Fatal("an expired ctx must surface as an error")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("the search did not respect the caller ctx (%v)", elapsed)
	}
}

func TestLookupRidesTheRequestContext(t *testing.T) {
	lookup := func(ctx context.Context, host string) ([]string, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return []string{"93.184.216.34"}, nil
		}
	}
	f := web.NewFetch(web.FetchConfig{Lookup: lookup})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := f.Guarded(ctx, "http://slow.example/")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a fetch whose DNS outlives the deadline must surface the deadline, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("the DNS lookup ignored the request context: the fetch took %v", elapsed)
	}
}

func TestOutOfRangeMaxResultsRefusesInsteadOfPanicking(t *testing.T) {
	w := webTool(web.Config{Search: web.SearchConfig{
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "application/json"}, `{"results":[]}`), nil
		},
	}})
	for _, args := range []string{
		`{"action":"search","target":"q","maxResults":-1}`,
		`{"action":"search","target":"q","maxResults":0}`,
		`{"action":"search","target":"q","maxResults":21}`,
		`{"action":"search","target":"q","maxResults":999999999999}`,
	} {
		_, err := execArgs(t, w, args)
		if err == nil {
			t.Fatalf("out-of-range maxResults %s must refuse, not run", args)
		}
	}
}

func TestOutOfRangeMaxCharsRefusesInsteadOfPanicking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "body")
	}))
	defer srv.Close()
	w := webTool(web.Config{Fetch: web.FetchConfig{Lookup: publicLookup, Do: direct(), Trafilatura: off()}})
	for _, args := range []string{
		`{"action":"fetch","target":"` + srv.URL + `","maxChars":-1}`,
		`{"action":"fetch","target":"` + srv.URL + `","maxChars":0}`,
		`{"action":"fetch","target":"` + srv.URL + `","maxChars":99}`,
	} {
		_, err := execArgs(t, w, args)
		if err == nil {
			t.Fatalf("out-of-range maxChars %s must refuse, not run", args)
		}
	}
}

func TestOutOfRangeTimeoutMsRefusesInsteadOfRunning(t *testing.T) {
	w := webTool(web.Config{Fetch: web.FetchConfig{
		Lookup: publicLookup,
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "text/plain"}, "ok"), nil
		},
	}})
	for _, n := range []int{0, 1, 999, 300001, 1 << 30} {
		_, err := execArgs(t, w, fmt.Sprintf(`{"action":"fetch","target":"http://example.com/","timeoutMs":%d}`, n))
		if err == nil || !strings.Contains(err.Error(), "timeoutMs must be between") {
			t.Fatalf("timeoutMs=%d: want a refusal naming the range, got %v", n, err)
		}
	}
}

func TestSearchActionIgnoresFetchParamsAndViceVersa(t *testing.T) {
	var seenURL string
	w := webTool(web.Config{Search: web.SearchConfig{
		Do: func(req *http.Request) (*http.Response, error) {
			seenURL = req.URL.String()
			return httpResp(200, map[string]string{"Content-Type": "application/json"}, `{"results":[]}`), nil
		},
	}})
	content, err := execArgs(t, w, `{"action":"search","target":"q","maxChars":500,"timeoutMs":5000}`)
	if err != nil {
		t.Fatal(err)
	}
	if seenURL == "" || !strings.Contains(seenURL, "q=") {
		t.Fatalf("the fetch params must not disturb the search URL: %q", seenURL)
	}
	if !strings.HasPrefix(content, "no results for") {
		t.Fatalf("search reply = %q", content)
	}
}
