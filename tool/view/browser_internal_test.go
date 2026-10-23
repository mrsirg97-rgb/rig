package view

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
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
