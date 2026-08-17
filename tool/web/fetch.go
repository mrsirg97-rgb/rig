package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	maxBytesDefault  = 5 * 1024 * 1024
	maxChars         = 20_000
	defaultTimeoutMs = 30_000
	maxHops          = 5
	trafilaturaTime  = 20 * time.Second
	trafilaturaCap   = 2 * maxBytesDefault
)

const description = "Fetch a public http(s) URL and return its readable text (article extraction for HTML, " +
	"raw body for JSON/plain). Redirects are followed and re-checked; private/internal " +
	"addresses are refused. Output is capped; a loud [TRUNCATED] marker states the full size."

// guidelines is folded after the description since rig's tool surface
// carries no separate guidelines channel.
const guidelines = "Guidelines: " +
	"search finds, fetch reads; snippets are not the page. " +
	"web pages and textual APIs only; local files -> read, local services -> bash. " +
	"[TRUNCATED] -> refetch with larger maxChars only if the missing part matters."

// schemaJSON: required: url; maxChars >= 100, timeoutMs >= 1000.
const schemaJSON = `{
	"type": "object",
	"properties": {
		"url": {"type": "string", "description": "Absolute http(s) URL to fetch"},
		"maxChars": {"type": "integer", "description": "Max chars returned (default 20000)", "minimum": 100},
		"timeoutMs": {"type": "integer", "description": "Total timeout in ms (default 30000)", "minimum": 1000}
	},
	"required": ["url"]
}`

var (
	// the textual content-type allow-list.
	textualRE = regexp.MustCompile(`(?i)^(text/|application/(json|xml|xhtml\+xml|rss\+xml|atom\+xml|[\w.-]+\+(json|xml))(\s*;|$))`)
	// the html/xml content-type test that routes through extraction.
	htmlishRE = regexp.MustCompile(`(?i)html|xml`)
)

// LookupFn is the DNS seam: host in, addresses out.
type LookupFn func(host string) ([]string, error)

// FetchConfig is the injection seam: the egress proxy, the trafilatura
// binary, the DNS and transport seams, the byte cap. Trafilatura is
// tri-state: nil = the default resolution (shared venv, then PATH);
// non-nil = explicit (empty = off).
type FetchConfig struct {
	Proxy       string
	Trafilatura *string
	Lookup      LookupFn
	Do          func(*http.Request) (*http.Response, error)
	MaxBytes    int
}

// Fetched is the result of one guarded fetch.
type Fetched struct {
	FinalURL      string
	Status        int
	ContentType   string
	Body          string
	BodyTruncated bool
}

// fetch is the web_fetch tool: the guarded fetch plus extraction.
// Concrete type unexported with named constructors, the core/tool.go
// house shape.
type fetch struct {
	proxy    string
	traf     string // resolved binary; "" = off (the stdlib pass carries)
	lookup   LookupFn
	do       func(*http.Request) (*http.Response, error)
	maxBytes int
}

// Fetch is the defaults: the web-tools proxy on loopback, trafilatura
// resolved from the shared venv then PATH.
func Fetch() *fetch {
	return NewFetch(FetchConfig{Proxy: DefaultProxy})
}

