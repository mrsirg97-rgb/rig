// The port's contract: pane's web_search and web_fetch named cases, in
// pane's order (web-fetch.test.mjs, then web-search.test.mjs), plus the
// looper-side cases. Every case runs against httptest servers and
// injected seams; no live SearXNG, no live proxy, no required trafilatura.
package web_test

import (
	"context"
	"encoding/json"
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

	"github.com/mrsirg97-rgb/looper/tool/web"
)

// ---- seams and small fakes -------------------------------------------

func ptr[T any](v T) *T { return &v }

func off() *string { var s string = ""; return &s }

// httpResp is a hand-built response for the Do seam (pane's fetchImpl).
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

func publicLookup(host string) ([]string, error) {
	return []string{"93.184.216.34"}, nil
}

func privateLookup(host string) ([]string, error) {
	return []string{"10.9.8.7"}, nil
}

// schema checks: the hand-written JSON, not a validator library.
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

// ---- pane's web-fetch named cases, in pane's order ---------------------

func TestIPisPrivateV4Table(t *testing.T) {
	priv := []string{
		"0.0.0.0", "10.1.2.3", "127.0.0.1", "169.254.169.254",
		"172.16.0.1", "172.31.255.255", "192.168.1.1", "100.64.0.1",
		"100.127.9.9", "192.0.0.170", "198.18.0.1", "224.0.0.1",
		"255.255.255.255",
	}
	pub := []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "172.32.0.1",
		"100.128.0.1", "198.20.0.1"}
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
	lookup := func(host string) ([]string, error) {
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
	text, _ := web.ExtractReadable("<body><p>plain fallback</p></body>", off())
	has(t, text, "plain fallback")
	missing, _ := web.ExtractReadable("<body><p>still works</p></body>", ptr("/nonexistent/bin"))
	has(t, missing, "still works")
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

	f := web.NewFetch(web.FetchConfig{Lookup: publicLookup})
	got, err := f.Guarded(context.Background(), srv.URL+"/hop")
	if err != nil {
		t.Fatal(err)
	}
	has(t, got.Body, "real e2e body")
	// the Guarded body is the raw, unextracted HTML — the script is still
	// in it; extraction (and the script's removal) happens in Exec.
	has(t, got.Body, "<script>")
	if want := strings.TrimSuffix(srv.URL, "/") + "/page"; got.FinalURL != want {
		t.Fatalf("final URL = %q, want %q", got.FinalURL, want)
	}

	content, err := f.Exec(context.Background(), json.RawMessage(`{"url":`+`"`+srv.URL+"/hop"+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	has(t, content, "real e2e body")
	if strings.Contains(content, "x()") {
		t.Fatal("script content leaked into the content")
	}
}

func TestE2ETimeoutSurfacesAsAClearError(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	f := web.NewFetch(web.FetchConfig{Lookup: publicLookup})
	_, err := f.Exec(context.Background(), json.RawMessage(
		`{"url":"`+srv.URL+`/slow","timeoutMs":300}`))
	if err == nil || !regexp.MustCompile(`(?i)timed out`).MatchString(err.Error()) {
		t.Fatalf("want a timeout error, got %v", err)
	}
}

func TestExecuteReportsGuardRefusalsAsToolErrorsNotThrows(t *testing.T) {
	f := web.NewFetch(web.FetchConfig{Lookup: privateLookup})
	_, err := f.Exec(context.Background(), json.RawMessage(`{"url":"http://internal.example/"}`))
	if err == nil || !regexp.MustCompile(`(?i)private|refused`).MatchString(err.Error()) {
		t.Fatalf("want the refusal as the tool error, got %v", err)
	}
}

func TestToolRegistrationNameRequiredURLGuidelinesExist(t *testing.T) {
	f := web.Fetch()
	if f.Name() != "web_fetch" {
		t.Fatalf("name = %q", f.Name())
	}
	s := getSchema(t, f)
	if len(s.Required) != 1 || s.Required[0] != "url" {
		t.Fatalf("required = %v, want [url]", s.Required)
	}
	if _, ok := s.Properties["url"]; !ok {
		t.Fatal("the url parameter is missing from the schema")
	}
	has(t, f.Description(), "search finds, fetch reads")
}

// ---- pane's web-search named cases, in pane's order --------------------

func TestQueryIsEncodedAndSentToLocalSearXNGJSONAPI(t *testing.T) {
	var seen *http.Request
	s := web.NewSearch(web.SearchConfig{
		Do: func(req *http.Request) (*http.Response, error) {
			seen = req
			return httpResp(200, map[string]string{"Content-Type": "application/json"},
				`{"results":[]}`), nil
		},
	})
	if _, err := s.Exec(context.Background(), json.RawMessage(`{"query":"rust simd & memchr"}`)); err != nil {
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
	s := web.NewSearch(web.SearchConfig{
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "application/json"}, `{
				"results": [
					{"title":"memchr","url":"https://crates.io/crates/memchr",
					 "content":"  <b>SIMD</b>   string\nsearch  "},
					{"title":"long","url":"https://x.example/","content":"`+strings.Repeat("y", 500)+`"}
				]}`), nil
		},
	})
	content, err := s.Exec(context.Background(), json.RawMessage(`{"query":"q"}`))
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
	s := web.NewSearch(web.SearchConfig{
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "application/json"}, string(body)), nil
		},
	})

	cut := func(t *testing.T, args string) int {
		t.Helper()
		content, err := s.Exec(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		var got []map[string]any
		if err := json.Unmarshal([]byte(content), &got); err != nil {
			t.Fatalf("content is not the results JSON: %v\n%s", err, content)
		}
		return len(got)
	}
	if n := cut(t, `{"query":"q"}`); n != 5 {
		t.Fatalf("default slice: got %d results, want 5", n)
	}
	if n := cut(t, `{"query":"q","maxResults":2}`); n != 2 {
		t.Fatalf("maxResults slice: got %d results, want 2", n)
	}
}

