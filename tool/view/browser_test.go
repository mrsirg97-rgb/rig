package view_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/imagemarker"
	"github.com/mrsirg97-rgb/rig/v3/tool/view"
)

func requireBrowser(t *testing.T) string {
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

func loopbackLookup(ctx context.Context, host string) ([]string, error) {
	return []string{"93.184.216.34"}, nil
}

type pageServer struct {
	*httptest.Server
	mu   sync.Mutex
	form map[string]string
	seen int
}

func newPageServer(t *testing.T) *pageServer {
	t.Helper()
	s := &pageServer{form: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<!doctype html><html><head><title>Sign in</title></head><body>
<h1>Sign in</h1>
<form method="POST" action="/submit">
  <label for="email">Email</label>
  <input id="email" name="email" type="text">
  <label for="pw">Password</label>
  <input id="pw" name="pw" type="password">
  <button id="go" type="submit">Sign in</button>
</form>
</body></html>`)
	})
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.mu.Lock()
		s.form = map[string]string{"email": r.Form.Get("email"), "pw": r.Form.Get("pw")}
		s.seen++
		s.mu.Unlock()
		fmt.Fprint(w, `<!doctype html><html><head><title>Welcome</title></head><body>
<h1>Welcome</h1>
<p>signed in</p>
<button>Sign out</button>
</body></html>`)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *pageServer) submitted() (map[string]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.form {
		out[k] = v
	}
	return out, s.seen
}

func browserTool(t *testing.T, blobs string) view.View {
	t.Helper()
	bin := requireBrowser(t)
	return view.NewWith(view.Config{
		Blobs:   blobs,
		Browser: bin,
		Lookup:  loopbackLookup,
		Cwd:     t.TempDir(),
		RigHome: t.TempDir(),
	})
}

func stepArgs(t *testing.T, params map[string]any) []byte {
	t.Helper()
	params["action"] = "browser"
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func execStep(t *testing.T, tl view.View, params map[string]any) string {
	t.Helper()
	out, err := tl.Exec(context.Background(), stepArgs(t, params))
	t.Logf("browser(%v): %s (err=%v)", params, out, err)
	if err != nil {
		t.Fatalf("browser %v: %v", params, err)
	}
	return out
}

func lastMarker(t *testing.T, reply string) imagemarker.Ref {
	t.Helper()
	ref, ok := imagemarker.ParseLast(reply)
	if !ok {
		t.Fatalf("the reply carries no final marker line:\n%s", reply)
	}
	return ref
}

func TestABrowserOpensListsTypesSubmitsAndShoots(t *testing.T) {
	srv := newPageServer(t)
	blobs := realTemp(t)
	tl := browserTool(t, blobs)
	defer tl.Close()

	open := execStep(t, tl, map[string]any{"step": "open", "url": srv.URL})
	ref := lastMarker(t, open)
	if ref.Src != srv.URL+"/" {
		t.Fatalf("src = %q, want the page's URL %q", ref.Src, srv.URL+"/")
	}
	if ref.Mime != "image/png" {
		t.Fatalf("a screenshot is lossless: mime = %q", ref.Mime)
	}
	if !strings.Contains(open, `page `+srv.URL+`/ — Sign in`) {
		t.Fatalf("the reply names the page and its title:\n%s", open)
	}
	if !strings.Contains(open, `[1] textbox "Email"`) || !strings.Contains(open, `[3] button "Sign in"`) {
		t.Fatalf("the reply lists the page's interactive elements by ref:\n%s", open)
	}
	decodeBlob(t, blobs, ref)

	typed := execStep(t, tl, map[string]any{"step": "type", "ref": 1, "text": "ada@example.test"})
	if !strings.Contains(typed, `[1] textbox "Email"`) {
		t.Fatalf("type lists the page again:\n%s", typed)
	}
	if ref, ok := imagemarker.ParseLast(typed); ok {
		t.Fatalf("a step that is not asked for pixels returns none: %+v", ref)
	}

	clicked := execStep(t, tl, map[string]any{"step": "click", "ref": 3})
	if !strings.Contains(clicked, "Welcome") || !strings.Contains(clicked, `button "Sign out"`) {
		t.Fatalf("the click submitted the form and the reply lists the new page:\n%s", clicked)
	}
	form, seen := srv.submitted()
	if seen != 1 || form["email"] != "ada@example.test" {
		t.Fatalf("the form submit must land on the server with the typed value: %v (seen %d)", form, seen)
	}

	shot := execStep(t, tl, map[string]any{"step": "shot"})
	ref = lastMarker(t, shot)
	if ref.W > maxSide || ref.H > maxSide {
		t.Fatalf("the screenshot is %dx%d, over the bound", ref.W, ref.H)
	}
	decodeBlob(t, blobs, ref)

	closed := execStep(t, tl, map[string]any{"step": "close"})
	if !strings.Contains(closed, "browser closed") {
		t.Fatalf("close names what it did: %q", closed)
	}
	fresh := execStep(t, tl, map[string]any{"step": "shot"})
	if !strings.Contains(fresh, "about:blank") {
		t.Fatalf("a step after close starts a fresh page: %s", fresh)
	}
}

func TestABrowserKeepsItsPageAndRefsAcrossCalls(t *testing.T) {
	srv := newPageServer(t)
	tl := browserTool(t, realTemp(t))
	defer tl.Close()

	first := execStep(t, tl, map[string]any{"step": "open", "url": srv.URL})
	firstURL := lastMarker(t, first).Src
	execStep(t, tl, map[string]any{"step": "type", "ref": 1, "text": "one@example.test"})
	again := execStep(t, tl, map[string]any{"step": "shot"})
	if got := lastMarker(t, again).Src; got != firstURL {
		t.Fatalf("the page must stay where the open left it: %q vs %q", got, firstURL)
	}
	execStep(t, tl, map[string]any{"step": "click", "ref": 3})
	form, _ := srv.submitted()
	if form["email"] != "one@example.test" {
		t.Fatalf("the value typed in an earlier call rides the page: %v", form)
	}
}

func TestABrowserStepRefusesAStaleRef(t *testing.T) {
	srv := newPageServer(t)
	tl := browserTool(t, realTemp(t))
	defer tl.Close()
	execStep(t, tl, map[string]any{"step": "open", "url": srv.URL})
	_, err := tl.Exec(context.Background(), stepArgs(t, map[string]any{"step": "click", "ref": 99}))
	if err == nil || !strings.Contains(err.Error(), "ref 99") {
		t.Fatalf("a stale ref must refuse naming it: %v", err)
	}
}

func TestTheBrowserGuardsEverythingWebGuards(t *testing.T) {
	tl := view.NewWith(view.Config{Blobs: realTemp(t), Browser: requireBrowser(t), Cwd: realTemp(t), RigHome: realTemp(t)})
	defer tl.Close()
	for _, raw := range []string{
		"http://127.0.0.1:1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
	} {
		_, err := tl.Exec(context.Background(), stepArgs(t, map[string]any{"step": "open", "url": raw}))
		if err == nil || !strings.Contains(err.Error(), "private") {
			t.Fatalf("%s must be refused as private: %v", raw, err)
		}
	}
	for _, raw := range []string{"javascript:alert(1)", "data:text/html,hi", "about:config", "ftp://example.test/"} {
		_, err := tl.Exec(context.Background(), stepArgs(t, map[string]any{"step": "open", "url": raw}))
		if err == nil {
			t.Fatalf("%s must be refused", raw)
		}
	}
}

func TestTheBrowserRefusesAFileOutsideTheWorkspace(t *testing.T) {
	dir := realTemp(t)
	tl := view.NewWith(view.Config{Blobs: realTemp(t), Browser: requireBrowser(t), Cwd: dir, RigHome: realTemp(t)})
	defer tl.Close()
	_, err := tl.Exec(context.Background(), stepArgs(t, map[string]any{"step": "open", "url": "file:///etc/passwd"}))
	if err == nil || !strings.Contains(err.Error(), "outside the session's workspace") {
		t.Fatalf("file:// passes pathguard: %v", err)
	}
	inside := filepath.Join(dir, "page.html")
	if err := os.WriteFile(inside, []byte(`<!doctype html><title>local</title><button>Push</button>`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := tl.Exec(context.Background(), stepArgs(t, map[string]any{"step": "open", "url": "file://" + inside}))
	if err != nil {
		t.Fatalf("a file inside the workspace opens: %v", err)
	}
	if !strings.Contains(out, "local") || !strings.Contains(out, `button "Push"`) {
		t.Fatalf("the local page lists its elements:\n%s", out)
	}
}

func TestABrowserWithoutABinaryRefusesNamingTheKnob(t *testing.T) {
	tl := view.NewWith(view.Config{Blobs: realTemp(t)})
	defer tl.Close()
	_, err := tl.Exec(context.Background(), stepArgs(t, map[string]any{"step": "open", "url": "about:blank"}))
	if err == nil || !strings.Contains(err.Error(), "RIG_BROWSER") || !strings.Contains(err.Error(), "settings.browser") {
		t.Fatalf("an unconfigured browser names its knob: %v", err)
	}
}

func TestTheBrowserRefusesAnUnknownStep(t *testing.T) {
	tl := browserTool(t, realTemp(t))
	defer tl.Close()
	_, err := tl.Exec(context.Background(), stepArgs(t, map[string]any{"step": "hover"}))
	if err == nil || !strings.Contains(err.Error(), "open, click, type, scroll, shot, close") {
		t.Fatalf("an unknown step names the live set: %v", err)
	}
}

func TestBrowserStepsAreSerialWithinOneSession(t *testing.T) {
	srv := newPageServer(t)
	tl := browserTool(t, realTemp(t))
	defer tl.Close()
	execStep(t, tl, map[string]any{"step": "open", "url": srv.URL})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := tl.Exec(context.Background(), stepArgs(t, map[string]any{"step": "scroll", "amount": 10}))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a serial step failed: %v", err)
		}
	}
}
