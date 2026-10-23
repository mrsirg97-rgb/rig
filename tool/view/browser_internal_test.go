package view

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func browserBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("RIG_BROWSER"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		t.Skipf("RIG_BROWSER=%q is not an executable file", p)
		return ""
	}
	for _, name := range []string{"chrome-headless-shell", "chromium", "google-chrome", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("no headless chromium (RIG_BROWSER or chrome-headless-shell on PATH); the browser cases need one")
	return ""
}

func TestCDPCallsAreRoutedByIDOnANulDelimitedPipe(t *testing.T) {
	browserRead, clientWrite := io.Pipe()
	clientRead, browserWrite := io.Pipe()
	conn := newCDP(clientWrite, clientRead)
	defer conn.close()

	var mu sync.Mutex
	var seen []cdpMessage
	go func() {
		r := bufio.NewReader(browserRead)
		for {
			body, err := readNul(r, maxCDPMessage)
			if err != nil {
				return
			}
			var m cdpMessage
			if json.Unmarshal(body, &m) != nil {
				continue
			}
			mu.Lock()
			seen = append(seen, m)
			mu.Unlock()
			reply := cdpMessage{ID: m.ID, Result: json.RawMessage(fmt.Sprintf(`{"echo":%q}`, m.Method))}
			if m.Method == "fails" {
				reply = cdpMessage{ID: m.ID, Error: &cdpError{Code: 1, Message: "no such node"}}
			}
			line, _ := json.Marshal(reply)
			browserWrite.Write(append(line, 0))
		}
	}()

	ctx := context.Background()
	var wg sync.WaitGroup
	got := map[string]string{}
	var mu2 sync.Mutex
	for _, method := range []string{"a", "b", "c"} {
		method := method
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := conn.call(ctx, method, map[string]any{}, "")
			if err != nil {
				t.Errorf("%s: %v", method, err)
				return
			}
			var out struct {
				Echo string `json:"echo"`
			}
			if err := json.Unmarshal(res, &out); err != nil {
				t.Errorf("%s: %v", method, err)
				return
			}
			mu2.Lock()
			got[method] = out.Echo
			mu2.Unlock()
		}()
	}
	wg.Wait()
	for _, method := range []string{"a", "b", "c"} {
		if got[method] != method {
			t.Fatalf("call %q got %q: replies are routed by id, not arrival order", method, got[method])
		}
	}
	if _, err := conn.call(ctx, "fails", map[string]any{}, ""); err == nil || !strings.Contains(err.Error(), "no such node") {
		t.Fatalf("a protocol error surfaces as an error: %v", err)
	}
	mu.Lock()
	for _, m := range seen {
		if m.SessionID != "" {
			t.Fatalf("a flat session id rides every page command: %+v", m)
		}
	}
	mu.Unlock()
}

func TestCDPDeathFailsEveryPendingCall(t *testing.T) {
	browserRead, clientWrite := io.Pipe()
	clientRead, browserWrite := io.Pipe()
	conn := newCDP(clientWrite, clientRead)

	done := make(chan error, 1)
	go func() {
		_, err := conn.call(context.Background(), "slow", map[string]any{}, "")
		done <- err
	}()
	select {
	case <-time.After(50 * time.Millisecond):
	case err := <-done:
		t.Fatalf("the call answered before the browser died: %v", err)
	}
	browserRead.Close()
	browserWrite.Close()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("the dying call names the closed pipe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a dead browser must fail the call in flight")
	}
}

func TestAKilledBrowserIsNamedOnTheNextCallAndOnlyOnce(t *testing.T) {
	bin := browserBinary(t)
	tl := NewWith(Config{Blobs: t.TempDir(), Browser: bin, Cwd: t.TempDir(), RigHome: t.TempDir()}).(*toolView)
	defer tl.Close()

	if _, err := tl.Exec(context.Background(), []byte(`{"action":"browser","step":"open","url":"about:blank"}`)); err != nil {
		t.Fatalf("open: %v", err)
	}
	b := tl.browserFor()
	b.mu.Lock()
	p := b.p
	b.mu.Unlock()
	if p == nil {
		t.Fatal("the browser was not started")
	}
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.dead:
	case <-time.After(10 * time.Second):
		t.Fatal("the browser did not die")
	}

	out, err := tl.Exec(context.Background(), []byte(`{"action":"browser","step":"shot"}`))
	if err != nil {
		t.Fatalf("the next step starts a fresh browser: %v", err)
	}
	if !strings.Contains(out, "note: fresh browser; the previous browser exited (signal killed)") {
		t.Fatalf("the next call names the death:\n%s", out)
	}
	again, err := tl.Exec(context.Background(), []byte(`{"action":"browser","step":"shot"}`))
	if err != nil {
		t.Fatalf("second step: %v", err)
	}
	if strings.Contains(again, "note: fresh browser") {
		t.Fatalf("the note is one-shot:\n%s", again)
	}
}

