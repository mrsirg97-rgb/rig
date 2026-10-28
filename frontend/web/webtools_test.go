package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/webtools"
)

type fakeWebTools struct {
	status   webtools.Status
	statusEr error
	genOut   string
	genPw    string
	genForce bool
	genErr   error
	startOut string
	startEr  error
	stopOut  string
	stopEr   error
	calls    []string
}

func (f *fakeWebTools) Dir() string { return "/home/u/.rig/external" }

func (f *fakeWebTools) Status(context.Context) (webtools.Status, error) {
	f.calls = append(f.calls, "status")
	return f.status, f.statusEr
}

func (f *fakeWebTools) Generate(_ context.Context, pw string, force bool) (string, error) {
	f.calls = append(f.calls, "generate")
	f.genPw, f.genForce = pw, force
	return f.genOut, f.genErr
}

func (f *fakeWebTools) Start(context.Context) (string, error) {
	f.calls = append(f.calls, "start")
	return f.startOut, f.startEr
}

func (f *fakeWebTools) Stop(context.Context) (string, error) {
	f.calls = append(f.calls, "stop")
	return f.stopOut, f.stopEr
}

func toolsServer(t *testing.T, tools webtools.Tools) *httptest.Server {
	t.Helper()
	srv, err := build(t.TempDir(), WithTools(tools))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := httptest.NewServer(srv.router())
	t.Cleanup(s.Close)
	return s
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("%s is not JSON: %v", res.Status, err)
	}
	return res.StatusCode, out
}

func postJSON(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("%s is not JSON: %v", res.Status, err)
	}
	return res.StatusCode, out
}

func TestTheDashboardReportsTheWebToolsAsRead(t *testing.T) {
	f := &fakeWebTools{status: webtools.Status{
		Dir:      "/home/u/.rig/external",
		Docker:   "ok 29.9.2",
		Missing:  []string{},
		Differ:   []string{},
		Proxy:    "http://rig@127.0.0.1:8889",
		Services: []string{"tinyproxy", "valkey"},
	}}
	code, out := getJSON(t, toolsServer(t, f).URL+"/api/webtools")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	status, ok := out["status"].(map[string]any)
	if !ok {
		t.Fatalf("reply = %v, want a status object", out)
	}
	for _, want := range []string{"dir", "docker", "proxy", "services", "lines"} {
		if _, ok := status[want]; !ok {
			t.Fatalf("status = %v, want %s", status, want)
		}
	}
	if status["docker"] != "ok 29.9.2" {
		t.Fatalf("docker = %v", status["docker"])
	}
	text, _ := json.Marshal(out)
	if strings.Contains(string(text), "hunter2") {
		t.Fatalf("the reply carries a password: %s", text)
	}
	lines := strings.Join(toAnyStringList(status["lines"]), "\n")
	for _, want := range []string{"/home/u/.rig/external", "tinyproxy", "http://rig@127.0.0.1:8889"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("lines = %q, want %s", lines, want)
		}
	}
}

func toAnyStringList(v any) []string {
	list, _ := v.([]any)
	out := []string{}
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestTheDashboardRunsTheVerbsAndPassesThePassword(t *testing.T) {
	f := &fakeWebTools{genOut: "wrote 7 of 7 files", startOut: "started", stopOut: "stopped"}
	srv := toolsServer(t, f)
	code, out := postJSON(t, srv.URL+"/api/webtools/generate", `{"password":"hunter2longenough","force":true}`)
	if code != http.StatusOK || out["out"] != "wrote 7 of 7 files" {
		t.Fatalf("generate = %d %v", code, out)
	}
	if f.genPw != "hunter2longenough" || !f.genForce {
		t.Fatalf("generate saw %q force=%v", f.genPw, f.genForce)
	}
	if _, out := postJSON(t, srv.URL+"/api/webtools/start", `{}`); out["out"] != "started" {
		t.Fatalf("start = %v", out)
	}
	if _, out := postJSON(t, srv.URL+"/api/webtools/stop", ``); out["out"] != "stopped" {
		t.Fatalf("stop = %v", out)
	}
	if strings.Join(f.calls, ",") != "generate,start,stop" {
		t.Fatalf("calls = %v, want the three verbs and no status read", f.calls)
	}
}

func TestTheDashboardRefusesABodyThatIsNotJSON(t *testing.T) {
	f := &fakeWebTools{}
	code, _ := postJSON(t, toolsServer(t, f).URL+"/api/webtools/generate", `{"password":`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 with no generate run: calls = %v", code, f.calls)
	}
	if len(f.calls) != 0 {
		t.Fatalf("a refused body still ran the verb: %v", f.calls)
	}
}

func TestTheDashboardNamesAnUnknownVerbAndTheGETOnlyPath(t *testing.T) {
	f := &fakeWebTools{}
	srv := toolsServer(t, f)
	code, _ := postJSON(t, srv.URL+"/api/webtools/remove", `{}`)
	if code != http.StatusNotFound {
		t.Fatalf("remove = %d, want 404 naming itself as not a verb", code)
	}
	res, err := http.Post(srv.URL+"/api/webtools", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("/api/webtools POST = %d, want 405 (it is a read)", res.StatusCode)
	}
	if res.Header.Get("Allow") != "GET" {
		t.Fatalf("Allow = %q, want GET", res.Header.Get("Allow"))
	}
}

func TestTheDashboardRefusesWithoutTheToolsWired(t *testing.T) {
	srv, err := build(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(srv.router())
	defer s.Close()
	code, _ := getJSON(t, s.URL+"/api/webtools")
	if code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", code)
	}
	res, err := http.Post(s.URL+"/api/webtools/start", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("start = %d, want 501", res.StatusCode)
	}
}

func TestThePageCarriesTheWebToolsPanel(t *testing.T) {
	srv := toolsServer(t, &fakeWebTools{})
	res, err := http.Get(srv.URL + "/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, want := range []string{"/api/webtools", "renderWebTools", "webtools setup", "fetch egress"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the page lacks %q", want)
		}
	}
}
