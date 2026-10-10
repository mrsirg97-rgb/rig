package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/models"
)

func TestAnAnthropicRowSelectsTheMessagesWire(t *testing.T) {
	var mu sync.Mutex
	var path, key, version string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path, key, version = r.URL.Path, r.Header.Get("x-api-key"), r.Header.Get("anthropic-version")
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"claude\",\"usage\":{\"input_tokens\":1}}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	r := &root{
		row: models.Model{
			ID: "claude", Provider: "anthropic", Remote: true, BaseURL: srv.URL,
			APIKey: "row-key", MaxTokens: 8192, Retries: 3,
		},
		activeID: "claude",
		baseURL:  "http://swap/v1",
	}
	ch, err := r.buildProvider().Stream(context.Background(), core.Request{
		Messages:  []core.Message{{Role: core.RoleUser, Content: "hi"}},
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for ev := range ch {
		if f, ok := ev.(core.Fault); ok {
			t.Fatalf("the anthropic row must stream through the Messages wire: %v", f.Err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if path != "/v1/messages" {
		t.Fatalf("path = %q, want the Messages endpoint and not the swap's chat completions", path)
	}
	if key != "row-key" {
		t.Fatalf("x-api-key = %q, want the row's key", key)
	}
	if version != "2023-06-01" {
		t.Fatalf("anthropic-version = %q", version)
	}
}

func TestAPlainRemoteRowKeepsTheOpenAIWire(t *testing.T) {
	var mu sync.Mutex
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path = r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	r := &root{
		row: models.Model{
			ID: "deep", Provider: "deepseek", Remote: true, BaseURL: srv.URL,
			APIKey: "row-key", MaxTokens: 8192, Retries: 3,
		},
		activeID: "deep",
		baseURL:  "http://swap/v1",
	}
	ch, err := r.buildProvider().Stream(context.Background(), core.Request{
		Messages:  []core.Message{{Role: core.RoleUser, Content: "hi"}},
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for ev := range ch {
		if f, ok := ev.(core.Fault); ok {
			t.Fatalf("the plain remote row must keep the openai wire: %v", f.Err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if path != "/chat/completions" {
		t.Fatalf("path = %q, want the openai suffix", path)
	}
}