func TestClosingTheToolKillsTheBrowserWithoutADeathNote(t *testing.T) {
	bin := browserBinary(t)
	tl := NewWith(Config{Blobs: t.TempDir(), Browser: bin, Cwd: t.TempDir(), RigHome: t.TempDir()}).(*toolView)
	if _, err := tl.Exec(context.Background(), []byte(`{"action":"browser","step":"open","url":"about:blank"}`)); err != nil {
		t.Fatalf("open: %v", err)
	}
	b := tl.browserFor()
	b.mu.Lock()
	p := b.p
	b.mu.Unlock()
	tl.Close()
	select {
	case <-p.dead:
	case <-time.After(10 * time.Second):
		t.Fatal("close must end the browser's process")
	}
	if b.current() != nil {
		t.Fatal("close must forget the process")
	}
	out, err := tl.Exec(context.Background(), []byte(`{"action":"browser","step":"shot"}`))
	if err != nil {
		t.Fatalf("a step after close starts fresh: %v", err)
	}
	if strings.Contains(out, "note: fresh browser") {
		t.Fatalf("a deliberate close is not a death:\n%s", out)
	}
}

func (b *browser) current() *bProc {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.p
}

func TestTheBrowserDiesWithItsParent(t *testing.T) {
	bin := browserBinary(t)
	helper := exec.Command(os.Args[0], "-test.run", "^TestBrowserParentHelper$")
	helper.Env = append(os.Environ(), "RIG_BROWSER="+bin, "RIG_BROWSER_HELPER=1")
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if helper.Process != nil {
			helper.Process.Kill()
		}
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("the helper printed no browser pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "pid ")))
	if err != nil || pid <= 0 {
		t.Fatalf("the helper's line = %q, want \"pid <n>\"", line)
	}
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	helper.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the browser (pid %d) outlived the rig process that started it", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestBrowserParentHelper(t *testing.T) {
	if os.Getenv("RIG_BROWSER_HELPER") != "1" {
		t.Skip("the parent-death helper")
	}
	tl := NewWith(Config{Blobs: t.TempDir(), Browser: os.Getenv("RIG_BROWSER"), Cwd: t.TempDir(), RigHome: t.TempDir()}).(*toolView)
	if _, err := tl.Exec(context.Background(), []byte(`{"action":"browser","step":"shot"}`)); err != nil {
		t.Fatalf("the helper's browser: %v", err)
	}
	p := tl.browserFor().current()
	fmt.Printf("pid %d\n", p.cmd.Process.Pid)
	time.Sleep(60 * time.Second)
}

func TestTheBrowserReachesTheNetworkOnlyThroughItsEgress(t *testing.T) {
	args := strings.Join((&browser{cfg: Config{Proxy: "http://127.0.0.1:8889"}}).argv("profile", "http://127.0.0.1:4321"), " ")
	for _, want := range []string{
		"--proxy-server=http://127.0.0.1:4321",
		"--proxy-bypass-list=<-loopback>",
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("the flags must carry %s: %s", want, args)
		}
	}
	if strings.Contains(args, "8889") {
		t.Fatalf("the configured proxy is the egress's upstream, never the browser's: %s", args)
	}
}

func TestTheEgressDialsThePinAndKeepsTheName(t *testing.T) {
	var host string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host = r.Host
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	var dialed string
	var refused []string
	e, err := newEgress(Config{
		Lookup: func(ctx context.Context, h string) ([]string, error) {
			if h == "inside.test" {
				return []string{"10.0.0.7"}, nil
			}
			return []string{"93.184.216.34"}, nil
		},
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialed = addr
			var d net.Dialer
			return d.DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}, func(raw, why string) { refused = append(refused, raw+": "+why) })
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	proxy, _ := url.Parse(e.url())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	res, err := client.Get("http://site.test:" + port + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if dialed != "93.184.216.34:"+port || host != "site.test:"+port {
		t.Fatalf("dialed %q with Host %q; want the pin, with the name kept", dialed, host)
	}
	dialed = ""
	res, err = client.Get("http://inside.test:" + port + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || dialed != "" || len(refused) != 1 || !strings.Contains(refused[0], "private address 10.0.0.7") {
		t.Fatalf("a private answer refuses before any dial: status %d, dialed %q, refused %v", res.StatusCode, dialed, refused)
	}
}

func TestTheEgressTunnelsThroughTheUpstreamByThePin(t *testing.T) {
	asked := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked <- r.Method + " " + r.Host
		w.WriteHeader(http.StatusForbidden)
	}))
	defer upstream.Close()
	e, err := newEgress(Config{
		Proxy:  upstream.URL,
		Lookup: func(ctx context.Context, h string) ([]string, error) { return []string{"93.184.216.34"}, nil },
	}, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	proxy, _ := url.Parse(e.url())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	if _, err := client.Get("https://site.test/"); err == nil {
		t.Fatal("the upstream refused the tunnel, so the fetch must fail")
	}
	if got := <-asked; got != "CONNECT 93.184.216.34:443" {
		t.Fatalf("the upstream was asked %q; it must be handed the pin, never the name it would resolve again", got)
	}
}
