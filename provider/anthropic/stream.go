package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/provider/wire"
)

func (p *provider) Stream(ctx context.Context, req core.Request) (<-chan evt.Signal, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("anthropic: empty message list")
	}
	body, err := p.encode(req)
	if err != nil {
		return nil, err
	}

	ch := make(chan evt.Signal, 4)
	emit := func(ev evt.Signal) {
		ch <- ev
	}

	go func() {
		defer close(ch)
		attempts := 0
		var (
			usage core.Usage
			model string
		)
		for {
			usage, model = core.Usage{}, ""
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(), bytes.NewReader(body))
			if err != nil {
				emit(core.Fault{Err: fmt.Errorf("anthropic: encode request: %w", err)})
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("Accept", "text/event-stream")
			if encoded, ok := core.MintFrom(ctx); ok {
				httpReq.Header.Set(core.MintHeader, encoded)
			}
			httpReq.Header.Set("anthropic-version", p.version)
			if p.apiKey != "" {
				httpReq.Header.Set("x-api-key", p.apiKey)
			}
			resp, err := p.client.Do(httpReq)
			if err != nil {
				if ctx.Err() != nil {
					emit(core.Done{Usage: usage, Model: model})
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
					emit(core.Done{Usage: usage, Model: model})
					return
				case <-time.After(wire.Backoff(p.base, p.jitter, attempts, resp.Header)):
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

func (p *provider) pump(ctx context.Context, resp *http.Response, emit func(evt.Signal)) {
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
		input  int
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
		payload, ok := wire.SSEData(line)
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
				input = m.Usage.InputTokens
				usage = core.Usage{
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
				if ev.Delta.Text != "" {
					emit(core.TextDelta{Text: ev.Delta.Text})
				}
			case "thinking_delta":
				b.thinking.WriteString(ev.Delta.Thinking)
				if ev.Delta.Thinking != "" {
					emit(core.ReasoningDelta{Text: ev.Delta.Thinking})
				}
			case "signature_delta":
				b.signature = ev.Delta.Signature
				b.signaled = true
				emitRecord(emit, b)
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
				if !b.signaled {
					emitRecord(emit, b)
				}
			case "redacted_thinking":
				emitRecord(emit, b)
			case "tool_use":
				args := json.RawMessage(b.args.String())
				if len(args) > 0 && !json.Valid(args) {
					break
				}
				b.emitted = true
				emit(core.ToolCallEvent{Call: core.ToolCall{ID: b.id, Name: b.name, Args: inputOf(args)}})
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				stop = ev.Delta.StopReason
			}
			if ev.Usage != nil {
				if ev.Usage.OutputTokens > 0 {
					usage.Completion = ev.Usage.OutputTokens
				}
				if ev.Usage.CacheReadInputTokens > 0 {
					usage.CacheRead = ev.Usage.CacheReadInputTokens
				}
				if ev.Usage.CacheCreationInputTokens > 0 {
					usage.CacheWrite = ev.Usage.CacheCreationInputTokens
				}
			}
		case "message_stop":
			done = true
		}
	}

	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			emit(core.Done{Usage: p.settle(usage, input), Model: model})
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
		if ctx.Err() != nil {
			emit(core.Done{Usage: p.settle(usage, input), Model: model})
			return
		}
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
		emit(core.ToolCallEvent{Call: call})
	}
	usage = p.settle(usage, input)
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

func emitRecord(emit func(evt.Signal), b *blockState) {
	raw, _ := json.Marshal([]thinkingRecord{{Type: b.kind, Thinking: b.thinking.String(), Signature: b.signature, Data: b.data}})
	emit(core.ReasoningDelta{Details: raw})
}

// settle folds the prompt the start event carried and the row's prices into
// the usage a terminal Done reports: a cancelled or truncated stream still
// bills what the endpoint already read.
func (p *provider) settle(u core.Usage, input int) core.Usage {
	u.Prompt = input + u.CacheRead + u.CacheWrite
	u.Cost = p.cost(u)
	return u
}

func (p *provider) cost(u core.Usage) float64 {
	uncached := u.Prompt - u.CacheRead - u.CacheWrite
	return (p.prices.input*float64(uncached) +
		p.prices.write*float64(u.CacheWrite) +
		p.prices.read*float64(u.CacheRead) +
		p.prices.output*float64(u.Completion)) / 1e6
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
