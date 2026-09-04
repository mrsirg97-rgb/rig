package scheduler_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

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

func TestSocketProxySetsPrivatePermissions(t *testing.T) {
	dest, srv := newScriptSrv(t)
	_ = dest
	sock := t.TempDir() + "/rig.sock"
	proxy, err := sched.NewSocketProxy(sock, srv.URL)
	if err != nil {
		t.Fatalf("NewSocketProxy: %v", err)
	}
	defer proxy.Close()
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o777 != 0o600 {
		t.Fatalf("socket mode = %o, want 600 (any local user must not reach the model endpoint)", fi.Mode()&0o777)
	}
}
