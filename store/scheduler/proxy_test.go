package scheduler_test

// The socket proxy's named cases (SPEC_SANDBOX 1, testing): the one
// hole — one unix socket, one destination (the swap endpoint's /v1
// path), the stream flowing, nothing answering after the run.

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

// scriptSrv is the destination the proxy forwards to (the swap
// endpoint): it records the paths it is asked for and replies with a
// canned SSE completion.
type scriptSrv struct {
	mu    sync.Mutex
	paths []string
}

func newScriptSrv(t *testing.T) (*scriptSrv, *httptest.Server) {
	t.Helper()
	s := &scriptSrv{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n")
	}))
	t.Cleanup(srv.Close)
	return s, srv
}

func (s *scriptSrv) saw() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

func unixClient(sock string) *http.Client {
	d := net.Dialer{}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
}

// TestSocketProxyForwardsToOneDestination (SPEC_SANDBOX 1): a model
// call through the bound socket reaches exactly the destination, under
// the OpenAI path prefix the worker's socket URL omits.
func TestSocketProxyForwardsToOneDestination(t *testing.T) {
	dest, srv := newScriptSrv(t)
	sock := t.TempDir() + "/rig.sock"
	proxy, err := sched.NewSocketProxy(sock, srv.URL)
	if err != nil {
		t.Fatalf("NewSocketProxy: %v", err)
	}
	defer proxy.Close()

	resp, err := unixClient(sock).Post("http://localhost/chat/completions", "application/json", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatalf("the call through the socket: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"content":"ok"`) {
		t.Fatalf("the stream must flow through the proxy, got %q", body)
	}
	if got := dest.saw(); len(got) != 1 || got[0] != "/v1/chat/completions" {
		t.Fatalf("the destination saw %v, want exactly [/v1/chat/completions] (one destination, the /v1 prefix applied)", got)
	}
}

// TestSocketProxyClosedRefusesDials (SPEC_SANDBOX 1): after the run,
// the hole is gone — the socket file is removed and nothing answers.
func TestSocketProxyClosedRefusesDials(t *testing.T) {
	_, srv := newScriptSrv(t)
	sock := t.TempDir() + "/rig.sock"
	proxy, err := sched.NewSocketProxy(sock, srv.URL)
	if err != nil {
		t.Fatalf("NewSocketProxy: %v", err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := net.Dial("unix", sock); err == nil {
		t.Fatal("a closed proxy must refuse dials (the socket file is gone)")
	}
}

// TestSocketProxyNormalizesASuffixedTarget (SPEC_SANDBOX 1): the swap
// URL may carry the /v1 suffix (the operator's spelling); the proxy
// still applies the prefix exactly once.
func TestSocketProxyNormalizesASuffixedTarget(t *testing.T) {
	dest, srv := newScriptSrv(t)
	sock := t.TempDir() + "/rig.sock"
	proxy, err := sched.NewSocketProxy(sock, srv.URL+"/v1")
	if err != nil {
		t.Fatalf("NewSocketProxy: %v", err)
	}
	defer proxy.Close()

	resp, err := unixClient(sock).Post("http://localhost/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("the call through the socket: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if got := dest.saw(); len(got) != 1 || got[0] != "/v1/chat/completions" {
		t.Fatalf("the destination saw %v, want the prefix applied exactly once", got)
	}
}
