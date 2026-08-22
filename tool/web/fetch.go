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

const description = "fetch a public http(s) URL as readable text (article extraction for HTML, the raw body " +
	"for JSON and plain text)."

const guidelines = "Guidelines: search finds, fetch reads — snippets are not the page; web pages and textual " +
	"APIs only (local files -> read, local services -> bash). Reply: the text, capped with a [TRUNCATED] " +
	"marker naming the full size — refetch with a larger maxChars only if the missing part matters; " +
	"private and internal addresses are refused."

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
	textualRE = regexp.MustCompile(`(?i)^(text/|application/(json|xml|xhtml\+xml|rss\+xml|atom\+xml|[\w.-]+\+(json|xml))(\s*;|$))`)

	htmlishRE = regexp.MustCompile(`(?i)html|xml`)
)

type LookupFn func(host string) ([]string, error)

type FetchConfig struct {
	Proxy       string
	Trafilatura *string
	Lookup      LookupFn
	Do          func(*http.Request) (*http.Response, error)
	MaxBytes    int
}

type Fetched struct {
	FinalURL      string
	Status        int
	ContentType   string
	Body          string
	BodyTruncated bool
}

type fetch struct {
	proxy    string
	traf     string
	lookup   LookupFn
	do       func(*http.Request) (*http.Response, error)
	maxBytes int
}

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

	f.do = (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}).Do
	return f
}

func (f *fetch) Name() string { return "web_fetch" }

func (f *fetch) Description() string { return description + "\n\n" + guidelines }

func (f *fetch) Schema() json.RawMessage { return json.RawMessage(schemaJSON) }

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
		case a == 100 && b >= 64 && b <= 127:
			return true
		case a == 169 && b == 254:
			return true
		case a == 172 && b >= 16 && b <= 31:
			return true
		case a == 192 && b == 168:
			return true
		case a == 192 && b == 0 && octets[2] == 0:
			return true
		case a == 198 && (b == 18 || b == 19):
			return true
		case a >= 224:
			return true
		}
		return false
	}
	switch {
	case normalized == "::", normalized == "::1":
		return true
	case strings.HasPrefix(normalized, "fc"), strings.HasPrefix(normalized, "fd"):
		return true
	case strings.HasPrefix(normalized, "fe8"), strings.HasPrefix(normalized, "fe9"),
		strings.HasPrefix(normalized, "fea"), strings.HasPrefix(normalized, "feb"):
		return true
	case strings.HasPrefix(normalized, "ff"):
		return true
	}
	return false
}

func guardedURL(raw string, base *url.URL, lookup LookupFn) (*url.URL, error) {
	var u *url.URL
	var err error
	if base != nil {
		u, err = base.Parse(raw)
	} else {
		u, err = url.Parse(raw)
	}
	if err != nil || u.Scheme == "" {

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
			nu, err := guardedURL(loc, u, f.lookup)
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

	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	fetched, err := f.Guarded(cctx, p.URL)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %s", err)
	}

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

	text := CapChars(readable, maxC)
	if fetched.BodyTruncated {
		text += "\n\n[TRUNCATED: download hit the byte cap; content is partial.]"
	}
	if text == "" {
		text = "(no content extracted)"
	}
	return text, nil
}

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

func CapChars(text string, max int) string {
	r := []rune(text)
	if len(r) <= max {
		return text
	}
	return string(r[:max]) + fmt.Sprintf(
		"\n\n[TRUNCATED: showing %d of %d chars. Refetch with a larger maxChars or a more specific URL.]",
		max, len(r))
}

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
