package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

func userReq() core.Request {
	return core.Request{Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}}}
}

func kinds(events []core.Event) string {
	var out []string
	for _, ev := range events {
		switch ev.(type) {
		case core.TextDelta:
			out = append(out, "delta")
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

func hostedDrain(t *testing.T, ctx context.Context, p core.Provider, req core.Request) ([]core.Event, error) {
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

func TestBearerSent(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("Authorization"))
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n")
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL, Model: "m", APIKey: "sk-secret"})
	if _, err := hostedDrain(t, context.Background(), p, userReq()); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got.Load() != "Bearer sk-secret" {
		t.Fatalf("Authorization = %v, want Bearer sk-secret", got.Load())
	}
}

func TestNoAuthHeaderWithoutKey(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("Authorization"))
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n")
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL, Model: "m"})
	if _, err := hostedDrain(t, context.Background(), p, userReq()); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got.Load() != "" {
		t.Fatalf("Authorization = %v, want empty", got.Load())
	}
}

func TestRetries429WithBackoff(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":"rate limited"}`)
			return
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n")
	}))
	defer srv.Close()

	p := NewWithConfig(Config{
		BaseURL: srv.URL, Model: "m",
		Retries: 3, RetryBase: 10 * time.Millisecond, Jitter: func() float64 { return 0 },
	})
	started := time.Now()
	events, err := hostedDrain(t, context.Background(), p, userReq())
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("request count = %d, want 3 (two 429s then success)", got)
	}
	if elapsed < 30*time.Millisecond {
		t.Fatalf("backoff = %v, want at least 30ms (base 10ms: 10ms + 20ms)", elapsed)
	}
	if !strings.Contains(kinds(events), "done") {
		t.Fatalf("event kinds = %s, want a done after the retries", kinds(events))
	}
}

func TestRetries5xxWithBackoff(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n")
	}))
	defer srv.Close()

	p := NewWithConfig(Config{
		BaseURL: srv.URL, Model: "m",
		Retries: 3, RetryBase: 5 * time.Millisecond, Jitter: func() float64 { return 0 },
	})
	if _, err := hostedDrain(t, context.Background(), p, userReq()); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("request count = %d, want 2 (one 502 then success)", got)
	}
}

func TestRetryBoundExhaustsWithFault(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":"still busy"}`)
	}))
	defer srv.Close()

	p := NewWithConfig(Config{
		BaseURL: srv.URL, Model: "m",
		Retries: 2, RetryBase: 5 * time.Millisecond, Jitter: func() float64 { return 0 },
	})
	events, err := hostedDrain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("request count = %d, want 3 (the bound plus the first attempt)", got)
	}
	ft := lastFault(t, events)
	if !strings.Contains(ft.Err.Error(), "429") {
		t.Fatalf("fault must name the status after the bound, got %v", ft.Err)
	}
}

func TestCostParsedFromUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"cost\":0.0012}}\ndata: [DONE]\n")
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL, Model: "m"})
	events, err := hostedDrain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	done := events[len(events)-1].(core.Done)
	if done.Usage.Cost != 0.0012 {
		t.Fatalf("usage cost = %v, want 0.0012", done.Usage.Cost)
	}
	if done.Usage.Prompt != 10 || done.Usage.Completion != 5 {
		t.Fatalf("usage tokens %+v, want prompt 10 completion 5", done.Usage)
	}
}

func openRouterReqBody(t *testing.T, msgs []core.Message, cfg Config) map[string]any {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n")
	}))
	defer srv.Close()
	cfg.BaseURL = srv.URL
	p := NewWithConfig(cfg)
	req := core.Request{Messages: msgs, ReasoningEffort: "high"}
	if _, err := hostedDrain(t, context.Background(), p, req); err != nil {
		t.Fatalf("stream: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("request body: %v", err)
	}
	return m
}