func NewFetch(cfg FetchConfig) *fetch {
	f := &fetch{
		proxy:    cfg.Proxy,
		lookup:   cfg.Lookup,
		maxBytes: cfg.MaxBytes,
	}
	if f.maxBytes == 0 {
		f.maxBytes = maxBytesDefault
	}
	if f.lookup == nil {
		f.lookup = func(host string) ([]string, error) {
			return net.DefaultResolver.LookupHost(context.Background(), host)
		}
	}
	if cfg.Trafilatura != nil {
		f.traf = *cfg.Trafilatura
	} else {
		f.traf = DefaultTrafilatura()
	}
	if cfg.Do != nil {
		f.do = cfg.Do
		return f
	}
	tr := &http.Transport{
		MaxIdleConns:          8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if f.proxy != "" {
		if pu, err := url.Parse(f.proxy); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	// Redirects are manual: the client returns 3xx, the loop owns the
	// hop, and every hop is re-guarded before the next dial.
	f.do = (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}).Do
	return f
}

// Name implements core.Tool.
func (f *fetch) Name() string { return "web_fetch" }

// Description implements core.Tool: description with guidelines folded
// (the house convention).
func (f *fetch) Description() string { return description + "\n\n" + guidelines }

// Schema implements core.Tool.
func (f *fetch) Schema() json.RawMessage { return json.RawMessage(schemaJSON) }

// IPisPrivate refuses the v4 private and
// CGNAT ranges, link-local, 192.0.0.0/24, benchmarking, and multicast
// through broadcast; the v6 loopback, unique-local, link-local, and
// multicast ranges; IPv4-mapped v6 folded to the v4 table.
func IPisPrivate(ip string) bool {
	normalized := strings.ToLower(ip)
	v4 := normalized
	if strings.HasPrefix(normalized, "::ffff:") {
		v4 = normalized[len("::ffff:"):]
	}
	if strings.Contains(v4, ".") {
		parts := strings.Split(v4, ".")
		if len(parts) != 4 {
			return true
		}
		octets := make([]int, 4)
		for i, s := range parts {
			n, err := strconv.Atoi(s)
			if err != nil || n > 255 {
				return true
			}
			octets[i] = n
		}
		a, b := octets[0], octets[1]
		switch {
		case a == 0, a == 10, a == 127:
			return true
		case a == 100 && b >= 64 && b <= 127: // CGNAT
			return true
		case a == 169 && b == 254: // link-local
			return true
		case a == 172 && b >= 16 && b <= 31:
			return true
		case a == 192 && b == 168:
			return true
		case a == 192 && b == 0 && octets[2] == 0: // IETF protocol assignments
			return true
		case a == 198 && (b == 18 || b == 19): // benchmarking
			return true
		case a >= 224: // multicast through broadcast
			return true
		}
		return false
	}
	switch {
	case normalized == "::", normalized == "::1":
		return true
	case strings.HasPrefix(normalized, "fc"), strings.HasPrefix(normalized, "fd"): // unique-local
		return true
	case strings.HasPrefix(normalized, "fe8"), strings.HasPrefix(normalized, "fe9"),
		strings.HasPrefix(normalized, "fea"), strings.HasPrefix(normalized, "feb"): // link-local
		return true
	case strings.HasPrefix(normalized, "ff"): // multicast
		return true
	}
	return false
}

// guardedURL: parse (against the base for
// redirect hops), http(s) only, resolve, and refuse any private address
// before any connection.
func guardedURL(raw string, base *url.URL, lookup LookupFn) (*url.URL, error) {
	var u *url.URL
	var err error
	if base != nil {
		u, err = base.Parse(raw) // relative or absolute
	} else {
		u, err = url.Parse(raw)
	}
	if err != nil || u.Scheme == "" {
		// schemeless input parses in Go where WHATWG throws; refuse it too.
		return nil, fmt.Errorf("invalid URL: %s", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http(s) is fetchable, got %s", u.Scheme)
	}
	host := u.Hostname()
	addrs, err := lookup(host)
	if err != nil || len(addrs) == 0 {
		return nil, fmt.Errorf("cannot resolve host: %s", host)
	}
	for _, a := range addrs {
		if IPisPrivate(a) {
			return nil, fmt.Errorf("refused: %s resolves to private address %s", host, a)
		}
	}
	return u, nil
}

// Guarded is the guarded fetch: the timeout is the caller's ctx (one
// budget for the whole fetch, all hops included) and the hop loop owns
// the redirects.
func (f *fetch) Guarded(ctx context.Context, raw string) (Fetched, error) {
	start := time.Now()
	budgetMs := 0
	if d, ok := ctx.Deadline(); ok {
		budgetMs = int(d.Sub(start) / time.Millisecond)
	}

	u, err := guardedURL(raw, nil, f.lookup)
	if err != nil {
		return Fetched{}, err
	}
	current := u.String()
	for hop := 0; ; hop++ {
		if hop > maxHops {
			return Fetched{}, fmt.Errorf("too many redirects (>%d) starting from %s", maxHops, raw)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return Fetched{}, fmt.Errorf("invalid URL: %s", raw)
		}
		req.Header.Set("User-Agent", "pi-web-fetch/1.0")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/*;q=0.9,*/*;q=0.5")

		res, err := f.do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				if errors.Is(ctxErr, context.DeadlineExceeded) {
					return Fetched{}, fmt.Errorf("timed out after %dms fetching %s", budgetMs, current)
				}
				return Fetched{}, ctxErr
			}
			if f.proxy != "" && isConnRefused(err) {
				return Fetched{}, fmt.Errorf(
					"egress proxy %s is unreachable. Start it: cd ~/docker/web-tools && docker compose up -d", f.proxy)
			}
			return Fetched{}, err
		}

		if loc := res.Header.Get("Location"); res.StatusCode >= 300 && res.StatusCode < 400 && loc != "" {
			res.Body.Close()
			nu, err := guardedURL(loc, u, f.lookup) // every hop re-guarded
			if err != nil {
				return Fetched{}, err
			}
			u = nu
			current = nu.String()
			continue
		}

		if res.StatusCode < 200 || res.StatusCode >= 300 {
			res.Body.Close()
			return Fetched{}, fmt.Errorf("HTTP %d from %s", res.StatusCode, u.String())
		}
		ct := res.Header.Get("Content-Type")
		if ct == "" {
			ct = "text/plain"
		}
		if !textualRE.MatchString(ct) {
			res.Body.Close()
			return Fetched{}, fmt.Errorf("unsupported content type %s; only textual responses are fetchable",
				strings.Split(ct, ";")[0])
		}
		if cl, err := strconv.Atoi(res.Header.Get("Content-Length")); err == nil && cl > f.maxBytes {
			res.Body.Close()
			return Fetched{}, fmt.Errorf("response too large: %d bytes declared, cap is %d", cl, f.maxBytes)
		}
		body, truncated, err := readCapped(res.Body, f.maxBytes)
		res.Body.Close()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Fetched{}, ctxErr
			}
			return Fetched{}, err
		}
		return Fetched{
			FinalURL:      u.String(),
			Status:        res.StatusCode,
			ContentType:   ct,
			Body:          body,
			BodyTruncated: truncated,
		}, nil
	}
}

