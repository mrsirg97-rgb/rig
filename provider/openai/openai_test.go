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

func userReq() core.Request {
	return core.Request{Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}}}
}

func TestStreamsTextDeltasAndDone(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hello "}}]}`,
		`data: {"choices":[{"delta":{"content":"world"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); got != "delta,delta,done" {
		t.Fatalf("event order = %s, want delta,delta,done", got)
	}
	var text string
	for _, ev := range events {
		if d, ok := ev.(core.TextDelta); ok {
			text += d.Text
		}
	}
	if text != "hello world" {
		t.Fatalf("accumulated text = %q, want %q", text, "hello world")
	}
	done := events[len(events)-1].(core.Done)
	if done.StopReason != "stop" {
		t.Fatalf("stop reason = %q, want stop", done.StopReason)
	}
}

func TestAccumulatesSplitToolCallArgs(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"bash","arguments":"{\"comma"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"nd\":\"ls\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var calls []core.ToolCall
	for _, ev := range events {
		if c, ok := ev.(core.ToolCallEvent); ok {
			calls = append(calls, c.Call)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("tool calls = %d, want exactly 1 accumulated before Done", len(calls))
	}
	call := calls[0]
	if call.ID != "c1" || call.Name != "bash" {
		t.Fatalf("accumulated call identity wrong: %+v", call)
	}
	if got := string(call.Args); got != `{"command":"ls"}` {
		t.Fatalf("split args accumulated as %s, want %s", got, `{"command":"ls"}`)
	}
	if got := kinds(events); !strings.HasSuffix(got, ",call,done") && got != "call,done" {
		t.Fatalf("event order %s must place the call before Done", got)
	}
}

func TestLengthFinishedTruncatedToolCallArgsFault(t *testing.T) {

	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"bash","arguments":"{\"command\": \"cat /tmp/longfile"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"length"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); !strings.HasSuffix(got, "fault") {
		t.Fatalf("truncated tool call args (finish length) must Fault, got %s", got)
	}
	for _, ev := range events {
		if _, ok := ev.(core.ToolCallEvent); ok {
			t.Fatal("a truncated tool call must not be emitted into the transcript")
		}
		if _, ok := ev.(core.Done); ok {
			t.Fatal("no Done after a truncated tool call")
		}
	}
	msg := lastFault(t, events).Err.Error()
	if !strings.Contains(msg, "bash") || !strings.Contains(msg, "truncated") {
		t.Fatalf("fault must name the call and the cause, got %q", msg)
	}
}

func TestNoArgToolCallStillEmitted(t *testing.T) {

	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"ping"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var calls []core.ToolCall
	for _, ev := range events {
		if c, ok := ev.(core.ToolCallEvent); ok {
			calls = append(calls, c.Call)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("no-arg tool call must still be emitted, got %d calls (%s)", len(calls), kinds(events))
	}
	if len(calls[0].Args) != 0 {
		t.Fatalf("no-arg call args = %s, want empty", calls[0].Args)
	}
}

func TestMalformedJSONLineFaults(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"half "}}]}`,
		`data: {this is not json`,
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); !strings.HasSuffix(got, ",fault") {
		t.Fatalf("malformed JSON line must terminate with a Fault, got %s", got)
	}
	if msg := lastFault(t, events).Err.Error(); !strings.Contains(msg, "not json") {
		t.Fatalf("fault must carry the offending line for diagnosis, got %q", msg)
	}
}

