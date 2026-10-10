package anthropic_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/provider/anthropic"
)

func TestAStallingServerBoundsTheWaitForHeaders(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	p := anthropic.New(anthropic.Config{BaseURL: srv.URL, Model: "m", MaxTokens: 1024, HeaderTimeout: 50 * time.Millisecond})
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

func TestAStallingStreamFaultsAfterTheIdleBound(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"m\",\"usage\":{\"input_tokens\":1}}}\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := anthropic.New(anthropic.Config{BaseURL: srv.URL, Model: "m", MaxTokens: 1024, HeaderTimeout: time.Minute, IdleTimeout: 50 * time.Millisecond})
	start := time.Now()
	events, err := drain(t, ctx, p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	f := lastFault(t, events)
	if !strings.Contains(f.Err.Error(), "idle") {
		t.Fatalf("the fault must name the idle bound, got %v", f.Err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the idle bound must end the wait, took %s", elapsed)
	}
}

func TestIdleBoundResetsOnEveryEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for i := 0; i < 12; i++ {
			io.WriteString(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
			io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n")
			fl.Flush()
			time.Sleep(30 * time.Millisecond)
		}
		io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := anthropic.New(anthropic.Config{BaseURL: srv.URL, Model: "m", MaxTokens: 1024, HeaderTimeout: time.Minute, IdleTimeout: 150 * time.Millisecond})
	events, err := drain(t, ctx, p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for _, ev := range events {
		if f, ok := ev.(core.Fault); ok {
			t.Fatalf("a stream with gaps under the bound must complete: %v", f.Err)
		}
	}
	if got := kinds(events); !strings.HasSuffix(got, "done") {
		t.Fatalf("events = %s, want the stream to survive the gaps", got)
	}
}
