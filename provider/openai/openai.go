// Package openai is the OpenAI-compatible streaming adapter over net/http:
// the wire format is plain JSON and SSE, stdlib only. Per-model tool-call
// formats are the adapter's problem; the loop sees core.Event only.
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

	"github.com/mrsirg97-rgb/rig/core"
)

type provider struct {
	baseURL string
	model   string
	client  *http.Client
}

// New registers a provider over an OpenAI-compatible endpoint. baseURL may
// carry a path prefix such as /v1; it joins /chat/completions.
func New(baseURL, model string) core.Provider {
	return &provider{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{},
	}
}

func (p *provider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("openai: empty message list")
	}

	body, err := json.Marshal(wireRequest{
		Model:         p.model,
		Messages:      wireMessages(req.Messages),
		Tools:         wireTools(req.Tools),
		Stream:        true,
		StreamOptions: &wireStreamOptions{IncludeUsage: true},
	})
	if err != nil {
		return nil, fmt.Errorf("openai: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: encode request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	ch := make(chan core.Event, 4)
	emit := func(ev core.Event) bool { // false once the context is gone
		select {
		case ch <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	go func() {
		defer close(ch)
		resp, err := p.client.Do(httpReq)
		if err != nil {
			if ctx.Err() != nil {
				return // torn down: closed without Done or Fault
			}
			emit(core.Fault{Err: fmt.Errorf("openai: transport: %w", err)})
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			snippetBytes, _ := io.ReadAll(resp.Body)
			if len(snippetBytes) > 256 {
				snippetBytes = snippetBytes[:256]
			}
			emit(core.Fault{Err: fmt.Errorf("openai: %d: %s", resp.StatusCode, strings.TrimSpace(string(snippetBytes)))})
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // bounded, generous

		var (
			pending   map[int]*core.ToolCall
			finishing string
			usage     core.Usage
		)
		fault := func(err error) { emit(core.Fault{Err: err}) }

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
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
			if chunk.Usage != nil {
				// cached tokens are a subset of prompt on this wire;
				// total_tokens is read and ignored
				usage = core.Usage{
					Prompt:     chunk.Usage.PromptTokens,
					Completion: chunk.Usage.CompletionTokens,
					CacheRead:  chunk.Usage.PromptTokensDetails.CachedTokens,
					CacheWrite: chunk.Usage.PromptTokensDetails.CacheWriteTokens,
				}
			}
			for _, choice := range chunk.Choices {
				// thinking precedes speech, within a unit and across the stream
				if choice.Delta.ReasoningContent != "" && !emit(core.ReasoningDelta{Text: choice.Delta.ReasoningContent}) {
					return
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
				return // torn down by the cancellation
			}
			fault(fmt.Errorf("openai: stream read: %w", err))
			return
		}
		if finishing == "" {
			// incomplete calls are discarded, never executed.
			fault(fmt.Errorf("openai: stream truncated: no finish marker"))
			return
		}
		for _, idx := range sortedPending(pending) {
			if !emit(core.ToolCallEvent{Call: *pending[idx]}) {
				return
			}
		}
		emit(core.Done{StopReason: finishing, Usage: usage})
	}()

	return ch, nil
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

// wire shapes. Named types so a field typo fails at compile time.

type wireRequest struct {
	Model         string             `json:"model"`
	Messages      []wireMessage      `json:"messages"`
	Tools         []wireTool         `json:"tools,omitempty"`
	Stream        bool               `json:"stream"`
	StreamOptions *wireStreamOptions `json:"stream_options,omitempty"`
}

// wireStreamOptions asks the server to include the usage chunk on the
// stream. OpenAI and llama.cpp both emit usage only when this is set;
// without it Done.Usage is all zeros and the cache-hit line reads zero.
type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func wireMessages(msgs []core.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	for _, m := range msgs {
		wm := wireMessage{Role: string(m.Role), Content: m.Content, ReasoningContent: m.Reasoning, ToolID: m.ToolID}
		for _, c := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireCall{
				ID:       c.ID,
				Type:     "function",
				Function: wireFunc{Name: c.Name, Arguments: string(c.Args)},
			})
		}
		out = append(out, wm)
	}
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

// parameters must go over the wire as a JSON object (json.RawMessage), not
// a quoted string: OpenAI-compat servers and llama.cpp templates take the
// object shape.
type wireToolFn struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []wireCall `json:"tool_calls,omitempty"`
	ToolID           string     `json:"tool_call_id,omitempty"`
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
}

type wirePromptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}
