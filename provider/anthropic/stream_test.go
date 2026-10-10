package anthropic_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/provider/anthropic"
)

const textTurn = `data: {"type":"message_start","message":{"id":"msg_1","model":"claude-fake","usage":{"input_tokens":10,"output_tokens":1}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello "}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":7}}

data: {"type":"message_stop"}

`

func newTextProvider(e *endpoint) core.Provider {
	return anthropic.New(anthropic.Config{BaseURL: e.url, Model: "claude-fake", MaxTokens: 1024})
}

func TestStreamsTextDeltasAndDone(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(textTurn)
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); got != "delta,delta,done" {
		t.Fatalf("events = %s, want the deltas then done", got)
	}
	d := lastDone(t, events)
	if d.StopReason != "stop" || d.Model != "claude-fake" {
		t.Fatalf("done = %+v, want the mapped stop and the echoed model", d)
	}
	if d.Usage.Prompt != 10 || d.Usage.Completion != 7 {
		t.Fatalf("usage = %+v, want the message_start prompt and the message_delta output", d.Usage)
	}
}

func TestPingsAndCommentsPassThrough(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"msg_1","model":"m","usage":{"input_tokens":1}}}

: keep-alive

data: {"type":"ping"}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}

data: {"type":"message_stop"}

`)
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); got != "done" {
		t.Fatalf("events = %s, want a clean done", got)
	}
}

func TestThinkingStreamsLiveAndCarriesTheRecord(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"msg_1","model":"m","usage":{"input_tokens":1}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"need to"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" look"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sigZ"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hi"}}

data: {"type":"content_block_stop","index":1}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

data: {"type":"message_stop"}

`)
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); got != "reason,reason,reason,delta,done" {
		t.Fatalf("events = %s, want the live deltas, the record, the text, done", got)
	}
	var record json.RawMessage
	for _, ev := range events {
		if r, ok := ev.(core.ReasoningDelta); ok && len(r.Details) > 0 {
			record = r.Details
		}
	}
	want := `[{"type":"thinking","thinking":"need to look","signature":"sigZ"}]`
	if string(record) != want {
		t.Fatalf("record = %s, want the block verbatim", record)
	}
}

func TestAThinkingBlockWithoutASignatureStillCarriesBack(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"msg_1","model":"m","usage":{"input_tokens":1}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"plain"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}

data: {"type":"message_stop"}

`)
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for _, ev := range events {
		if r, ok := ev.(core.ReasoningDelta); ok && len(r.Details) > 0 {
			if string(r.Details) != `[{"type":"thinking","thinking":"plain"}]` {
				t.Fatalf("record = %s, want the unsigned block verbatim", r.Details)
			}
			return
		}
	}
	t.Fatal("no reasoning record emitted")
}

func TestRedactedThinkingCarriesBackThroughTheStream(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"msg_1","model":"m","usage":{"input_tokens":1}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"raw-bytes"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}

data: {"type":"message_stop"}

`)
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for _, ev := range events {
		if r, ok := ev.(core.ReasoningDelta); ok && len(r.Details) > 0 {
			if string(r.Details) != `[{"type":"redacted_thinking","data":"raw-bytes"}]` {
				t.Fatalf("record = %s", r.Details)
			}
			return
		}
	}
	t.Fatal("no reasoning record emitted")
}

func TestStopReasonsMap(t *testing.T) {
	cases := map[string]string{
		"end_turn":      "stop",
		"stop_sequence": "stop",
		"tool_use":      "tool_calls",
		"max_tokens":    "length",
		"pause_turn":    "pause_turn",
	}
	for reason, want := range cases {
		e := captureEndpoint(t)
		e.queue(`data: {"type":"message_start","message":{"id":"m","model":"m","usage":{"input_tokens":1}}}

data: {"type":"message_delta","delta":{"stop_reason":"` + reason + `"},"usage":{"output_tokens":1}}

data: {"type":"message_stop"}

`)
		events, err := drain(t, context.Background(), newTextProvider(e), userReq())
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if got := lastDone(t, events).StopReason; got != want {
			t.Fatalf("stop_reason %s mapped to %q, want %q", reason, got, want)
		}
	}
}

