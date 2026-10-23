package openai

import (
	"context"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/provider/wire"
)

type provider struct {
	baseURL string
	model   string
	client  *http.Client
	sock    string
	idle    time.Duration
	images  *imageStore
	apiKey  string
	remote  bool
	style   wireStyle
	pin     []string
	cache   bool
	retries int
	base    time.Duration
	jitter  func() float64
}

type Option func(*options)

type options struct {
	apiKey        string
	remote        bool
	reasoning     string
	pin           []string
	cache         bool
	retries       int
	retryBase     time.Duration
	jitter        func() float64
	headerTimeout time.Duration
	idleTimeout   time.Duration
	blobsDir      string
}

const (
	defaultHeaderTimeout = wire.DefaultHeaderTimeout
	defaultIdleTimeout   = 10 * time.Minute
	defaultRetryBase     = 500 * time.Millisecond
	defaultEmptyRetries  = 3
)

func WithAPIKey(key string) Option          { return func(o *options) { o.apiKey = key } }
func WithRemote(remote bool) Option         { return func(o *options) { o.remote = remote } }
func WithReasoning(reasoning string) Option { return func(o *options) { o.reasoning = reasoning } }
func WithProviderPin(pin ...string) Option {
	return func(o *options) { o.pin = append([]string(nil), pin...) }
}
func WithCacheControl(cache bool) Option { return func(o *options) { o.cache = cache } }
func WithRetries(retries int) Option     { return func(o *options) { o.retries = retries } }
func WithRetryBase(base time.Duration) Option {
	return func(o *options) { o.retryBase = base }
}
func WithJitter(jitter func() float64) Option { return func(o *options) { o.jitter = jitter } }
func WithHeaderTimeout(timeout time.Duration) Option {
	return func(o *options) { o.headerTimeout = timeout }
}
func WithIdleTimeout(timeout time.Duration) Option {
	return func(o *options) { o.idleTimeout = timeout }
}
func WithBlobsDir(dir string) Option { return func(o *options) { o.blobsDir = dir } }

func New(baseURL, model string, opts ...Option) core.Provider {
	o := &options{headerTimeout: defaultHeaderTimeout, idleTimeout: defaultIdleTimeout}
	for _, opt := range opts {
		opt(o)
	}
	headerTimeout := o.headerTimeout
	if headerTimeout == 0 {
		headerTimeout = defaultHeaderTimeout
	}
	if headerTimeout < 0 {
		headerTimeout = 0
	}
	idleTimeout := o.idleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	var imgs *imageStore
	if o.blobsDir != "" {
		imgs = &imageStore{dir: o.blobsDir}
	}
	baseURL = strings.TrimRight(baseURL, "/")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = headerTimeout
	p := &provider{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Transport: transport},
		idle:    idleTimeout,
		images:  imgs,
		apiKey:  o.apiKey,
		remote:  o.remote,
		pin:     append([]string(nil), o.pin...),
		cache:   o.cache,
		retries: o.retries,
		base:    o.retryBase,
		jitter:  o.jitter,
	}
	if p.base <= 0 {
		p.base = defaultRetryBase
	}
	if p.jitter == nil {
		p.jitter = rand.Float64
	}
	p.style = styleFor(o.reasoning)
	if strings.HasPrefix(baseURL, "unix:") {
		sock := strings.TrimPrefix(baseURL, "unix:")
		p.sock = sock
		d := net.Dialer{}
		p.client = &http.Client{
			Transport: &http.Transport{
				Proxy:                 nil,
				ResponseHeaderTimeout: headerTimeout,
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return d.DialContext(ctx, "unix", sock)
				},
			},
		}
	}
	return p
}

func (p *provider) endpoint(suffix string) string {
	u := p.baseURL + suffix
	if p.sock == "" {
		return u
	}
	return "http://localhost" + strings.TrimPrefix(strings.TrimPrefix(u, "unix:"), p.sock)
}
