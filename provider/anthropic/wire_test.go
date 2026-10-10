package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/provider/anthropic"
)

const doneStream = `data: {"type":"message_start","message":{"id":"msg_1","model":"claude-fake","usage":{"input_tokens":1,"output_tokens":1}}}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}

data: {"type":"message_stop"}

`

type endpoint struct {
	mu      sync.Mutex
	bodies  [][]byte
	headers http.Header
	streams []string
	status  int
	reply   string
	url     string
}

func captureEndpoint(t *testing.T) *endpoint {
	t.Helper()
	e := &endpoint{}
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	e.url = srv.URL
	return e
}

func (e *endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	e.mu.Lock()
	e.bodies = append(e.bodies, b)
	e.headers = r.Header.Clone()
	status, reply := e.status, e.reply
	stream := doneStream
	if len(e.streams) > 0 {
		stream = e.streams[0]
		e.streams = e.streams[1:]
	}
	e.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		io.WriteString(w, reply)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	io.WriteString(w, stream)
}

func (e *endpoint) queue(stream string) {
	e.mu.Lock()
	e.streams = append(e.streams, stream)
	e.mu.Unlock()
}

func (e *endpoint) fail(status int, reply string) {
	e.mu.Lock()
	e.status = status
	e.reply = reply
	e.mu.Unlock()
}

func (e *endpoint) all() [][]byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([][]byte{}, e.bodies...)
}

func (e *endpoint) lastBody(t *testing.T) []byte {
	t.Helper()
	bodies := e.all()
	if len(bodies) == 0 {
		t.Fatal("the endpoint saw no request")
	}
	return bodies[len(bodies)-1]
}

func (e *endpoint) lastHeaders(t *testing.T) http.Header {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.headers == nil {
		t.Fatal("the endpoint saw no request")
	}
	return e.headers
}

func drain(t *testing.T, ctx context.Context, p core.Provider, req core.Request) ([]core.Event, error) {
	t.Helper()
	ch, err := p.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	var events []core.Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events, nil
}

func kinds(events []core.Event) string {
	var out []string
	for _, ev := range events {
		switch ev.(type) {
		case core.TextDelta:
			out = append(out, "delta")
		case core.ReasoningDelta:
			out = append(out, "reason")
		case core.ToolCallEvent:
			out = append(out, "call")
		case core.Done:
			out = append(out, "done")
		case core.Fault:
			out = append(out, "fault")
		}
	}
	return strings.Join(out, ",")
}

func lastFault(t *testing.T, events []core.Event) core.Fault {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if ft, ok := events[i].(core.Fault); ok {
			return ft
		}
	}
	t.Fatal("no Fault event found")
	return core.Fault{}
}

func lastDone(t *testing.T, events []core.Event) core.Done {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if d, ok := events[i].(core.Done); ok {
			return d
		}
	}
	t.Fatal("no Done event found")
	return core.Done{}
}

func userReq() core.Request {
	return core.Request{Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}}, MaxTokens: 8192}
}

func pinnedRequest() core.Request {
	return core.Request{
		Messages: []core.Message{
			{Role: core.RoleSystem, Content: "You are rig."},
			{Role: core.RoleUser, Content: "run ls"},
			{Role: core.RoleAssistant,
				ReasoningDetails: json.RawMessage(`[{"type":"thinking","thinking":"need ls","signature":"sigA"}]`),
				ToolCalls:        []core.ToolCall{{ID: "toolu_01", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)}}},
			{Role: core.RoleTool, ToolID: "toolu_01", Content: "total 0"},
			{Role: core.RoleAssistant, Content: "done"},
			{Role: core.RoleUser, Content: "thanks"},
		},
		Tools: []core.ToolSpec{
			{Name: "bash", Description: "run a command", Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)},
			{Name: "read", Description: "read a file", Schema: json.RawMessage(`{"type":"object"}`)},
		},
		MaxTokens: 1024,
	}
}

const pinnedBody = `{"model":"claude-fake","max_tokens":1024,"system":[{"type":"text","text":"You are rig.","cache_control":{"type":"ephemeral"}}],"tools":[{"name":"bash","description":"run a command","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}},{"name":"read","description":"read a file","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}],"thinking":{"type":"enabled","budget_tokens":512},"messages":[{"role":"user","content":[{"type":"text","text":"run ls"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"need ls","signature":"sigA"},{"type":"tool_use","id":"toolu_01","name":"bash","input":{"command":"ls"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":[{"type":"text","text":"total 0"}],"cache_control":{"type":"ephemeral"}}]},{"role":"assistant","content":[{"type":"text","text":"done"}]},{"role":"user","content":[{"type":"text","text":"thanks"}]}],"stream":true}`

func TestBodyPinnedByteForByte(t *testing.T) {
	e := captureEndpoint(t)
	p := anthropic.New(anthropic.Config{
		BaseURL:      e.url,
		Model:        "claude-fake",
		APIKey:       "test-key-1",
		MaxTokens:    1024,
		Thinking:     anthropic.Thinking{Budget: 512},
		CacheControl: true,
	})
	if _, err := drain(t, context.Background(), p, pinnedRequest()); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := string(e.lastBody(t)); got != pinnedBody {
		t.Fatalf("body mismatch:\n%s", got)
	}
	h := e.lastHeaders(t)
	if got := h.Get("x-api-key"); got != "test-key-1" {
		t.Fatalf("x-api-key = %q", got)
	}
	if got := h.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("anthropic-version = %q", got)
	}
	if got := h.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q", got)
	}
	if strings.Contains(pinnedBody, "test-key-1") {
		t.Fatal("the key never rides the body")
	}
}

