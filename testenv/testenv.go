package testenv

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var OperatorHome = os.Getenv("HOME")

var (
	mu    sync.Mutex
	hosts = map[string]bool{}
)

var testHome string

func Main(m *testing.M) {
	isolate()
	code := m.Run()
	if testHome != "" {
		os.RemoveAll(testHome)
	}
	os.Exit(code)
}

func isolate() {
	realHome := os.Getenv("HOME")
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		gopath = filepath.Join(realHome, "go")
	}
	gomod := os.Getenv("GOMODCACHE")
	if gomod == "" {
		gomod = filepath.Join(strings.Split(gopath, string(os.PathListSeparator))[0], "pkg", "mod")
	}
	gocache := os.Getenv("GOCACHE")
	if gocache == "" {
		gocache = filepath.Join(realHome, ".cache", "go-build")
	}
	testHome, _ = os.MkdirTemp("", "rig-test-home-*")
	os.Setenv("HOME", testHome)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(testHome, ".config"))
	os.Setenv("RIG_HOME", "")
	os.Setenv("GOPATH", gopath)
	os.Setenv("GOMODCACHE", gomod)
	os.Setenv("GOCACHE", gocache)
}

type transport struct{}

func (transport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	mu.Lock()
	ok := hosts[host]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("testenv: dial refused: %s is not an httptest server", host)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func Transport() http.RoundTripper {
	return transport{}
}

func Server(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	u, err := url.Parse(srv.URL)
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	mu.Lock()
	hosts[u.Host] = true
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		delete(hosts, u.Host)
		mu.Unlock()
		srv.Close()
	})
	return srv
}