func TestTruncatedStreamFaults(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"and then the model simply"}}]}`,
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); !strings.HasSuffix(got, ",fault") {
		t.Fatalf("truncated stream (no finish marker) must Fault, got %s", got)
	}
}

func TestNon200Faults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "backend exploded")
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	ft, ok := events[len(events)-1].(core.Fault)
	if !ok {
		t.Fatalf("HTTP %d must Fault, got %s", http.StatusInternalServerError, kinds(events))
	}
	if msg := ft.Err.Error(); !strings.Contains(msg, "500") {
		t.Fatalf("fault must name the status, got %q", msg)
	}
}

func TestEmptyMessageListFailsLoud(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request may leave the building with an empty message list")
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	if _, err := drain(t, context.Background(), p, core.Request{}); err == nil {
		t.Fatal("empty message list must fail loudly")
	} else if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("failure must name the empty list, got %v", err)
	}
}

func TestNonSSELineFaults(t *testing.T) {
	body := `{"choices":[{"delta":{"content":"plain json"}}]}\n`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if _, ok := events[len(events)-1].(core.Fault); !ok {
		t.Fatalf("non-SSE line must Fault, got %s", kinds(events))
	}
}

func TestCancellationTearsDownTheStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"late"}}]}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	p := openai.New(srv.URL, "local")
	ch, err := p.Stream(ctx, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()

	var events []core.Event
	for ev := range ch {
		events = append(events, ev)
	}
	for _, ev := range events {
		if _, ok := ev.(core.Done); ok {
			t.Fatal("torn-down stream must not carry a Done")
		}
	}
}

func TestUsageWhenPresent(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"x"}}],"usage":{"prompt_tokens":3,"completion_tokens":7}}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	done := events[len(events)-1].(core.Done)
	if done.Usage.Prompt != 3 || done.Usage.Completion != 7 {
		t.Fatalf("usage = %+v, want prompt=3 completion=7", done.Usage)
	}
}

func TestToolSchemaGoesOverTheWireAsAnObject(t *testing.T) {
	var captured json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]json.RawMessage
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		var tools []map[string]json.RawMessage
		if err := json.Unmarshal(req["tools"], &tools); err != nil {
			t.Fatalf("tools is not an object array: %v", err)
		}
		if len(tools) != 1 {
			t.Fatalf("tools = %d entries, want 1", len(tools))
		}
		var fnObj map[string]json.RawMessage
		if err := json.Unmarshal(tools[0]["function"], &fnObj); err != nil {
			t.Fatalf("tools[0].function is not an object: %v", err)
		}
		var paramsObj map[string]json.RawMessage
		if err := json.Unmarshal(fnObj["parameters"], &paramsObj); err != nil {
			t.Fatalf("tools[0].function.parameters must be a JSON object, not a quoted string: %v", err)
		}
		var tStr string
		if err := json.Unmarshal(paramsObj["type"], &tStr); err != nil || tStr != "object" {
			t.Fatalf("parameters.type = %s, want \"object\"", paramsObj["type"])
		}
		captured = req["tools"]
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n")
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	req := core.Request{
		Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
		Tools:    []core.ToolSpec{{Name: "bash", Description: "runs commands", Schema: json.RawMessage(`{"type":"object"}`)}},
	}
	if _, err := drain(t, context.Background(), p, req); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("no tool array captured")
	}
	var capturedTools []map[string]json.RawMessage
	if err := json.Unmarshal(captured, &capturedTools); err != nil {
		t.Fatalf("captured tools array: %v", err)
	}
	fnRaw, ok := capturedTools[0]["function"]
	if !ok {
		t.Fatal("captured tools[0] has no function key")
	}
	var capturedFn map[string]json.RawMessage
	if err := json.Unmarshal(fnRaw, &capturedFn); err != nil {
		t.Fatalf("captured function: %v", err)
	}
	if got, _ := json.Marshal(capturedFn["description"]); string(got) != `"runs commands"` {
		t.Fatalf("function.description = %s, want %q", got, `"runs commands"`)
	}
}

func TestBaseURLJoining(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n")
	}))
	defer srv.Close()

	for _, baseURL := range []string{srv.URL, srv.URL + "/v1"} {
		path = ""
		p := openai.New(baseURL, "local")
		events, err := drain(t, context.Background(), p, userReq())
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if _, ok := events[len(events)-1].(core.Done); !ok {
			t.Fatalf("expected Done; got events %v", events)
		}

		if !strings.HasSuffix(path, "/chat/completions") {
			t.Fatalf("baseURL %q joined to %q, want a /chat/completions suffix", baseURL, path)
		}
		if baseURL == srv.URL && path != "/chat/completions" {
			t.Fatalf("bare baseURL joined to %q, want /chat/completions", path)
		}
		if strings.HasSuffix(baseURL, "/v1") && path != "/v1/chat/completions" {
			t.Fatalf("v1 baseURL joined to %q, want /v1/chat/completions", path)
		}
	}
}

func TestStreamIgnoresSSEComments(t *testing.T) {
	body := strings.Join([]string{
		`: ping`,
		`data: {"choices":[{"delta":{"content":"hello"}}]}`,
		`:`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "local")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); got != "delta,done" {
		t.Fatalf("event order = %s, want delta,done (the comments invisible)", got)
	}
}
