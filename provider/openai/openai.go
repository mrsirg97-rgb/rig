package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/imagemarker"
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

// NewWithVision is the vision-capable provider: the marker a view tool
// result carries becomes an image part read from blobsDir, and an empty
// dir means the model has no store to read, which is the text path.
func NewWithVision(baseURL, model, blobsDir string) core.Provider {
	return NewWithConfig(Config{BaseURL: baseURL, Model: model, BlobsDir: blobsDir})
}

// NewWithConfig is the hosted-mode constructor: an API key, retry with
// backoff on 429 and 5xx, the row's reasoning field names, and the
// OpenRouter extras. Zero values take the local-row defaults (no retry,
// reasoning_content).
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

type wireStyle struct {
	reasoning string
}

func styleFor(name string) wireStyle {
	if name == "reasoning" {
		return wireStyle{reasoning: "reasoning"}
	}
	return wireStyle{reasoning: "reasoning_content"}
}

func (p *provider) endpoint(suffix string) string {
	u := p.baseURL + suffix
	if p.sock == "" {
		return u
	}
	return "http://localhost" + strings.TrimPrefix(strings.TrimPrefix(u, "unix:"), p.sock)
}

func (p *provider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("openai: empty message list")
	}

	var kwargs *wireChatTemplateKwargs
	if req.ReasoningEffort != "" && !p.remote {
		kwargs = &wireChatTemplateKwargs{ReasoningEffort: req.ReasoningEffort}
	}
	var providerPin *wireProvider
	if len(p.pin) > 0 {
		providerPin = &wireProvider{Order: p.pin}
	}
	var cacheControl *wireCacheControl
	if p.cache {
		cacheControl = &wireCacheControl{Type: "ephemeral"}
	}
	body, err := json.Marshal(wireRequest{
		Model:              p.model,
		Messages:           wireMessagesStyled(req.Messages, p.images, p.style),
		Tools:              wireTools(req.Tools),
		MaxTokens:          req.MaxTokens,
		ReasoningEffort:    req.ReasoningEffort,
		ChatTemplateKwargs: kwargs,
		Provider:           providerPin,
		CacheControl:       cacheControl,
		Stream:             true,
		StreamOptions:      &wireStreamOptions{IncludeUsage: true},
	})
	if err != nil {
		return nil, fmt.Errorf("openai: encode request: %w", err)
	}

	ch := make(chan core.Event, 4)
	emit := func(ev core.Event) bool {
		select {
		case ch <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	go func() {
		defer close(ch)
		attempts := 0
		for {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint("/chat/completions"), bytes.NewReader(body))
			if err != nil {
				emit(core.Fault{Err: fmt.Errorf("openai: encode request: %w", err)})
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("Accept", "text/event-stream")
			if p.apiKey != "" {
				httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
			}
			resp, err := p.client.Do(httpReq)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				emit(core.Fault{Err: fmt.Errorf("openai: transport: %w", err)})
				return
			}
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
				if attempts < p.retries {
					attempts++
					wait := time.Duration(float64(p.base*(1<<(attempts-1))) * (1 + p.jitter()))
					resp.Body.Close()
					select {
					case <-ctx.Done():
						return
					case <-time.After(wait):
					}
					continue
				}
			}
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				// the error body is untrusted: read at most the snippet the
				// fault will carry, so a hostile endpoint cannot pin memory
				// through a large error response.
				snippetBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
				emit(core.Fault{Err: fmt.Errorf("openai: %d: %s", resp.StatusCode, strings.TrimSpace(string(snippetBytes)))})
				return
			}

			scanner := bufio.NewScanner(resp.Body)
			scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

			var idleClosed atomic.Bool
			var idle *time.Timer
			if p.idle > 0 {
				idle = time.AfterFunc(p.idle, func() {
					idleClosed.Store(true)
					resp.Body.Close()
				})
				defer idle.Stop()
			}

			var (
				pending   map[int]*core.ToolCall
				finishing string
				usage     core.Usage
				model     string
			)
			fault := func(err error) { emit(core.Fault{Err: err}) }

			for scanner.Scan() {
				if idle != nil {
					idle.Reset(p.idle)
				}
				line := scanner.Text()
				if line == "" {
					continue
				}
				if strings.HasPrefix(line, ":") {
					continue
				}
				payload, ok := sseData(line)
				if !ok {
					fault(fmt.Errorf("openai: unrecognized stream line: %q", line))
					return
				}
				if payload == "[DONE]" {
					break
				}
				var chunk streamChunk
				if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
					fault(fmt.Errorf("openai: malformed stream chunk: %s", payload))
					return
				}
				if chunk.Model != "" {
					model = chunk.Model
				}
				if chunk.Usage != nil {
					usage = core.Usage{
						Prompt:     chunk.Usage.PromptTokens,
						Completion: chunk.Usage.CompletionTokens,
						CacheRead:  chunk.Usage.PromptTokensDetails.CachedTokens,
						CacheWrite: chunk.Usage.PromptTokensDetails.CacheWriteTokens,
						Cost:       chunk.Usage.Cost,
					}
				}
				for _, choice := range chunk.Choices {
					if text, d := p.reasoningDelta(choice.Delta); text != "" || len(d) > 0 {
						if !emit(core.ReasoningDelta{Text: text, Details: d}) {
							return
						}
					}
					if choice.Delta.Content != "" && !emit(core.TextDelta{Text: choice.Delta.Content}) {
						return
					}
					for _, dc := range choice.Delta.ToolCalls {
						accumulate(&pending, dc)
					}
					if choice.FinishReason != nil && *choice.FinishReason != "" {
						finishing = *choice.FinishReason
					}
				}
			}

			if err := scanner.Err(); err != nil {
				if ctx.Err() != nil {
					return
				}
				if idleClosed.Load() {
					fault(fmt.Errorf("openai: stream idle for %s: no data from the endpoint; the connection was closed at the idle bound", p.idle))
					return
				}
				fault(fmt.Errorf("openai: stream read: %w", err))
				return
			}
			if finishing == "" {
				fault(fmt.Errorf("openai: stream truncated: no finish marker"))
				return
			}
			for _, idx := range sortedPending(pending) {
				call := pending[idx]
				if !json.Valid(call.Args) && (len(call.Args) > 0 || finishing == "length") {
					call.Cut = finishing
				}
				if !emit(core.ToolCallEvent{Call: *call}) {
					return
				}
			}
			emit(core.Done{StopReason: finishing, Usage: usage, Model: model})
			return
		}
	}()

	return ch, nil
}

