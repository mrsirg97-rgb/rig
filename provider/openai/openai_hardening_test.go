package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/provider/openai"
)

func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

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

func TestMaxTokensWireShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]json.RawMessage
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n")
		if _, ok := req["max_tokens"]; !ok {
			t.Fatal("max_tokens is absent on the wire")
		}
		var mt int
		if err := json.Unmarshal(req["max_tokens"], &mt); err != nil {
			t.Fatalf("max_tokens is not an integer: %v", err)
		}
		if mt != 8192 {
			t.Fatalf("max_tokens = %d, want 8192", mt)
		}
	}))
	t.Cleanup(srv.Close)
	p := openai.New(srv.URL, "local")
	req := userReq()
	req.MaxTokens = 8192
	if _, err := drain(t, context.Background(), p, req); err != nil {
		t.Fatalf("stream: %v", err)
	}

	var saw bool
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]json.RawMessage
		_ = json.Unmarshal(body, &req)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n")
		_, saw = req["max_tokens"]
	}))
	t.Cleanup(srv2.Close)
	p2 := openai.New(srv2.URL, "local")
	if _, err := drain(t, context.Background(), p2, userReq()); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if saw {
		t.Fatal("max_tokens must be absent when 0 (the provider's default)")
	}
}

func TestReasoningEffortWireShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]json.RawMessage
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n")

		var top string
		if err := json.Unmarshal(req["reasoning_effort"], &top); err != nil {
			t.Fatalf("reasoning_effort is not a string: %v", err)
		}
		if top != "medium" {
			t.Fatalf("reasoning_effort = %q, want medium", top)
		}
		var kwargs struct {
			ReasoningEffort string `json:"reasoning_effort"`
		}
		if err := json.Unmarshal(req["chat_template_kwargs"], &kwargs); err != nil {
			t.Fatalf("chat_template_kwargs is not an object: %v", err)
		}
		if kwargs.ReasoningEffort != "medium" {
			t.Fatalf("chat_template_kwargs.reasoning_effort = %q, want medium", kwargs.ReasoningEffort)
		}
	}))
	t.Cleanup(srv.Close)
	p := openai.New(srv.URL, "local")
	req := userReq()
	req.ReasoningEffort = "medium"
	if _, err := drain(t, context.Background(), p, req); err != nil {
		t.Fatalf("stream: %v", err)
	}

	var sawTop, sawKwargs bool
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]json.RawMessage
		_ = json.Unmarshal(body, &req)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n")
		_, sawTop = req["reasoning_effort"]
		_, sawKwargs = req["chat_template_kwargs"]
	}))
	t.Cleanup(srv2.Close)
	p2 := openai.New(srv2.URL, "local")
	if _, err := drain(t, context.Background(), p2, userReq()); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if sawTop {
		t.Fatal("reasoning_effort must be absent when empty (the server's default)")
	}
	if sawKwargs {
		t.Fatal("chat_template_kwargs must be absent when empty (the server's default)")
	}
}

func TestAStallingServerBoundsTheWaitForHeaders(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	p := openai.NewWithHeaderTimeout(srv.URL, "local", 50*time.Millisecond)
	start := time.Now()
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %v, want exactly the one transport fault", kinds(events))
	}
	f, ok := events[0].(core.Fault)
	if !ok {
		t.Fatalf("event 0 = %+v, want the transport fault", events[0])
	}
	if !strings.Contains(f.Err.Error(), "transport") {
		t.Fatalf("the fault must name the transport, got %v", f.Err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the header timeout must bound the wait, took %s", elapsed)
	}
}
