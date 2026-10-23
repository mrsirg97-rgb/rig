package view

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"sync"

	webtool "github.com/mrsirg97-rgb/rig/v3/tool/web"
)

const httpPort = "80"

type DialFn func(ctx context.Context, network, addr string) (net.Conn, error)

type egress struct {
	ln       net.Listener
	srv      *http.Server
	lookup   LookupFn
	dial     DialFn
	upstream *url.URL
	refused  func(raw, why string)
}

func newEgress(cfg Config, refused func(raw, why string)) (*egress, error) {
	var upstream *url.URL
	if cfg.Proxy != "" {
		u, err := url.Parse(cfg.Proxy)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("view: the configured proxy %q is not a proxy URL", cfg.Proxy)
		}
		upstream = u
	}
	dial := cfg.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: guardTimeout}).DialContext
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("view: the browser's egress: %v", err)
	}
	e := &egress{ln: ln, lookup: cfg.Lookup, dial: dial, upstream: upstream, refused: refused}
	e.srv = &http.Server{Handler: e, ReadHeaderTimeout: guardTimeout}
	go e.srv.Serve(ln)
	return e, nil
}

func (e *egress) url() string {
	return "http://" + e.ln.Addr().String()
}

func (e *egress) close() {
	e.srv.Close()
}

func (e *egress) pin(ctx context.Context, host string) ([]netip.Addr, error) {
	return webtool.PinHost(ctx, host, webtool.LookupFn(e.lookup))
}

func (e *egress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		e.tunnel(w, r)
		return
	}
	if r.URL.Host == "" || r.URL.Scheme != "http" {
		http.Error(w, "the browser's egress forwards proxied http and CONNECT only", http.StatusBadRequest)
		return
	}
	e.forward(w, r)
}

func (e *egress) refuse(w http.ResponseWriter, raw string, err error) {
	e.refused(raw, err.Error())
	http.Error(w, err.Error(), http.StatusForbidden)
}

func (e *egress) forward(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), guardTimeout)
	pins, err := e.pin(ctx, r.URL.Hostname())
	cancel()
	if err != nil {
		e.refuse(w, r.URL.String(), err)
		return
	}
	port := r.URL.Port()
	if port == "" {
		port = httpPort
	}
	host := r.Host
	transport := &http.Transport{}
	target := *r.URL
	if e.upstream != nil {
		transport.Proxy = http.ProxyURL(e.upstream)
		transport.DialContext = e.dial
		target.Host = net.JoinHostPort(pins[0].String(), port)
	} else {
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return e.connect(ctx, network, pins, port)
		}
	}
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = &target
			pr.Out.Host = host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, err.Error(), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

func (e *egress) tunnel(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, "CONNECT needs host:port", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), guardTimeout)
	defer cancel()
	pins, err := e.pin(ctx, host)
	if err != nil {
		e.refuse(w, r.Host, err)
		return
	}
	var server net.Conn
	if e.upstream != nil {
		server, err = e.viaUpstream(ctx, net.JoinHostPort(pins[0].String(), port))
	} else {
		server, err = e.connect(ctx, "tcp", pins, port)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		server.Close()
		http.Error(w, "the browser's egress cannot take the connection", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		server.Close()
		return
	}
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		client.Close()
		server.Close()
		return
	}
	splice(client, buf.Reader, server)
}

func (e *egress) connect(ctx context.Context, network string, pins []netip.Addr, port string) (net.Conn, error) {
	var last error
	for _, a := range pins {
		c, err := e.dial(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

func (e *egress) viaUpstream(ctx context.Context, addr string) (net.Conn, error) {
	c, err := e.dial(ctx, "tcp", e.upstream.Host)
	if err != nil {
		return nil, fmt.Errorf("the egress proxy %s is unreachable: %v", e.upstream.Host, err)
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: http.Header{}}
	if u := e.upstream.User; u != nil {
		pass, _ := u.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.Username()+":"+pass)))
	}
	if err := req.Write(c); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	res, err := http.ReadResponse(br, req)
	if err != nil {
		c.Close()
		return nil, err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		c.Close()
		return nil, fmt.Errorf("the egress proxy %s refused CONNECT %s: %s", e.upstream.Host, addr, res.Status)
	}
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: c, r: br}, nil
	}
	return c, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func splice(client net.Conn, pending *bufio.Reader, server net.Conn) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			client.Close()
			server.Close()
		})
	}
	go func() {
		defer closeBoth()
		if n := pending.Buffered(); n > 0 {
			head, _ := pending.Peek(n)
			if _, err := server.Write(head); err != nil {
				return
			}
		}
		io.Copy(server, client)
	}()
	go func() {
		defer closeBoth()
		io.Copy(client, server)
	}()
}