func (p *provider) reasoningDelta(d wireDelta) (string, json.RawMessage) {
	if p.style.reasoning == "reasoning" {
		return d.Reasoning, d.ReasoningDetails
	}
	return d.ReasoningContent, nil
}

func sseData(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
}

func accumulate(pending *map[int]*core.ToolCall, dc wireDeltaCall) {
	if *pending == nil {
		*pending = map[int]*core.ToolCall{}
	}
	pc := (*pending)[dc.Index]
	if pc == nil {
		pc = &core.ToolCall{ID: dc.ID, Name: dc.Function.Name}
		(*pending)[dc.Index] = pc
	}
	if pc.ID == "" {
		pc.ID = dc.ID
	}
	if pc.Name == "" {
		pc.Name = dc.Function.Name
	}
	pc.Args = append(pc.Args, dc.Function.Arguments...)
}

func sortedPending(pending map[int]*core.ToolCall) []int {
	idxs := make([]int, 0, len(pending))
	for idx := range pending {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	return idxs
}

type wireProvider struct {
	Order []string `json:"order,omitempty"`
}

type wireCacheControl struct {
	Type string `json:"type"`
}

type wireRequest struct {
	Model              string                  `json:"model"`
	Messages           []wireMessage           `json:"messages"`
	Tools              []wireTool              `json:"tools,omitempty"`
	MaxTokens          int                     `json:"max_tokens,omitempty"`
	ReasoningEffort    string                  `json:"reasoning_effort,omitempty"`
	ChatTemplateKwargs *wireChatTemplateKwargs `json:"chat_template_kwargs,omitempty"`
	Provider           *wireProvider           `json:"provider,omitempty"`
	CacheControl       *wireCacheControl       `json:"cache_control,omitempty"`
	Stream             bool                    `json:"stream"`
	StreamOptions      *wireStreamOptions      `json:"stream_options,omitempty"`
}

type wireChatTemplateKwargs struct {
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func wireMessages(msgs []core.Message) []wireMessage {
	return wireMessagesStyled(msgs, nil, wireStyle{reasoning: "reasoning_content"})
}

func wireMessagesWith(msgs []core.Message, imgs *imageStore) []wireMessage {
	return wireMessagesStyled(msgs, imgs, wireStyle{reasoning: "reasoning_content"})
}

func wireMessagesStyled(msgs []core.Message, imgs *imageStore, style wireStyle) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	var pending []orderedMessage
	// owner is the assistant message that owns the current tool batch: the
	// batch runs from that assistant message until the next message that is
	// not a tool result, so a reused call id on a later turn is looked up
	// in the turn that issued it, never in the transcript as a whole.
	var owner callTable
	flush := func() {
		if len(pending) == 0 {
			return
		}
		sortOrdered(pending)
		for _, p := range pending {
			out = append(out, p.msg)
		}
		pending = nil
	}
	for _, m := range msgs {
		if m.Role == core.RoleAssistant {
			flush()
			out = append(out, encodeMessage(m, style))
			owner = tableOf(m)
			continue
		}
		if m.Role != core.RoleTool {
			flush()
			out = append(out, encodeMessage(m, style))
			owner = callTable{}
			continue
		}
		out = append(out, encodeMessage(m, style))
		if imgs == nil || owner.nameOf(m.ToolID) != viewToolName {
			continue
		}
		// The view contract is one line and nothing else: only a result
		// that is exactly the marker it wrote is honored, so a path that
		// smuggled a marker line stays text.
		ref, ok := imagemarker.Parse(m.Content)
		if !ok {
			continue
		}
		pending = append(pending, orderedMessage{at: owner.indexOf(m.ToolID), msg: imageMessage(imgs, ref)})
	}
	flush()
	return out
}

func wireTools(specs []core.ToolSpec) []wireTool {
	out := make([]wireTool, 0, len(specs))
	for _, s := range specs {
		out = append(out, wireTool{
			Type:     "function",
			Function: wireToolFn{Name: s.Name, Description: s.Description, Parameters: s.Schema},
		})
	}
	return out
}

type wireToolFn struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireMessage struct {
	Role             string          `json:"role"`
	Content          wireContent     `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
	ToolCalls        []wireCall      `json:"tool_calls,omitempty"`
	ToolID           string          `json:"tool_call_id,omitempty"`
}

type wireCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function wireFunc `json:"function"`
}

type wireFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string     `json:"type"`
	Function wireToolFn `json:"function"`
}

type streamChunk struct {
	Model   string       `json:"model"`
	Choices []wireChoice `json:"choices"`
	Usage   *wireUsage   `json:"usage"`
}

type wireChoice struct {
	Delta        wireDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

type wireDelta struct {
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	Reasoning        string          `json:"reasoning"`
	ReasoningDetails json.RawMessage `json:"reasoning_details"`
	ToolCalls        []wireDeltaCall `json:"tool_calls"`
}

type wireDeltaCall struct {
	Index    int      `json:"index"`
	ID       string   `json:"id"`
	Function wireFunc `json:"function"`
}

type wireUsage struct {
	PromptTokens        int                     `json:"prompt_tokens"`
	CompletionTokens    int                     `json:"completion_tokens"`
	PromptTokensDetails wirePromptTokensDetails `json:"prompt_tokens_details"`
	Cost                float64                 `json:"cost"`
}

type wirePromptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}
