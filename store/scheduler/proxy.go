package scheduler

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

type SocketProxy struct {
	sock string
	ln   net.Listener
	srv  *http.Server
}

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

	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		ln.Close()
		return nil, err
	}

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
		srv.Serve(ln)
	}()
	return &SocketProxy{sock: sockPath, ln: ln, srv: srv}, nil
}

func (p *SocketProxy) Close() error {
	err := p.srv.Close()
	p.ln.Close()
	os.Remove(p.sock)
	return err
}
