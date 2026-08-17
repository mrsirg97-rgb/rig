package openai_test

// The SPEC_HARDENING named cases (decision 2, decision 3): the reasoning
// round-trip over the wire and the cache usage fields, failing first
// against the adapter that had neither.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/provider/openai"
)

func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// delta.reasoning_content streams as ReasoningDelta, in stream order —
// the thinking arrives before the answer, as the live swap emits it.
func TestReasoningContentStreamsAsReasoningDelta(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"let me "}}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"think"}}]}`,
		`data: {"choices":[{"delta":{"content":"the answer"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := sseServer(t, body)

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var (
		out  []string
		reas string
		text string
	)
	for _, ev := range events {
		switch e := ev.(type) {
		case core.ReasoningDelta:
			out = append(out, "reasoning")
			reas += e.Text
		case core.TextDelta:
			out = append(out, "delta")
			text += e.Text
		case core.Done:
			out = append(out, "done")
		}
	}
	if strings.Join(out, ",") != "reasoning,reasoning,delta,done" {
		t.Fatalf("stream order = %v, want reasoning,reasoning,delta,done", out)
	}
	if reas != "let me think" || text != "the answer" {
		t.Fatalf("reasoning = %q, text = %q", reas, text)
	}
}

// A delta carrying both kinds emits the thinking first: the stream order
// is the wire order, and a unit's own order is thinking-then-speech.
func TestReasoningPrecedesContentWithinOneDelta(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"hmm","content":"sure"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := sseServer(t, body)

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var order []string
	for _, ev := range events {
		switch ev.(type) {
		case core.ReasoningDelta:
			order = append(order, "reasoning")
		case core.TextDelta:
			order = append(order, "delta")
		}
	}
	if strings.Join(order, ",") != "reasoning,delta" {
		t.Fatalf("in-delta order = %v, want reasoning,delta", order)
	}
}

// usage.prompt_tokens_details.cached_tokens maps to CacheRead (grounded
// live: warm 918 of 922 prompt tokens).
func TestUsageCacheReadMapsFromPromptTokensDetails(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"x"}}],"usage":{"prompt_tokens":922,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":918}}}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := sseServer(t, body)

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	done := events[len(events)-1].(core.Done)
	if done.Usage.Prompt != 922 || done.Usage.Completion != 10 {
		t.Fatalf("usage = %+v, want prompt=922 completion=10", done.Usage)
	}
	if done.Usage.CacheRead != 918 {
		t.Fatalf("CacheRead = %d, want 918 (cached_tokens on the wire)", done.Usage.CacheRead)
	}
}

// An absent details object reports zero: no cache accounting, not an error.
func TestUsageCacheAbsentReportsZero(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"x"}}],"usage":{"prompt_tokens":3,"completion_tokens":7}}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := sseServer(t, body)

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	done := events[len(events)-1].(core.Done)
	if done.Usage.CacheRead != 0 || done.Usage.CacheWrite != 0 {
		t.Fatalf("absent cache fields must report zero, got %+v", done.Usage)
	}
}

// cache_write_tokens maps when the server reports it (zero until then).
func TestUsageCacheWriteMapsWhenReported(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"x"}}],"usage":{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":40,"cache_write_tokens":60}}}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := sseServer(t, body)

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	done := events[len(events)-1].(core.Done)
	if done.Usage.CacheRead != 40 || done.Usage.CacheWrite != 60 {
		t.Fatalf("cache fields = read %d write %d, want 40/60", done.Usage.CacheRead, done.Usage.CacheWrite)
	}
}

// The round-trip: an assistant message carrying Reasoning goes back over
// the wire as reasoning_content (the F2 wire-shape precedent).
func TestAssistantReasoningRoundTripsOverTheWire(t *testing.T) {
	var captured map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n")
	}))
	t.Cleanup(srv.Close)

	p := openai.New(srv.URL, "local")
	req := core.Request{Messages: []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleAssistant, Content: "the answer", Reasoning: "the thinking behind it"},
	}}
	if _, err := drain(t, context.Background(), p, req); err != nil {
		t.Fatalf("stream: %v", err)
	}

	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(captured["messages"], &msgs); err != nil {
		t.Fatalf("messages is not an array: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d entries, want 2", len(msgs))
	}
	rc, ok := stringOrError(msgs[1]["reasoning_content"])
	if !ok || rc != "the thinking behind it" {
		t.Fatalf("assistant reasoning_content = %q (ok=%v), want %q", rc, ok, "the thinking behind it")
	}
}

func stringOrError(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// The adapter must request the usage chunk on the stream: OpenAI and
// llama.cpp emit usage only when stream_options.include_usage is set —
// without it Done.Usage is all zeros and the cache line reads zero
// (verified live against the swap). Assert the field goes over the wire,
// the way the shape test asserts parameters is an object.
func TestStreamRequestsUsageOnTheWire(t *testing.T) {
	var captured json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]json.RawMessage
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		captured = req["stream_options"]
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n")
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	if _, err := drain(t, context.Background(), p, userReq()); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("request body has no stream_options — the server will not send a usage chunk")
	}
	var opts struct {
		IncludeUsage bool `json:"include_usage"`
	}
	if err := json.Unmarshal(captured, &opts); err != nil {
		t.Fatalf("stream_options is not an object: %v", err)
	}
	if !opts.IncludeUsage {
		t.Fatalf("stream_options.include_usage = %v, want true", opts.IncludeUsage)
	}
}
