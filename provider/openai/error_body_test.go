package openai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/provider/openai"
)

func TestErrorBodyIsCappedAtASnippet(t *testing.T) {
	const bodySize = 1 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", bodySize)))
	}))
	defer srv.Close()

	p := openai.New(srv.URL, "m")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	f := lastFault(t, events)
	if !strings.Contains(f.Err.Error(), "openai: 502") {
		t.Fatalf("the fault must name the status, got %v", f.Err)
	}
	if xs := strings.Count(f.Err.Error(), "x"); xs > 256 {
		t.Fatalf("the fault must carry a bounded snippet of the body, got %d body bytes", xs)
	}
}
