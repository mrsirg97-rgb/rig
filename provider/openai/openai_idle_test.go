package openai_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/provider/openai"
)

func TestAStallingStreamFaultsAfterTheIdleBound(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"hello"}}]}`+"\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := openai.NewWithTimeouts(srv.URL, "local", time.Minute, 50*time.Millisecond)
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
			io.WriteString(w, `data: {"choices":[{"delta":{"content":"x"}}]}`+"\n")
			fl.Flush()
			time.Sleep(30 * time.Millisecond)
		}
		io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n"+`data: [DONE]`+"\n")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := openai.NewWithTimeouts(srv.URL, "local", time.Minute, 150*time.Millisecond)
	events, err := drain(t, ctx, p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := kinds(events); got != strings.Repeat("delta,", 12)+"done" {
		t.Fatalf("a stream with gaps under the bound must complete, got %s", got)
	}
}
