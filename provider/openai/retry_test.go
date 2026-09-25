package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

func TestLocalRowRetriesEmpty502(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n")
	}))
	defer srv.Close()

	p := NewWithConfig(Config{
		BaseURL: srv.URL, Model: "m",
		RetryBase: 5 * time.Millisecond, Jitter: func() float64 { return 0 },
	})
	started := time.Now()
	events, err := hostedDrain(t, context.Background(), p, userReq())
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("request count = %d, want 3 (two empty 502s then success)", got)
	}
	if elapsed < 15*time.Millisecond {
		t.Fatalf("backoff = %v, want at least 15ms (base 5ms: 5ms + 10ms)", elapsed)
	}
	if got := kinds(events); !strings.Contains(got, "done") {
		t.Fatalf("event kinds = %s, want a done after the retries", got)
	}
	for _, ev := range events {
		if _, ok := ev.(core.Fault); ok {
			t.Fatal("an empty 502 before the first token must be retried, not faulted")
		}
	}
}

func TestEmpty502RetryBoundExhaustsWithFault(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	p := NewWithConfig(Config{
		BaseURL: srv.URL, Model: "m",
		RetryBase: 5 * time.Millisecond, Jitter: func() float64 { return 0 },
	})
	events, err := hostedDrain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("request count = %d, want 4 (the hosted 3-retry bound plus the first attempt)", got)
	}
	ft := lastFault(t, events)
	if !strings.Contains(ft.Err.Error(), "502") {
		t.Fatalf("fault must name the status after the bound, got %v", ft.Err)
	}
}

func TestLocalRow502WithBodyFaults(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "backend exploded")
	}))
	defer srv.Close()

	p := NewWithConfig(Config{
		BaseURL: srv.URL, Model: "m",
		RetryBase: 5 * time.Millisecond, Jitter: func() float64 { return 0 },
	})
	events, err := hostedDrain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("request count = %d, want 1 (a 502 carrying a body is a real error)", got)
	}
	ft := lastFault(t, events)
	if !strings.Contains(ft.Err.Error(), "backend exploded") {
		t.Fatalf("fault must carry the response snippet, got %v", ft.Err)
	}
}

func TestNoRetryAfterFirstToken(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"half\"}}]}\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("test server must support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		conn.Close()
	}))
	defer srv.Close()

	p := NewWithConfig(Config{
		BaseURL: srv.URL, Model: "m",
		Retries: 3, RetryBase: 5 * time.Millisecond, Jitter: func() float64 { return 0 },
	})
	events, err := hostedDrain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("request count = %d, want 1 (never retry after the first token)", got)
	}
	if _, ok := events[len(events)-1].(core.Fault); !ok {
		t.Fatalf("a stream that dies mid-way must Fault, got %s", kinds(events))
	}
}