func TestMaxTokensCutMarksThePartialCall(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"m","model":"m","usage":{"input_tokens":1}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"bash"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"comm"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":9}}

data: {"type":"message_stop"}

`)
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); got != "call,done" {
		t.Fatalf("events = %s, want the cut call then done", got)
	}
	for _, ev := range events {
		if c, ok := ev.(core.ToolCallEvent); ok {
			if c.Call.Cut != "length" {
				t.Fatalf("cut = %q, want the length mark", c.Call.Cut)
			}
			if string(c.Call.Args) != `{"comm` {
				t.Fatalf("args = %s, want the raw partial json", c.Call.Args)
			}
			return
		}
	}
	t.Fatal("no call emitted")
}

func TestACutCallRidesTheNextRequestWithEmptyInput(t *testing.T) {
	e := captureEndpoint(t)
	p := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "claude-fake", MaxTokens: 1024})
	req := core.Request{
		Messages: []core.Message{
			{Role: core.RoleUser, Content: "go"},
			{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "toolu_1", Name: "bash", Args: json.RawMessage(`{"comm`), Cut: "length"}}},
			{Role: core.RoleTool, ToolID: "toolu_1", Content: "bash: the tool call was cut off by the output token limit while its arguments were still being generated; re-issue the call more tersely, or split it into several calls"},
		},
		MaxTokens: 1024,
	}
	if _, err := drain(t, context.Background(), p, req); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := string(e.lastBody(t)); !strings.Contains(got, `"input":{}`) {
		t.Fatalf("a malformed call's input cannot ride the wire:\n%s", got)
	}
}

func TestRefusalFaultsAndNeverDones(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"m","model":"m","usage":{"input_tokens":1}}}

data: {"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":2}}

data: {"type":"message_stop"}

`)
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); got != "fault" {
		t.Fatalf("events = %s, want a fault and no done", got)
	}
	if f := lastFault(t, events); !strings.Contains(f.Err.Error(), "refusal") {
		t.Fatalf("fault = %v, want the refusal named", f.Err)
	}
}

func TestTruncatedStreamFaults(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"m","model":"m","usage":{"input_tokens":1}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"half"}}

`)
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); got != "delta,fault" {
		t.Fatalf("events = %s, want the delta then the fault", got)
	}
	if f := lastFault(t, events); f.Err.Error() != "anthropic: stream truncated: no finish marker" {
		t.Fatalf("fault = %v, want the openai words under the anthropic name", f.Err)
	}
}

func TestAStreamWithoutAStopReasonIsTruncated(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"m","model":"m","usage":{"input_tokens":1}}}

data: {"type":"message_stop"}

`)
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if f := lastFault(t, events); f.Err.Error() != "anthropic: stream truncated: no finish marker" {
		t.Fatalf("fault = %v", f.Err)
	}
}

func TestAnErrorEventFaultsWithTheApiTypeAndMessage(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"m","model":"m","usage":{"input_tokens":1}}}

data: {"type":"error","error":{"type":"overloaded_error","message":"This model is currently overloaded. Please try again."}}

`)
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	f := lastFault(t, events)
	want := "anthropic: overloaded_error: This model is currently overloaded. Please try again."
	if f.Err.Error() != want {
		t.Fatalf("fault = %v, want %q", f.Err, want)
	}
}

func TestUsageAndCostArithmeticFromTheRowPrices(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"m","model":"m","usage":{"input_tokens":100,"cache_read_input_tokens":40,"cache_creation_input_tokens":10}}}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}

data: {"type":"message_stop"}

