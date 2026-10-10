package anthropic

import (
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
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

type Thinking struct {
	Budget int
}

type Config struct {
	BaseURL         string
	Model           string
	APIKey          string
	Version         string
	MaxTokens       int
	Thinking        Thinking
	CacheControl    bool
	Retries         int
	RetryBase       time.Duration
	Jitter          func() float64
	HeaderTimeout   time.Duration
	IdleTimeout     time.Duration
	BlobsDir        string
	InputPrice      float64
	OutputPrice     float64
	CacheReadPrice  float64
	CacheWritePrice float64
}

const (
	defaultBaseURL                     = "https://api.anthropic.com"
	defaultVersion                     = "2023-06-01"
	defaultHeaderTimeout               = 5 * time.Minute
	defaultIdleTimeout                 = 10 * time.Minute
	defaultRetryBase                   = 500 * time.Millisecond
	HeaderTimeoutOff     time.Duration = -1
)

func New(cfg Config) core.Provider {
	headerTimeout := cfg.HeaderTimeout
	if headerTimeout == 0 {
		headerTimeout = defaultHeaderTimeout
	}
	if headerTimeout < 0 {
		headerTimeout = 0
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	version := cfg.Version
	if version == "" {
		version = defaultVersion
	}
	base := cfg.RetryBase
	if base <= 0 {
		base = defaultRetryBase
	}
	jitter := cfg.Jitter
	if jitter == nil {
		jitter = rand.Float64
	}
	var blobs *blobStore
	if cfg.BlobsDir != "" {
		blobs = &blobStore{dir: cfg.BlobsDir}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = headerTimeout
	return &provider{
		baseURL:   strings.TrimRight(baseURL, "/"),
		model:     cfg.Model,
		client:    &http.Client{Transport: transport},
		idle:      idleTimeout,
		apiKey:    cfg.APIKey,
		version:   version,
		maxTokens: cfg.MaxTokens,
		budget:    cfg.Thinking.Budget,
		cache:     cfg.CacheControl,
		retries:   cfg.Retries,
		base:      base,
		jitter:    jitter,
		prices: prices{
			input:  cfg.InputPrice,
			output: cfg.OutputPrice,
			read:   cfg.CacheReadPrice,
			write:  cfg.CacheWritePrice,
		},
		blobs: blobs,
	}
}

func (p *provider) endpoint() string {
	return p.baseURL + "/v1/messages"
}
