package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"time"
)

const (
	maxBytesDefault  = 5 * 1024 * 1024
	defaultMaxChars  = 20_000
	minTimeoutMs     = 1_000
	maxTimeoutMs     = 300_000
	defaultTimeoutMs = 30_000
	maxHops          = 5
	trafilaturaTime  = 20 * time.Second
	trafilaturaCap   = 2 * maxBytesDefault
)

var (
	textualRE = regexp.MustCompile(`(?i)^(text/|application/(json|xml|xhtml\+xml|rss\+xml|atom\+xml|[\w.-]+\+(json|xml))(\s*;|$))`)

	htmlishRE = regexp.MustCompile(`(?i)html|xml`)
)

type LookupFn func(ctx context.Context, host string) ([]string, error)

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

func newFetch(cfg FetchConfig) *fetch {
	f := &fetch{
		proxy:    cfg.Proxy,
		lookup:   cfg.Lookup,
		maxBytes: cfg.MaxBytes,
	}
	if f.maxBytes == 0 {
		f.maxBytes = maxBytesDefault
	}
	if f.lookup == nil {
		f.lookup = func(ctx context.Context, host string) ([]string, error) {
			return net.DefaultResolver.LookupHost(ctx, host)
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
	} else {
		tr.DialContext = pinnedDial
	}

	f.do = (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}).Do
	return f
}

var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
}

func publicAddr(ip string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return netip.Addr{}, false
	}
	a = a.Unmap()
	if a.IsLoopback() || a.IsPrivate() || a.IsUnspecified() || a.IsMulticast() ||
		a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() {
		return netip.Addr{}, false
	}
	for _, p := range reservedPrefixes {
		if p.Contains(a) {
			return netip.Addr{}, false
		}
	}
	return a, true
}

func IPisPrivate(ip string) bool {
	_, ok := publicAddr(ip)
	return !ok
}

type pinKey struct{}

func pinnedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	pins, ok := ctx.Value(pinKey{}).([]netip.Addr)
	_, port, err := net.SplitHostPort(addr)
	if !ok || len(pins) == 0 || err != nil {
		return nil, fmt.Errorf("refused: unpinned dial to %s", addr)
	}
	var d net.Dialer
	var last error
	for _, a := range pins {
		c, err := d.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

func guardedURL(ctx context.Context, raw string, base *url.URL, lookup LookupFn) (*url.URL, []netip.Addr, error) {
	var u *url.URL
	var err error
	if base != nil {
		u, err = base.Parse(raw)
	} else {
		u, err = url.Parse(raw)
	}
	if err != nil || u.Scheme == "" {

		return nil, nil, fmt.Errorf("invalid URL: %s", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, nil, fmt.Errorf("only http(s) is fetchable, got %s", u.Scheme)
	}
	host := u.Hostname()
	addrs, err := lookup(ctx, host)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		return nil, nil, fmt.Errorf("cannot resolve host: %s (%v)", host, err)
	}
	if len(addrs) == 0 {
		return nil, nil, fmt.Errorf("cannot resolve host: %s", host)
	}
	pins := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		p, ok := publicAddr(a)
		if !ok {
			return nil, nil, fmt.Errorf("refused: %s resolves to private address %s", host, a)
		}
		pins = append(pins, p)
	}
	return u, pins, nil
}
