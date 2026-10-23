package anthropic

import (
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/provider/wire"
)

type provider struct {
	baseURL   string
	model     string
	client    *http.Client
	idle      time.Duration
	apiKey    string
	version   string
	maxTokens int
	budget    int
	cache     bool
	retries   int
	base      time.Duration
	jitter    func() float64
	prices    prices
	blobs     *blobStore
}

type prices struct {
	input  float64
	output float64
	read   float64
	write  float64
}

type Option func(*options)

type options struct {
	baseURL         string
	apiKey          string
	version         string
	maxTokens       int
	thinking        int
	cacheControl    bool
	retries         int
	retryBase       time.Duration
	jitter          func() float64
	headerTimeout   time.Duration
	idleTimeout     time.Duration
	blobsDir        string
	inputPrice      float64
	outputPrice     float64
	cacheReadPrice  float64
	cacheWritePrice float64
}

func WithBaseURL(baseURL string) Option       { return func(o *options) { o.baseURL = baseURL } }
func WithAPIKey(key string) Option            { return func(o *options) { o.apiKey = key } }
func WithVersion(version string) Option       { return func(o *options) { o.version = version } }
func WithMaxTokens(tokens int) Option         { return func(o *options) { o.maxTokens = tokens } }
func WithThinking(budget int) Option          { return func(o *options) { o.thinking = budget } }
func WithCacheControl(cache bool) Option      { return func(o *options) { o.cacheControl = cache } }
func WithRetries(retries int) Option          { return func(o *options) { o.retries = retries } }
func WithRetryBase(base time.Duration) Option { return func(o *options) { o.retryBase = base } }
func WithJitter(jitter func() float64) Option { return func(o *options) { o.jitter = jitter } }
func WithHeaderTimeout(timeout time.Duration) Option {
	return func(o *options) { o.headerTimeout = timeout }
}
func WithIdleTimeout(timeout time.Duration) Option {
	return func(o *options) { o.idleTimeout = timeout }
}
func WithBlobsDir(dir string) Option { return func(o *options) { o.blobsDir = dir } }
func WithPrices(input, output, read, write float64) Option {
	return func(o *options) {
		o.inputPrice, o.outputPrice, o.cacheReadPrice, o.cacheWritePrice = input, output, read, write
	}
}

const (
	defaultBaseURL       = "https://api.anthropic.com"
	defaultVersion       = "2023-06-01"
	defaultHeaderTimeout = wire.DefaultHeaderTimeout
	defaultIdleTimeout   = 10 * time.Minute
	defaultRetryBase     = 500 * time.Millisecond
)

func New(model string, opts ...Option) core.Provider {
	o := &options{}
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
	baseURL := o.baseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	version := o.version
	if version == "" {
		version = defaultVersion
	}
	base := o.retryBase
	if base <= 0 {
		base = defaultRetryBase
	}
	jitter := o.jitter
	if jitter == nil {
		jitter = rand.Float64
	}
	var blobs *blobStore
	if o.blobsDir != "" {
		blobs = &blobStore{dir: o.blobsDir}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = headerTimeout
	return &provider{
		baseURL:   strings.TrimRight(baseURL, "/"),
		model:     model,
		client:    &http.Client{Transport: transport},
		idle:      idleTimeout,
		apiKey:    o.apiKey,
		version:   version,
		maxTokens: o.maxTokens,
		budget:    o.thinking,
		cache:     o.cacheControl,
		retries:   o.retries,
		base:      base,
		jitter:    jitter,
		prices: prices{
			input:  o.inputPrice,
			output: o.outputPrice,
			read:   o.cacheReadPrice,
			write:  o.cacheWritePrice,
		},
		blobs: blobs,
	}
}

func (p *provider) endpoint() string {
	return p.baseURL + "/v1/messages"
}