func TestCacheControlOffOmitsEveryBreakpoint(t *testing.T) {
	e := captureEndpoint(t)
	p := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "claude-fake"})
	if _, err := drain(t, context.Background(), p, pinnedRequest()); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := string(e.lastBody(t)); strings.Contains(got, "cache_control") {
		t.Fatalf("no breakpoint without the switch: %s", got)
	}
}

func TestThinkingDisabledSendsNoThinkingBlocks(t *testing.T) {
	e := captureEndpoint(t)
	p := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "claude-fake", MaxTokens: 1024})
	req := pinnedRequest()
	if _, err := drain(t, context.Background(), p, req); err != nil {
		t.Fatalf("stream: %v", err)
	}
	body := string(e.lastBody(t))
	if strings.Contains(body, `"type":"thinking"`) {
		t.Fatalf("thinking blocks ride only when thinking is enabled: %s", body)
	}
	if strings.Contains(body, `"thinking"`) {
		t.Fatalf("the thinking field is the enabled switch only: %s", body)
	}
}

func TestRedactedThinkingCarriesBackVerbatim(t *testing.T) {
	e := captureEndpoint(t)
	p := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "claude-fake", MaxTokens: 1024, Thinking: anthropic.Thinking{Budget: 512}})
	req := core.Request{
		Messages: []core.Message{
			{Role: core.RoleUser, Content: "go"},
			{Role: core.RoleAssistant, ReasoningDetails: json.RawMessage(`[{"type":"redacted_thinking","data":"raw-bytes"}]`), Content: "ok"},
		},
		MaxTokens: 1024,
	}
	if _, err := drain(t, context.Background(), p, req); err != nil {
		t.Fatalf("stream: %v", err)
	}
	want := `"content":[{"type":"redacted_thinking","data":"raw-bytes"},{"type":"text","text":"ok"}]`
	if got := string(e.lastBody(t)); !strings.Contains(got, want) {
		t.Fatalf("the redacted block must ride verbatim:\n%s", got)
	}
}

func TestToolCallRoundTripsByID(t *testing.T) {
	e := captureEndpoint(t)
	e.queue(`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-fake","usage":{"input_tokens":1}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_9","name":"bash"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"comm"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"and\":\"ls -la\"}"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}

data: {"type":"message_stop"}

`)
	p := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "claude-fake", MaxTokens: 1024})
	events, err := drain(t, context.Background(), p, core.Request{Messages: []core.Message{{Role: core.RoleUser, Content: "ls"}}, MaxTokens: 1024})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); got != "call,done" {
		t.Fatalf("events = %s, want one call then done", got)
	}
	var call core.ToolCallEvent
	for _, ev := range events {
		if c, ok := ev.(core.ToolCallEvent); ok {
			call = c
		}
	}
	if call.Call.ID != "toolu_9" || call.Call.Name != "bash" {
		t.Fatalf("call = %+v, want the streamed id and name", call.Call)
	}
	if string(call.Call.Args) != `{"command":"ls -la"}` {
		t.Fatalf("args = %s, want the accumulated json", call.Call.Args)
	}

	req := core.Request{
		Messages: []core.Message{
			{Role: core.RoleUser, Content: "ls"},
			{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{call.Call}},
			{Role: core.RoleTool, ToolID: call.Call.ID, Content: "total 0"},
		},
		MaxTokens: 1024,
	}
	if _, err := drain(t, context.Background(), p, req); err != nil {
		t.Fatalf("stream: %v", err)
	}
	body := string(e.lastBody(t))
	if !strings.Contains(body, `"type":"tool_use","id":"toolu_9","name":"bash","input":{"command":"ls -la"}`) {
		t.Fatalf("the id must pair on the way back:\n%s", body)
	}
	if !strings.Contains(body, `"type":"tool_result","tool_use_id":"toolu_9"`) {
		t.Fatalf("the result must pair by id:\n%s", body)
	}
}

func TestEmptyMessageListFailsLoud(t *testing.T) {
	p := anthropic.New(anthropic.Config{BaseURL: "http://x", Model: "m"})
	if _, err := p.Stream(context.Background(), core.Request{}); err == nil || !strings.Contains(err.Error(), "empty message list") {
		t.Fatalf("err = %v, want the loud empty-list refusal", err)
	}
}

func TestMaxTokensFallsBackToTheRowThenRefuses(t *testing.T) {
	e := captureEndpoint(t)
	p := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "claude-fake", MaxTokens: 777})
	req := core.Request{Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}}}
	if _, err := drain(t, context.Background(), p, req); err != nil {
		t.Fatalf("stream: %v", err)
	}
	body := string(e.lastBody(t))
	if !strings.Contains(body, `"max_tokens":777`) {
		t.Fatalf("the config max_tokens is the fallback: %s", body)
	}
	bare := anthropic.New(anthropic.Config{BaseURL: e.url, Model: "claude-fake"})
	if _, err := drain(t, context.Background(), bare, req); err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("err = %v, want the loud max_tokens refusal", err)
	}
}
