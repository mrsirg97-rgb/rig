package openai_test

// The unix-socket base URL (SPEC_SANDBOX 1): the jailed worker's
// model calls ride the one bound socket — the provider dials the
// named socket, and the request's path is the OpenAI suffix, clean
// (the socket path is the transport's, not the wire's).

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/provider/openai"
)

// unixSrv serves one SSE completion on a unix socket and records what
// it was asked for (the socket proxy's destination, in the test's
// place).
type unixSrv struct {
	mu    sync.Mutex
	paths []string
	body  string
}

func newUnixSrv(t *testing.T) (*unixSrv, string) {
	t.Helper()
	s := &unixSrv{}
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "provider.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.body = string(b)
		s.mu.Unlock()
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n")
	})}
	go srv.Serve(ln)
	t.Cleanup(func() {
		srv.Close()
		ln.Close()
	})
	return s, ln.Addr().String()
}

func (s *unixSrv) saw() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// TestUnixBaseURLDialsTheSocket (SPEC_SANDBOX 1): a unix: base URL
// dials the named socket; the stream flows; the destination sees the
// OpenAI path, clean (no socket path on the wire).
func TestUnixBaseURLDialsTheSocket(t *testing.T) {
	srv, sock := newUnixSrv(t)
	p := openai.New("unix:"+sock, "qwen3.8-workers")
	events, err := drain(t, context.Background(), p, userReq())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if _, ok := events[len(events)-1].(core.Done); !ok {
		t.Fatalf("expected Done; got events %v", events)
	}
	found := false
	for _, ev := range events {
		if td, ok := ev.(core.TextDelta); ok && td.Text == "ok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the stream must flow through the socket; events %v", events)
	}
	if got := srv.saw(); len(got) != 1 || got[0] != "/chat/completions" {
		t.Fatalf("the destination saw %v, want exactly [/chat/completions] (the socket path is off the wire)", got)
	}
	if !strings.Contains(srv.body, `"qwen3.8-workers"`) {
		t.Fatalf("the request must carry the model, got %q", srv.body)
	}
}
