package openai

import (
	"context"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
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

type Config struct {
	BaseURL       string
	Model         string
	APIKey        string
	Remote        bool
	Reasoning     string
	ProviderPin   []string
	CacheControl  bool
	Retries       int
	RetryBase     time.Duration
	Jitter        func() float64
	HeaderTimeout time.Duration
	IdleTimeout   time.Duration
	BlobsDir      string
}

const (
	defaultHeaderTimeout = 5 * time.Minute
	defaultIdleTimeout   = 10 * time.Minute
	defaultRetryBase     = 500 * time.Millisecond
	defaultEmptyRetries  = 3
)

func New(baseURL, model string) core.Provider {
	return NewWithTimeouts(baseURL, model, defaultHeaderTimeout, defaultIdleTimeout)
}

func NewWithHeaderTimeout(baseURL, model string, headerTimeout time.Duration) core.Provider {
	return NewWithTimeouts(baseURL, model, headerTimeout, defaultIdleTimeout)
}

func NewWithTimeouts(baseURL, model string, headerTimeout, idleTimeout time.Duration) core.Provider {
	return newProvider(baseURL, model, headerTimeout, idleTimeout, nil, Config{})
}

func NewWithVision(baseURL, model, blobsDir string) core.Provider {
	return NewWithConfig(Config{BaseURL: baseURL, Model: model, BlobsDir: blobsDir})
}

func NewWithConfig(cfg Config) core.Provider {
	headerTimeout := cfg.HeaderTimeout
	if headerTimeout <= 0 {
		headerTimeout = defaultHeaderTimeout
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	var imgs *imageStore
	if cfg.BlobsDir != "" {
		imgs = &imageStore{dir: cfg.BlobsDir}
	}
	return newProvider(cfg.BaseURL, cfg.Model, headerTimeout, idleTimeout, imgs, cfg)
}

func newProvider(baseURL, model string, headerTimeout, idleTimeout time.Duration, imgs *imageStore, cfg Config) core.Provider {
	baseURL = strings.TrimRight(baseURL, "/")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = headerTimeout
	p := &provider{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Transport: transport},
		idle:    idleTimeout,
		images:  imgs,
		apiKey:  cfg.APIKey,
		remote:  cfg.Remote,
		pin:     append([]string(nil), cfg.ProviderPin...),
		cache:   cfg.CacheControl,
		retries: cfg.Retries,
		base:    cfg.RetryBase,
		jitter:  cfg.Jitter,
	}
	if p.base <= 0 {
		p.base = defaultRetryBase
	}
	if p.jitter == nil {
		p.jitter = rand.Float64
	}
	p.style = styleFor(cfg.Reasoning)
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
