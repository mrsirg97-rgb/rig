package scheduler

// The socket proxy (SPEC_SANDBOX 1): the one hole in the jail. One
// unix socket, one destination (the swap endpoint), the OpenAI path
// prefix applied exactly once. Stdlib only (net, httputil): the proxy
// is a reverse forwarder, not a server — it holds no state of its
// own and answers nothing it does not forward.

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// SocketProxy listens on a unix socket and forwards every request to
// one destination: the swap endpoint, under the /v1 path the worker's
// socket URL omits. The socket file is created on construction and
// removed on Close.
type SocketProxy struct {
	sock string
	ln   net.Listener
	srv  *http.Server
}

// NewSocketProxy listens on sockPath and forwards to target (the swap
// endpoint, e.g. http://127.0.0.1:8090). A target that already
// carries the /v1 suffix (the operator's spelling) is normalized: the
// prefix is applied exactly once, whatever the spelling.
func NewSocketProxy(sockPath, target string) (*SocketProxy, error) {
	base, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q (the swap endpoint is http)", base.Scheme)
	}
	prefix := strings.TrimSuffix(base.Path, "/v1") + "/v1"

	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		return nil, err
	}
	// a stale socket from a crashed run is ours to clear (the path is
	// the runner's, by convention).
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}

	// FlushInterval -1: the completion stream is SSE; the deltas must
	// flow as they arrive, not in write chunks.
	rp := &httputil.ReverseProxy{
		FlushInterval: -1,
		Director: func(req *http.Request) {
			req.URL.Scheme = base.Scheme
			req.URL.Host = base.Host
			req.URL.Path = prefix + req.URL.Path
			req.Host = base.Host
		},
	}
	srv := &http.Server{Handler: rp}
	go func() {
		srv.Serve(ln) // Close's error is the one Close reports
	}()
	return &SocketProxy{sock: sockPath, ln: ln, srv: srv}, nil
}

// Close tears the hole down: the listener, the in-flight forwarders,
// and the socket file — after the run, nothing answers.
func (p *SocketProxy) Close() error {
	err := p.srv.Close()
	p.ln.Close()
	os.Remove(p.sock)
	return err
}
