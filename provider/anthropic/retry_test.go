package anthropic_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/provider/anthropic"
)

func scriptedStreamServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, attempt int) bool) (*httptest.Server, *int32) {
	t.Helper()
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		if handle(w, r, int(n)) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, doneStream)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func retryProvider(url string, retries int) core.Provider {
	return anthropic.New(anthropic.Config{
		BaseURL:   url,
		Model:     "claude-fake",
		MaxTokens: 1024,
		Retries:   retries,
		RetryBase: time.Millisecond,
		Jitter:    func() float64 { return 0 },
	})
}

func Test429HonorsRetryAfter(t *testing.T) {
	srv, requests := scriptedStreamServer(t, func(w http.ResponseWriter, r *http.Request, attempt int) bool {
		if attempt <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
			return true
		}
		return false
	})
	p := anthropic.New(anthropic.Config{
		BaseURL:   srv.URL,
		Model:     "claude-fake",
		MaxTokens: 1024,
		Retries:   3,
		RetryBase: time.Hour,
		Jitter:    func() float64 { return 0 },
	})
	start := time.Now()
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if atomic.LoadInt32(requests) != 3 {
		t.Fatalf("requests = %d, want the two 429s plus the success", requests)
	}
	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Fatalf("the retry-after waits must be honoured, took %s", elapsed)
	}
	if got := kinds(events); got != "done" {
		t.Fatalf("events = %s, want a clean done after the waits", got)
	}
}

func Test429RetriesAreBounded(t *testing.T) {
	srv, requests := scriptedStreamServer(t, func(w http.ResponseWriter, r *http.Request, attempt int) bool {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
		return true
	})
	events, err := drain(t, context.Background(), retryProvider(srv.URL, 2), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if atomic.LoadInt32(requests) != 3 {
		t.Fatalf("requests = %d, want the bound plus the first attempt", requests)
	}
	if f := lastFault(t, events); f.Err.Error() != "anthropic: 429: rate_limit_error: slow down" {
		t.Fatalf("fault = %v", f.Err)
	}
}

func Test5xxRetriesWithBackoff(t *testing.T) {
	srv, requests := scriptedStreamServer(t, func(w http.ResponseWriter, r *http.Request, attempt int) bool {
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"internal"}}`)
			return true
		}
		return false
	})
	start := time.Now()
	events, err := drain(t, context.Background(), retryProvider(srv.URL, 3), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if atomic.LoadInt32(requests) != 2 {
		t.Fatalf("requests = %d, want the 503 plus the success", requests)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the backoff must stay small, took %s", elapsed)
	}
	if got := kinds(events); got != "done" {
		t.Fatalf("events = %s", got)
	}
}

func TestOther4xxFaultsImmediately(t *testing.T) {
	srv, requests := scriptedStreamServer(t, func(w http.ResponseWriter, r *http.Request, attempt int) bool {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`)
		return true
	})
	events, err := drain(t, context.Background(), retryProvider(srv.URL, 3), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if atomic.LoadInt32(requests) != 1 {
		t.Fatalf("requests = %d, want no retry on a plain 4xx", requests)
	}
	if f := lastFault(t, events); !strings.Contains(f.Err.Error(), "authentication_error") {
		t.Fatalf("fault = %v", f.Err)
	}
}

func TestNoRetryOnceTheStreamStarted(t *testing.T) {
	srv, requests := scriptedStreamServer(t, func(w http.ResponseWriter, r *http.Request, attempt int) bool {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"type":"message_start","message":{"id":"m","model":"m","usage":{"input_tokens":1}}}`+"\n\n")
		io.WriteString(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`+"\n\n")
		w.(http.Flusher).Flush()
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
		return true
	})
	events, err := drain(t, context.Background(), retryProvider(srv.URL, 3), userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if atomic.LoadInt32(requests) != 1 {
		t.Fatalf("requests = %d, a stream failure never retries", requests)
	}
	if f := lastFault(t, events); !strings.Contains(f.Err.Error(), "stream read") {
		t.Fatalf("fault = %v", f.Err)
	}
}
