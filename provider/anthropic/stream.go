package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

func (p *provider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("anthropic: empty message list")
	}
	body, err := p.encode(req)
	if err != nil {
		return nil, err
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
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(), bytes.NewReader(body))
			if err != nil {
				emit(core.Fault{Err: fmt.Errorf("anthropic: encode request: %w", err)})
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("Accept", "text/event-stream")
			httpReq.Header.Set("anthropic-version", p.version)
			if p.apiKey != "" {
				httpReq.Header.Set("x-api-key", p.apiKey)
			}
			resp, err := p.client.Do(httpReq)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				emit(core.Fault{Err: fmt.Errorf("anthropic: transport: %w", err)})
				return
			}
			var snippet []byte
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				snippet, _ = io.ReadAll(io.LimitReader(resp.Body, 256))
			}
			if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempts < p.retries {
				attempts++
				resp.Body.Close()
				select {
				case <-ctx.Done():
					return
				case <-time.After(p.backoff(attempts, resp.Header)):
				}
				continue
			}
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				emit(core.Fault{Err: errorBody(resp.StatusCode, snippet)})
				return
			}
			p.pump(ctx, resp, emit)
			return
		}
	}()

	return ch, nil
}

func (p *provider) pump(ctx context.Context, resp *http.Response, emit func(core.Event) bool) {
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
		order  []int
		blocks = map[int]*blockState{}
		usage  core.Usage
		model  string
		stop   string
		done   bool
	)
	fault := func(err error) {
		emit(core.Fault{Err: err})
	}

	for scanner.Scan() {
		if idle != nil {
			idle.Reset(p.idle)
		}
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		payload, ok := sseData(line)
		if !ok {
			continue
		}
		var ev wireEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			fault(fmt.Errorf("anthropic: malformed stream event: %s", payload))
			return
		}
		switch ev.Type {
		case "ping":
		case "error":
			if ev.Error == nil {
				fault(fmt.Errorf("anthropic: error event"))
				return
			}
			fault(fmt.Errorf("anthropic: %s: %s", ev.Error.Type, ev.Error.Message))
			return
		case "message_start":
			if m := ev.Message; m != nil {
				model = m.Model
				usage = core.Usage{
					Prompt:     m.Usage.InputTokens + m.Usage.CacheReadInputTokens + m.Usage.CacheCreationInputTokens,
					CacheRead:  m.Usage.CacheReadInputTokens,
					CacheWrite: m.Usage.CacheCreationInputTokens,
				}
			}
		case "content_block_start":
			b := &blockState{}
			if ev.Block != nil {
				b = &blockState{kind: ev.Block.Type, id: ev.Block.ID, name: ev.Block.Name, data: ev.Block.Data}
			}
			blocks[ev.Index] = b
			order = append(order, ev.Index)
		case "content_block_delta":
			b := blocks[ev.Index]
			if b == nil {
				fault(fmt.Errorf("anthropic: delta for unknown block %d", ev.Index))
				return
			}
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" && !emit(core.TextDelta{Text: ev.Delta.Text}) {
					return
				}
			case "thinking_delta":
				b.thinking.WriteString(ev.Delta.Thinking)
				if ev.Delta.Thinking != "" && !emit(core.ReasoningDelta{Text: ev.Delta.Thinking}) {
					return
				}
			case "signature_delta":
				b.signature = ev.Delta.Signature
				b.signaled = true
				if !emitRecord(emit, b) {
					return
				}
			case "input_json_delta":
				b.args.WriteString(ev.Delta.PartialJSON)
			}
		case "content_block_stop":
			b := blocks[ev.Index]
			if b == nil {
				continue
			}
			switch b.kind {
			case "thinking":
				if !b.signaled && !emitRecord(emit, b) {
					return
				}
			case "redacted_thinking":
				if !emitRecord(emit, b) {
					return
				}
			case "tool_use":
				args := json.RawMessage(b.args.String())
				if len(args) > 0 && !json.Valid(args) {
					break
				}
				b.emitted = true
				if !emit(core.ToolCallEvent{Call: core.ToolCall{ID: b.id, Name: b.name, Args: inputOf(args)}}) {
					return
				}
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				stop = ev.Delta.StopReason
			}
			if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
				usage.Completion = ev.Usage.OutputTokens
			}
		case "message_stop":
			done = true
		}
	}

	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return
		}
		if idleClosed.Load() {
			fault(fmt.Errorf("anthropic: stream idle for %s: no data from the endpoint; the connection was closed at the idle bound", p.idle))
			return
		}
		fault(fmt.Errorf("anthropic: stream read: %w", err))
		return
	}
	if !done || stop == "" {
		fault(fmt.Errorf("anthropic: stream truncated: no finish marker"))
		return
	}
	if stop == "refusal" {
		fault(fmt.Errorf("anthropic: the model refused: stop_reason refusal"))
		return
	}
	for _, idx := range order {
		b := blocks[idx]
		if b.kind != "tool_use" || b.emitted {
			continue
		}
		args := json.RawMessage(b.args.String())
		call := core.ToolCall{ID: b.id, Name: b.name, Args: args}
		if len(args) > 0 && !json.Valid(args) {
			call.Cut = stopReason(stop)
		}
		if !emit(core.ToolCallEvent{Call: call}) {
			return
		}
	}
	usage.Cost = p.cost(usage)
	emit(core.Done{StopReason: stopReason(stop), Usage: usage, Model: model})
}

type blockState struct {
	kind      string
	id        string
	name      string
	data      string
	signature string
	signaled  bool
	emitted   bool
	thinking  strings.Builder
	args      strings.Builder
}

func emitRecord(emit func(core.Event) bool, b *blockState) bool {
	raw, _ := json.Marshal([]thinkingRecord{{Type: b.kind, Thinking: b.thinking.String(), Signature: b.signature, Data: b.data}})
	return emit(core.ReasoningDelta{Details: raw})
}

func (p *provider) cost(u core.Usage) float64 {
	uncached := u.Prompt - u.CacheRead - u.CacheWrite
	return (p.prices.input*float64(uncached) +
		p.prices.write*float64(u.CacheWrite) +
		p.prices.read*float64(u.CacheRead) +
		p.prices.output*float64(u.Completion)) / 1e6
}

func (p *provider) backoff(attempts int, h http.Header) time.Duration {
	if v := h.Get("Retry-After"); v != "" {
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && n >= 0 {
			return time.Duration(n * float64(time.Second))
		}
	}
	return time.Duration(float64(p.base*(1<<(attempts-1))) * (1 + p.jitter()))
}

func errorBody(status int, snippet []byte) error {
	var env wireErrorEnvelope
	if err := json.Unmarshal(snippet, &env); err == nil && env.Error.Type != "" {
		return fmt.Errorf("anthropic: %d: %s: %s", status, env.Error.Type, env.Error.Message)
	}
	return fmt.Errorf("anthropic: %d: %s", status, strings.TrimSpace(string(snippet)))
}

func stopReason(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	}
	return reason
}

func sseData(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
}
