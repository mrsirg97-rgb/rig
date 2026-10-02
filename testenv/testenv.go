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

// ClosedSwapURL is the loopback port nothing listens on: a spawned
// binary pointed here gets the gate's fail-closed wiring, whatever the
// host happens to run.
const ClosedSwapURL = "http://127.0.0.1:1"

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
	os.Setenv("RIG_SWAP_URL", ClosedSwapURL)
	wallCrontab(testHome)
	os.Setenv("GOPATH", gopath)
	os.Setenv("GOMODCACHE", gomod)
	os.Setenv("GOCACHE", gocache)
}

func wallCrontab(home string) {
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return
	}
	script := "#!/bin/sh\necho 'testenv: a test reached the operator crontab' >&2\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "crontab"), []byte(script), 0o755); err != nil {
		return
	}
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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
