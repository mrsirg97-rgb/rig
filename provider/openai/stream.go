package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/provider/wire"
)

type wireStyle struct {
	reasoning string
}

func styleFor(name string) wireStyle {
	if name == "reasoning" {
		return wireStyle{reasoning: "reasoning"}
	}
	return wireStyle{reasoning: "reasoning_content"}
}

func (p *provider) Stream(ctx context.Context, req core.Request) (<-chan evt.Signal, error) {
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

	ch := make(chan evt.Signal, 4)
	emit := func(ev evt.Signal) {
		select {
		case ch <- ev:
		case <-ctx.Done():
		}
	}
	send := func(ev evt.Signal) { ch <- ev }

	go func() {
		defer close(ch)
		attempts := 0
		var (
			usage core.Usage
			model string
		)
		for {
			usage, model = core.Usage{}, ""
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint("/chat/completions"), bytes.NewReader(body))
			if err != nil {
				emit(core.Fault{Err: fmt.Errorf("openai: encode request: %w", err)})
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("Accept", "text/event-stream")
			if encoded, ok := core.MintFrom(ctx); ok {
				httpReq.Header.Set(core.MintHeader, encoded)
			}
			if p.apiKey != "" {
				httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
			}
			resp, err := p.client.Do(httpReq)
			if err != nil {
				if ctx.Err() != nil {
					send(core.Done{Usage: usage, Model: model})
					return
				}
				emit(core.Fault{Err: fmt.Errorf("openai: transport: %w", err)})
				return
			}
			var snippet []byte
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				snippet, _ = io.ReadAll(io.LimitReader(resp.Body, 256))
			}
			bound := p.retries
			if resp.StatusCode >= 500 && len(snippet) == 0 && bound == 0 {
				bound = defaultEmptyRetries
			}
			if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempts < bound {
				attempts++
				wait := wire.Backoff(p.base, p.jitter, attempts, resp.Header)
				resp.Body.Close()
				select {
				case <-ctx.Done():
					send(core.Done{Usage: usage, Model: model})
					return
				case <-time.After(wait):
				}
				continue
			}
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				emit(core.Fault{Err: fmt.Errorf("openai: %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))})
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
			)
			fault := func(err error) { send(core.Fault{Err: err}) }

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
				payload, ok := wire.SSEData(line)
				if !ok {
					continue
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
				if cu := chunk.Usage; cu != nil {
					if cu.PromptTokens != 0 {
						usage.Prompt = cu.PromptTokens
					}
					if cu.CompletionTokens != 0 {
						usage.Completion = cu.CompletionTokens
					}
					if cu.PromptTokensDetails.CachedTokens != 0 {
						usage.CacheRead = cu.PromptTokensDetails.CachedTokens
					}
					if cu.PromptTokensDetails.CacheWriteTokens != 0 {
						usage.CacheWrite = cu.PromptTokensDetails.CacheWriteTokens
					}
					if cu.Cost != 0 {
						usage.Cost = cu.Cost
					}
				}
				if chunk.Timings != nil {
					usage.PromptPerSecond = chunk.Timings.PromptPerSecond
					usage.DecodePerSecond = chunk.Timings.PredictedPerSecond
				}
				for _, choice := range chunk.Choices {
					text, d := p.reasoningDelta(choice.Delta)
					if len(d) > 0 {
						emit(core.ReasoningDelta{Text: text, Details: d})
					} else if text != "" {
						emit(core.ReasoningDelta{Text: text})
					}
					if choice.Delta.Content != "" {
						emit(core.TextDelta{Text: choice.Delta.Content})
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
					send(core.Done{Usage: usage, Model: model})
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
				if ctx.Err() != nil {
					send(core.Done{Usage: usage, Model: model})
					return
				}
				fault(fmt.Errorf("openai: stream truncated: no finish marker"))
				return
			}
			for _, idx := range sortedPending(pending) {
				call := pending[idx]
				if len(call.Args) == 0 {
					if finishing == "length" {
						call.Cut = finishing
					} else {
						call.Args = json.RawMessage("{}")
					}
				} else if !json.Valid(call.Args) {
					call.Cut = finishing
				}
				emit(core.ToolCallEvent{Call: *call})
			}
			send(core.Done{StopReason: finishing, Usage: usage, Model: model})
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
