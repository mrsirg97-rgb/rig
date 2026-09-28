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

	"github.com/mrsirg97-rgb/rig/v2/core"
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
				wait := time.Duration(float64(p.base*(1<<(attempts-1))) * (1 + p.jitter()))
				resp.Body.Close()
				select {
				case <-ctx.Done():
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