`)
	p := anthropic.New(anthropic.Config{
		BaseURL:         e.url,
		Model:           "claude-fake",
		MaxTokens:       1024,
		InputPrice:      3,
		OutputPrice:     15,
		CacheReadPrice:  0.25,
		CacheWritePrice: 4,
	})
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	u := lastDone(t, events).Usage
	if u.Prompt != 150 || u.CacheRead != 40 || u.CacheWrite != 10 || u.Completion != 20 {
		t.Fatalf("usage = %+v, want the anthropic fields summed into the prompt", u)
	}
	if u.Cost != 650.0/1e6 {
		t.Fatalf("cost = %v, want the per-million row prices over the raw fields", u.Cost)
	}
}

func TestAbsentPricesCostZero(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"m","model":"m","usage":{"input_tokens":100,"cache_read_input_tokens":40,"cache_creation_input_tokens":10}}}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}

data: {"type":"message_stop"}

`)
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if u := lastDone(t, events).Usage; u.Cost != 0 {
		t.Fatalf("cost = %v, want zero when the row names no prices", u.Cost)
	}
}

func TestNon200FaultsWithTheEnvelope(t *testing.T) {
	e := captureEndpoint(t)
	e.fail(401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	p := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "claude-fake", MaxTokens: 1024, APIKey: "test-key-1"})
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	f := lastFault(t, events)
	want := "anthropic: 401: authentication_error: invalid x-api-key"
	if f.Err.Error() != want {
		t.Fatalf("fault = %v, want %q", f.Err, want)
	}
	if strings.Contains(f.Err.Error(), "test-key-1") {
		t.Fatal("the key never rides a fault")
	}
}

func TestNon200WithoutTheEnvelopeKeepsTheSnippet(t *testing.T) {
	e := captureEndpoint(t)
	e.fail(502, "502 behind the proxy, no envelope")
	p := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "claude-fake", MaxTokens: 1024})
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if f := lastFault(t, events); f.Err.Error() != "anthropic: 502: 502 behind the proxy, no envelope" {
		t.Fatalf("fault = %v", f.Err)
	}
}

func TestMalformedDataFaults(t *testing.T) {
	e := captureEndpoint(t)
	e.queue("data: not json\n\ndata: {\"type\":\"message_stop\"}\n\n")
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if f := lastFault(t, events); !strings.Contains(f.Err.Error(), "malformed stream event") {
		t.Fatalf("fault = %v", f.Err)
	}
}

func TestEventNameLinesPassThrough(t *testing.T) {
	e := captureEndpoint(t)
	e.queue("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"m\",\"usage\":{\"input_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	events, err := drain(t, context.Background(), newTextProvider(e), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); got != "done" {
		t.Fatalf("events = %s, want the named events to ride their data lines", got)
	}
}

func TestCancellationTearsDownTheStream(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(textTurn)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		cancel()
	}()
	if _, err := drain(t, ctx, newTextProvider(e), userReq()); err != nil {
		t.Fatalf("stream: %v", err)
	}
}

func TestCacheReadsArriveFromBothUsageShapes(t *testing.T) {
	cases := []struct {
		name   string
		start  string
		delta  string
		read   int
		prompt int
	}{
		// the api reports the cache fields on message_start
		{name: "from message_start", start: `"input_tokens":100,"cache_read_input_tokens":7`, delta: `"output_tokens":9`, read: 7, prompt: 107},
		// compat servers report them only on the final message_delta
		{name: "from message_delta", start: `"input_tokens":100`, delta: `"output_tokens":9,"cache_read_input_tokens":7`, read: 7, prompt: 107},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := captureEndpoint(t)
			e.queue(`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-fake","usage":{` + c.start + `}}}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{` + c.delta + `}}

data: {"type":"message_stop"}

`)
			events, err := drain(t, context.Background(), newTextProvider(e), userReq())
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			u := lastDone(t, events).Usage
			if u.CacheRead != c.read {
				t.Fatalf("cacheRead = %d, want %d", u.CacheRead, c.read)
			}
			if u.Prompt != c.prompt {
				t.Fatalf("prompt = %d, want input plus the cache read", u.Prompt)
			}
		})
	}
}