// readCapped reads to the byte cap; the truncation flag is set only when
// there was actually more (a one-byte probe, so an exact fit is not
// flagged).
func readCapped(r io.Reader, max int) (string, bool, error) {
	buf, err := io.ReadAll(io.LimitReader(r, int64(max)))
	if err != nil {
		return string(buf), false, err
	}
	if _, err := r.Read([]byte{0}); err == nil {
		return string(buf), true, nil
	}
	return string(buf), false, nil
}

// isConnRefused detects ECONNREFUSED for the proxy-unreachable message;
// the string check is the portable backstop.
func isConnRefused(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			if errno, ok := sysErr.Err.(syscall.Errno); ok {
				return errno == syscall.ECONNREFUSED
			}
		}
		if errno, ok := opErr.Err.(syscall.Errno); ok {
			return errno == syscall.ECONNREFUSED
		}
	}
	return strings.Contains(err.Error(), "connection refused")
}

// Exec implements core.Tool; errors carry the web_fetch: prefix.
func (f *fetch) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		URL       string `json:"url"`
		MaxChars  *int   `json:"maxChars"`
		TimeoutMs *int   `json:"timeoutMs"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("web_fetch: bad args: %v", err)
	}
	if p.URL == "" {
		return "", errors.New("web_fetch: no url supplied")
	}
	maxC := maxChars
	if p.MaxChars != nil {
		maxC = *p.MaxChars
	}
	timeoutMs := defaultTimeoutMs
	if p.TimeoutMs != nil {
		timeoutMs = *p.TimeoutMs
	}

	// The budget is the whole fetch, all hops included; the timeout
	// message reconstructs the ms from the deadline at the moment it fires.
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	fetched, err := f.Guarded(cctx, p.URL)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %s", err)
	}
	// html/xml goes through the extractor,
	// everything else is the raw body trimmed.
	var readable string
	if htmlishRE.MatchString(fetched.ContentType) {
		var note string
		readable, note = ExtractReadable(fetched.Body, &f.traf)
		if note != "" {
			readable += "\n\n" + note
		}
	} else {
		readable = strings.TrimSpace(fetched.Body)
	}

	// Order: the char cap first, then the byte-cap marker, then the
	// empty-content fallback.
	text := CapChars(readable, maxC)
	if fetched.BodyTruncated {
		text += "\n\n[TRUNCATED: download hit the byte cap; content is partial.]"
	}
	if text == "" {
		text = "(no content extracted)"
	}
	return text, nil
}

// ---- extraction ---------------------------------------------------------

// HtmlToText is the stdlib text pipeline: the non-text blocks and
// comments out, block boundaries to newlines, the rest of the tags to
// spaces, entities decoded, lines collapsed. RE2 has no backreferences,
// so the block-tag pairs are five non-greedy patterns (equivalent for
// matched pairs, the only sane HTML).
var (
	blockTags  = []string{"script", "style", "noscript", "svg", "head"}
	commentRE  = regexp.MustCompile(`(?s)<!--.*?-->`)
	blockEndRE = regexp.MustCompile(`(?i)</(p|div|li|tr|h[1-6]|blockquote|pre|section|article)>|<br\s*\/?>`)
	tagRE      = regexp.MustCompile(`<[^>]+>`)
	inlineWSE  = regexp.MustCompile(`[ \t\r]+`)
	blank3RE   = regexp.MustCompile(`\n{3,}`)
)

func HtmlToText(html string) string {
	for _, tag := range blockTags {
		re := regexp.MustCompile(`(?is)<` + tag + `\b.*?</` + tag + `>`)
		html = re.ReplaceAllString(html, " ")
	}
	html = commentRE.ReplaceAllString(html, " ")
	html = blockEndRE.ReplaceAllString(html, "\n")
	html = tagRE.ReplaceAllString(html, " ")
	html = decodeEntities(html)
	lines := strings.Split(html, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(inlineWSE.ReplaceAllString(l, " "))
	}
	return strings.TrimSpace(blank3RE.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// decodeEntities decodes the named entity table plus the numeric forms.
var entityRE = regexp.MustCompile(`&(#x?[0-9a-fA-F]+|\w+);`)

var entities = map[string]string{
	"amp":  "&",
	"lt":   "<",
	"gt":   ">",
	"quot": `"`,
	"apos": "'",
	"nbsp": " ",
	"#39":  "'",
}

func decodeEntities(text string) string {
	return entityRE.ReplaceAllStringFunc(text, func(m string) string {
		name := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(m, "&"), ";"))
		if v, ok := entities[name]; ok {
			return v
		}
		if strings.HasPrefix(name, "#x") {
			if n, err := strconv.ParseUint(name[2:], 16, 32); err == nil && n > 0 {
				return string(rune(n))
			}
			return m
		}
		if strings.HasPrefix(name, "#") {
			if n, err := strconv.ParseUint(name[1:], 10, 32); err == nil && n > 0 {
				return string(rune(n))
			}
			return m
		}
		return m
	})
}

