package testenv

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

var OperatorHome = os.Getenv("HOME")

const ClosedSwapURL = "http://127.0.0.1:1"

var (
	mu    sync.Mutex
	hosts = map[string]bool{}
)

var testHome string

func Main(m *testing.M) {
	if err := isolate(); err != nil {
		if testHome != "" {
			os.RemoveAll(testHome)
		}
		fmt.Fprintf(os.Stderr, "testenv: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if testHome != "" {
		os.RemoveAll(testHome)
	}
	os.Exit(code)
}

func isolate() error {
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
	home, err := mkTestHome("")
	if err != nil {
		return err
	}
	if err := SeedKernel(home); err != nil {
		os.RemoveAll(home)
		return err
	}
	testHome = home
	os.Setenv("HOME", testHome)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(testHome, ".config"))
	os.Setenv("RIG_HOME", "")
	os.Setenv("RIG_SWAP_URL", ClosedSwapURL)
	os.Unsetenv(sched.DelegateEnv)
	os.Unsetenv(sched.FleetEnv)
	if err := wallCrontab(testHome); err != nil {
		return err
	}
	os.Setenv("GOPATH", gopath)
	os.Setenv("GOMODCACHE", gomod)
	os.Setenv("GOCACHE", gocache)
	return nil
}

func mkTestHome(dir string) (string, error) {
	home, err := os.MkdirTemp(dir, "rig-test-home-*")
	if err != nil {
		return "", fmt.Errorf("build the test home: %v", err)
	}
	return home, nil
}

func SeedKernel(home string) error {
	venv := filepath.Join(OperatorHome, ".pi", "agent", "kernel-venv")
	_, err := os.Stat(venv)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("seed the kernel venv: %v", err)
	}
	agent := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		return fmt.Errorf("seed the kernel venv: mkdir %s: %v", agent, err)
	}
	if err := os.Symlink(venv, filepath.Join(agent, "kernel-venv")); err != nil {
		return fmt.Errorf("seed the kernel venv: %v", err)
	}
	return nil
}

func wallCrontab(home string) error {
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return fmt.Errorf("crontab shim: mkdir %s: %v", bin, err)
	}
	path := filepath.Join(bin, "crontab")
	script := "#!/bin/sh\necho 'testenv: a test reached the operator crontab' >&2\nexit 3\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return fmt.Errorf("crontab shim: write %s: %v", path, err)
	}
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return nil
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