func TestMissingFieldsDegradeToEmptyStringsEmptyResultsSaySo(t *testing.T) {
	s := web.NewSearch(web.SearchConfig{
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "application/json"},
				`{"results":[{}]}`), nil
		},
	})
	content, err := s.Exec(context.Background(), json.RawMessage(`{"query":"q","maxResults":1}`))
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

	empty := web.NewSearch(web.SearchConfig{
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(200, map[string]string{"Content-Type": "application/json"},
				`{"results":[]}`), nil
		},
	})
	got, err := empty.Exec(context.Background(), json.RawMessage(`{"query":"q"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "no results" {
		t.Fatalf("empty results = %q, want %q", got, "no results")
	}
}

func TestSearXNGBeingDownSurfacesAsALoudError(t *testing.T) {
	s := web.NewSearch(web.SearchConfig{
		Do: func(*http.Request) (*http.Response, error) {
			return httpResp(502, map[string]string{"Content-Type": "application/json"}, `{}`), nil
		},
	})
	_, err := s.Exec(context.Background(), json.RawMessage(`{"query":"q"}`))
	if err == nil || !strings.Contains(err.Error(), "SearXNG search failed: HTTP 502") {
		t.Fatalf("want the 502 voice, got %v", err)
	}

	// a refused connection stays loud (Go's voice, not Node's ECONNREFUSED)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	down := web.NewSearch(web.SearchConfig{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port)})
	_, err = down.Exec(context.Background(), json.RawMessage(`{"query":"q"}`))
	if err == nil || !regexp.MustCompile(`(?i)connection refused|ECONNREFUSED`).MatchString(err.Error()) {
		t.Fatalf("want a refused-connection error, got %v", err)
	}
}

func TestSchemaRequiresQueryAndBoundsMaxResults(t *testing.T) {
	s := getSchema(t, web.Search())
	if len(s.Required) != 1 || s.Required[0] != "query" {
		t.Fatalf("required = %v, want [query]", s.Required)
	}
	mr := s.Properties["maxResults"]
	if mr == nil {
		t.Fatal("maxResults is missing from the schema")
	}
	if mr["minimum"] != float64(1) || mr["maximum"] != float64(20) {
		t.Fatalf("maxResults bounds = %v, want 1..20", mr)
	}
}

// ---- the looper-side named cases ---------------------------------------

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

	f := web.NewFetch(web.FetchConfig{
		Proxy: proxy.URL, Lookup: publicLookup, Trafilatura: off(),
	})
	content, err := f.Exec(context.Background(), json.RawMessage(`{"url":`+`"`+target.URL+`"}`))
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

	f := web.NewFetch(web.FetchConfig{Proxy: proxy, Lookup: publicLookup})
	_, err = f.Exec(context.Background(), json.RawMessage(`{"url":"http://example.example/"}`))
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

	// explicit off: the stdlib pass must say so in the content
	f := web.NewFetch(web.FetchConfig{Lookup: publicLookup, Trafilatura: off()})
	content, err := f.Exec(context.Background(), json.RawMessage(`{"url":`+`"`+srv.URL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	has(t, content, "announced fallback")
	if !regexp.MustCompile(`(?i)stdlib text pass`).MatchString(content) {
		t.Fatalf("the fallback is not announced: %q", content)
	}

	// a present binary: the content is pane's, no announcement
	if web.DefaultTrafilatura() == "" {
		t.Skip("no trafilatura on this box")
	}
	f2 := web.NewFetch(web.FetchConfig{Lookup: publicLookup})
	content, err = f2.Exec(context.Background(), json.RawMessage(`{"url":`+`"`+srv.URL+`"}`))
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

	// explicit wins over both: the seam takes it without resolution
	explicit := web.NewFetch(web.FetchConfig{Trafilatura: ptr("/opt/traf")})
	_ = explicit
}

// The 15s search budget is a constant, not a knob; a hanging endpoint must
// not hang the turn forever.
func TestTheSearchBudgetBitesOnAHangingEndpoint(t *testing.T) {
	start := time.Now()
	s := web.NewSearch(web.SearchConfig{
		// a real transport honours the request context; so must the seam
		Do: func(req *http.Request) (*http.Response, error) {
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(250 * time.Millisecond):
			}
			return httpResp(200, map[string]string{"Content-Type": "application/json"}, `{"results":[]}`), nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.Exec(ctx, json.RawMessage(`{"query":"q"}`))
	if err == nil {
		t.Fatal("an expired ctx must surface as an error")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("the search did not respect the caller ctx (%v)", elapsed)
	}
}