func TestOpenRouterReasoningEchoedUnderItsFieldNames(t *testing.T) {
	details := json.RawMessage(`[{"id":"r1","format":"anthropic-claude-v1","text":"thinking"}]`)
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "go"},
		{Role: core.RoleAssistant, Content: "answer", Reasoning: "thinking", ReasoningDetails: details},
	}
	m := openRouterReqBody(t, msgs, Config{Model: "m", Reasoning: "reasoning"})
	wire, ok := m["messages"].([]any)
	if !ok || len(wire) != 2 {
		t.Fatalf("messages: %v", m["messages"])
	}
	asst := wire[1].(map[string]any)
	if asst["reasoning"] != "thinking" {
		t.Fatalf("assistant reasoning = %v, want \"thinking\"", asst["reasoning"])
	}
	if _, ok := asst["reasoning_details"]; !ok {
		t.Fatalf("reasoning_details must be echoed on later turns: %v", asst)
	}
	if _, ok := asst["reasoning_content"]; ok {
		t.Fatalf("an OpenRouter row must not send reasoning_content: %v", asst)
	}
}

func TestDefaultReasoningContentStays(t *testing.T) {
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "go"},
		{Role: core.RoleAssistant, Content: "answer", Reasoning: "thinking"},
	}
	m := openRouterReqBody(t, msgs, Config{Model: "m"})
	wire := m["messages"].([]any)
	asst := wire[1].(map[string]any)
	if asst["reasoning_content"] != "thinking" {
		t.Fatalf("reasoning_content = %v, want \"thinking\"", asst["reasoning_content"])
	}
	if _, ok := asst["reasoning"]; ok {
		t.Fatalf("a default row must not send reasoning: %v", asst)
	}
}

func TestOpenRouterStreamReasoningAndDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Join([]string{
			`data: {"choices":[{"delta":{"reasoning":"think"}}]}`,
			`data: {"choices":[{"delta":{"reasoning":"ing","reasoning_details":[{"id":"r1","format":"anthropic-claude-v1","text":"thinking"}]}}]}`,
			`data: {"choices":[{"delta":{"content":"answer"}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
			"",
		}, "\n"))
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL, Model: "m", Reasoning: "reasoning"})
	events, err := hostedDrain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var text string
	var details json.RawMessage
	for _, ev := range events {
		if r, ok := ev.(core.ReasoningDelta); ok {
			text += r.Text
			if len(r.Details) > 0 {
				details = r.Details
			}
		}
	}
	if text != "thinking" {
		t.Fatalf("reasoning text = %q, want thinking", text)
	}
	if string(details) != `[{"id":"r1","format":"anthropic-claude-v1","text":"thinking"}]` {
		t.Fatalf("reasoning details = %s", details)
	}
}

func TestRemoteOmitsChatTemplateKwargs(t *testing.T) {
	m := openRouterReqBody(t, []core.Message{{Role: core.RoleUser, Content: "go"}}, Config{Model: "m", Remote: true})
	if _, ok := m["chat_template_kwargs"]; ok {
		t.Fatalf("a remote row must omit chat_template_kwargs: %v", m)
	}
	if m["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort must still ride the request, got %v", m["reasoning_effort"])
	}
}

func TestOpenRouterPinAndCacheControl(t *testing.T) {
	m := openRouterReqBody(t, []core.Message{{Role: core.RoleUser, Content: "go"}}, Config{
		Model: "m", ProviderPin: []string{"Together"}, CacheControl: true,
	})
	provider, ok := m["provider"].(map[string]any)
	if !ok {
		t.Fatalf("provider: %v", m["provider"])
	}
	order, ok := provider["order"].([]any)
	if !ok || len(order) != 1 || order[0] != "Together" {
		t.Fatalf("provider.order = %v, want [Together]", provider["order"])
	}
	if cc, ok := m["cache_control"].(map[string]any); !ok || cc["type"] != "ephemeral" {
		t.Fatalf("cache_control = %v, want {type: ephemeral}", m["cache_control"])
	}
}

func TestLocalRowStillSendsChatTemplateKwargs(t *testing.T) {
	m := openRouterReqBody(t, []core.Message{{Role: core.RoleUser, Content: "go"}}, Config{Model: "m"})
	if _, ok := m["chat_template_kwargs"]; !ok {
		t.Fatalf("a local row must keep chat_template_kwargs: %v", m)
	}
}

func TestHostedFaultsAreLoudAndBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":"no entry"}`)
	}))
	defer srv.Close()

	p := NewWithConfig(Config{BaseURL: srv.URL, Model: "m", Retries: 3, RetryBase: time.Millisecond, Jitter: func() float64 { return 0 }})
	events, err := hostedDrain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	ft := lastFault(t, events)
	if !strings.Contains(ft.Err.Error(), "403") {
		t.Fatalf("a 4xx other than 429 must fault immediately, got %v", ft.Err)
	}
}