// CapChars caps at max runes and appends the truncation marker.
func CapChars(text string, max int) string {
	r := []rune(text)
	if len(r) <= max {
		return text
	}
	return string(r[:max]) + fmt.Sprintf(
		"\n\n[TRUNCATED: showing %d of %d chars. Refetch with a larger maxChars or a more specific URL.]",
		max, len(r))
}

// runTrafilatura: html on stdin, stdout capped at trafilaturaCap, 20s
// budget, failure as an error (the caller decides the fallback).
func runTrafilatura(bin, html string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), trafilaturaTime)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader(html)
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", errors.New(strings.SplitN(detail, "\n", 2)[0])
	}
	if stdout.Len() > trafilaturaCap {
		stdout.Truncate(trafilaturaCap)
	}
	return stdout.String(), nil
}

// ExtractReadable extracts readable text; the second return is a footer
// naming the fallback ("" means trafilatura worked). The fallback is
// announced, never silent: a quiet quality change is the kind of silent
// behaviour the house rules refuse.
func ExtractReadable(html string, trafilatura *string) (string, string) {
	bin := DefaultTrafilatura()
	explicit := trafilatura != nil
	if explicit {
		bin = *trafilatura
	}
	if bin == "" {
		return HtmlToText(html), "[trafilatura unavailable; stdlib text pass used]"
	}
	out, err := runTrafilatura(bin, html)
	if err != nil {
		return HtmlToText(html), "[trafilatura failed (" + err.Error() + "); stdlib text pass used]"
	}
	if strings.TrimSpace(out) == "" {
		return HtmlToText(html), "[trafilatura produced no output; stdlib text pass used]"
	}
	return strings.TrimSpace(out), ""
}
