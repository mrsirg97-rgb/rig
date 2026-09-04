package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/config"
	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/store"
	remstore "github.com/mrsirg97-rgb/rig/store/rem"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/store/scope"
	"github.com/mrsirg97-rgb/rig/store/state"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

const testCWD = "/workspace/alpha"

type fakeCrontab struct{ text string }

func (f *fakeCrontab) List() (string, error) { return f.text, nil }
func (f *fakeCrontab) Install(text string) error {
	f.text = text
	return nil
}

func seedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	ctx := context.Background()

	spath := state.StorePath(home, testCWD)
	if err := os.MkdirAll(filepath.Dir(spath), 0o755); err != nil {
		t.Fatal(err)
	}
	sdb, _, _, err := store.Open(spath, state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordSession(ctx, sdb, "sess1", testCWD, "model-x", "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, sdb, "sess1", "user", "do the thing", nil, nil); err != nil {
		t.Fatal(err)
	}
	reasoning := "weighing the options"
	seq, err := state.RecordMessage(ctx, sdb, "sess1", "assistant", "done", &reasoning, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolCall(ctx, sdb, "sess1", seq, "call_1", "bash", `{"cmd":"ls"}`); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolResult(ctx, sdb, "sess1", seq, "call_1", "file1\nfile2", nil); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordUsage(ctx, sdb, seq, 100, 42, 10, 5); err != nil {
		t.Fatal(err)
	}
	if err := state.CloseSession(ctx, sdb, "sess1", "ok"); err != nil {
		t.Fatal(err)
	}
	if err := sdb.DB.Close(); err != nil {
		t.Fatal(err)
	}

	tpath := todostore.FilePath(home)
	if err := os.MkdirAll(filepath.Dir(tpath), 0o755); err != nil {
		t.Fatal(err)
	}
	tdb, _, _, err := store.Open(tpath, todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Create(ctx, tdb, todostore.Project{Key: scope.Key(testCWD), Label: scope.Label(testCWD)}, []todostore.CreateItem{{Text: "seeded task"}}, "seed"); err != nil {
		t.Fatal(err)
	}
	if err := tdb.DB.Close(); err != nil {
		t.Fatal(err)
	}

	rpath := remstore.FilePath(home)
	if err := os.MkdirAll(filepath.Dir(rpath), 0o755); err != nil {
		t.Fatal(err)
	}
	rdb, _, _, err := store.Open(rpath, remstore.Statements(), remstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := remstore.Learn(ctx, rdb, testCWD, remstore.LearnInput{Content: "remembered fact"}); err != nil {
		t.Fatal(err)
	}
	if err := rdb.DB.Close(); err != nil {
		t.Fatal(err)
	}

	shome := filepath.Join(home, "scheduler")
	if err := os.MkdirAll(shome, 0o755); err != nil {
		t.Fatal(err)
	}
	scdb, _, _, err := store.Open(filepath.Join(shome, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Create(ctx, scdb, &fakeCrontab{},
		sched.CreateInput{Name: "digest", Prompt: "the digest", Cron: "30 7 * * *", Cwd: testCWD, Model: "worker-test"},
		testCWD, "seed", "rig run-job", time.Now); err != nil {
		t.Fatal(err)
	}
	if err := scdb.DB.Close(); err != nil {
		t.Fatal(err)
	}

	pdir := filepath.Join(home, "plugins")
	if err := os.MkdirAll(filepath.Join(pdir, "pending"), 0o755); err != nil {
		t.Fatal(err)
	}
	pluginFile := "NAME = \"x\"\nDESCRIPTION = \"the %s plugin\"\n\ndef run(args):\n    return \"ok\"\n"
	if err := os.WriteFile(filepath.Join(pdir, "loaded_one.py"), []byte(strings.ReplaceAll(pluginFile, "%s", "loaded")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "pending", "pending_one.py"), []byte(strings.ReplaceAll(pluginFile, "%s", "pending")), 0o644); err != nil {
		t.Fatal(err)
	}

	return home
}

func modelsTable(t *testing.T) models.Table {
	t.Helper()
	table, err := models.New(
		models.Model{ID: "gpt-test", Window: 128000, MaxTokens: 8192, Reserve: 16000, KeepRecent: 32000, Role: models.RoleInteractive, Effort: "high", Efforts: []string{"low", "medium", "high"}},
		models.Model{ID: "worker-test", Window: 65536, MaxTokens: 4096, Reserve: 8000, KeepRecent: 16000, Role: models.RoleWorker, Effort: "medium", Efforts: []string{"low", "medium", "high"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	home := seedHome(t)
	srv, err := New(Options{Home: home, CWD: testCWD, Models: modelsTable(t), Workers: &config.Workers{Model: "worker-test", Slots: 1}, Crontab: &fakeCrontab{}, Natives: []string{"bash", "read"}, Root: home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	tok, _, err := srv.Token()
	if err != nil {
		t.Fatal(err)
	}
	srv.origins = []string{"http://127.0.0.1:7777"}
	return srv, tok
}

func doReq(t *testing.T, h http.Handler, method, target string, body io.Reader, hdr http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	for k, vs := range hdr {
		req.Header.Set(k, vs[0])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func bearer(tok string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+tok)
	return h
}

func TestTokenGate(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	home := srv.home

	rec := doReq(t, h, "GET", "/", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential: got %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("no credential: missing WWW-Authenticate: Bearer, got %q", rec.Header().Get("WWW-Authenticate"))
	}

	rec = doReq(t, h, "GET", "/", nil, bearer("wrong"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: got %d, want 401", rec.Code)
	}

	rec = doReq(t, h, "GET", "/", nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer: got %d, want 200", rec.Code)
	}

	rec = doReq(t, h, "GET", "/?token="+tok, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("?token=: got %d, want 200", rec.Code)
	}
	cookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, cookieName+"="+tok) ||
		!strings.Contains(cookie, "HttpOnly") ||
		!strings.Contains(cookie, "SameSite=Strict") {
		t.Fatalf("?token=: bad Set-Cookie: %q", cookie)
	}
	ch := http.Header{}
	ch.Set("Cookie", cookieName+"="+tok)
	rec = doReq(t, h, "GET", "/", nil, ch)
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie: got %d, want 200", rec.Code)
	}

	fi, err := os.Stat(tokenPath(home))
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file: perm %v, want 0600", perm)
	}
	tok2, minted2, err := EnsureToken(home)
	if err != nil {
		t.Fatal(err)
	}
	if minted2 {
		t.Fatal("EnsureToken: re-minted on a second read (should read the existing)")
	}
	if tok2 != tok {
		t.Fatalf("EnsureToken: second read differs (%q vs %q)", tok2, tok)
	}
}

func TestLoopbackRefusal(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:7777", "[::1]:7777", "localhost:7777"} {
		if err := Loopback(ok); err != nil {
			t.Errorf("Loopback(%q): unexpected refusal: %v", ok, err)
		}
	}
	for _, bad := range []string{"0.0.0.0:7777", "192.168.1.5:7777", "10.0.0.2:7777"} {
		if err := Loopback(bad); err == nil {
			t.Errorf("Loopback(%q): accepted, want a refusal", bad)
		}
	}
}

func TestAllowList404And405(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()

	rec := doReq(t, h, "GET", "/api/nonexistent", nil, bearer(tok))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path: got %d, want 404", rec.Code)
	}

	rec = doReq(t, h, "POST", "/api/models", nil, bearer(tok))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method: got %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") {
		t.Fatalf("wrong method: Allow %q, want GET in it", allow)
	}

	rec = doReq(t, h, "DELETE", "/api/todo", nil, bearer(tok))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("todo DELETE: got %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
		t.Fatalf("todo DELETE: Allow %q, want GET and POST", allow)
	}
}

func TestTodoCreateWalls(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	q := "?cwd=" + testCWD

	rec := doReq(t, h, "POST", "/api/todo"+q, strings.NewReader("a task\n"), bearer(tok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no origin: got %d, want 403", rec.Code)
	}

	foreign := http.Header{"Origin": {"http://evil.example"}, "Authorization": {"Bearer " + tok}}
	rec = doReq(t, h, "POST", "/api/todo"+q, strings.NewReader("a task\n"), foreign)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: got %d, want 403", rec.Code)
	}

	rec = doReq(t, h, "POST", "/api/todo"+q, strings.NewReader("   \n"), both(bearer(tok), "Origin", "http://127.0.0.1:7777"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: got %d, want 400", rec.Code)
	}

	big := strings.Repeat("x", maxWriteBytes+1)
	rec = doReq(t, h, "POST", "/api/todo"+q, strings.NewReader(big), both(bearer(tok), "Origin", "http://127.0.0.1:7777"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-cap: got %d, want 400", rec.Code)
	}

	rec = doReq(t, h, "POST", "/api/todo"+q, strings.NewReader("alpha\n\nbeta\n"), both(bearer(tok), "Origin", "http://127.0.0.1:7777"))
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	reply, _ := created["reply"].(string)
	if !strings.Contains(reply, "alpha") || !strings.Contains(reply, "beta") {
		t.Fatalf("create: reply %q, want the created tasks", reply)
	}

	rec = doReq(t, h, "GET", "/api/todo"+q, nil, bearer(tok))
	var after map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after["text"], "alpha") || !strings.Contains(after["text"], "beta") {
		t.Fatalf("queue after create: %q, want alpha and beta", after["text"])
	}
	if !strings.Contains(after["text"], "seeded task") {
		t.Fatalf("queue after create lost the seeded task (Create is an upsert, not a wipe): %q", after["text"])
	}
}

func both(base http.Header, k, v string) http.Header {
	base.Set(k, v)
	return base
}

func TestCwds(t *testing.T) {
	srv, tok := newTestServer(t)
	rec := doReq(t, srv.Handler(), "GET", "/api/cwds", nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("cwds: got %d", rec.Code)
	}
	var body map[string][]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range body["cwds"] {
		if c == testCWD {
			found = true
		}
	}
	if !found {
		t.Fatalf("cwds %v, want it to contain %s", body["cwds"], testCWD)
	}
}

func TestSessionsList(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()

	rec := doReq(t, h, "GET", "/api/sessions?cwd="+testCWD, nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions: got %d (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Cwd      string `json:"cwd"`
		Sessions []struct {
			ID    string `json:"id"`
			Cwd   string `json:"cwd"`
			Exit  string `json:"exit"`
			Turns int    `json:"turns"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Cwd != testCWD {
		t.Fatalf("cwd %q, want %q", body.Cwd, testCWD)
	}
	if len(body.Sessions) != 1 {
		t.Fatalf("sessions: got %d, want 1", len(body.Sessions))
	}
	s := body.Sessions[0]
	if s.ID != "sess1" || s.Cwd != testCWD || s.Exit != "ok" {
		t.Fatalf("session row %+v, want id sess1, cwd %s, exit ok", s, testCWD)
	}

	rec = doReq(t, h, "GET", "/api/sessions?cwd=/nope/nowhere", nil, bearer(tok))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("absent workspace: got %d, want 404", rec.Code)
	}
}

func TestTranscriptGolden(t *testing.T) {
	srv, tok := newTestServer(t)
	rec := doReq(t, srv.Handler(), "GET", "/api/sessions/sess1/transcript?cwd="+testCWD, nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("transcript: got %d (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		ID       string `json:"id"`
		Total    int    `json:"total"`
		HasMore  bool   `json:"has_more"`
		Messages []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			Reasoning string `json:"reasoning"`
			ToolCalls []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Args string `json:"args"`
			} `json:"tool_calls"`
			ToolID string `json:"tool_id"`
		} `json:"messages"`
		Usage []struct {
			Seq        int64 `json:"seq"`
			Prompt     int64 `json:"prompt"`
			Completion int64 `json:"completion"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "sess1" {
		t.Fatalf("id %q, want sess1", body.ID)
	}
	if body.HasMore {
		t.Fatalf("has_more true for a small transcript")
	}

	roles := make([]string, 0, len(body.Messages))
	for _, m := range body.Messages {
		roles = append(roles, m.Role)
	}
	if len(body.Messages) < 3 {
		t.Fatalf("transcript: %d messages, want >= 3 (got %v)", len(body.Messages), roles)
	}
	var sawReasoning, sawToolCall, sawToolResult bool
	for _, m := range body.Messages {
		if m.Role == "assistant" && m.Reasoning == "weighing the options" {
			sawReasoning = true
		}
		for _, tc := range m.ToolCalls {
			if tc.Name == "bash" && tc.Args == `{"cmd":"ls"}` {
				sawToolCall = true
			}
		}
		if m.Role == "tool" && strings.Contains(m.Content, "file1") {
			sawToolResult = true
		}
	}
	if !sawReasoning {
		t.Fatal("transcript: no assistant reasoning rendered")
	}
	if !sawToolCall {
		t.Fatal("transcript: no tool call rendered")
	}
	if !sawToolResult {
		t.Fatal("transcript: no tool result rendered")
	}

	if len(body.Usage) != 1 {
		t.Fatalf("usage: %d rows, want 1", len(body.Usage))
	}
	if body.Usage[0].Prompt != 100 || body.Usage[0].Completion != 42 {
		t.Fatalf("usage row %+v, want prompt 100 completion 42", body.Usage[0])
	}

	rec = doReq(t, srv.Handler(), "GET", "/api/sessions/nope/transcript?cwd="+testCWD, nil, bearer(tok))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown session: got %d, want 404", rec.Code)
	}
}

func TestTodoReadVerbatim(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	rec := doReq(t, h, "GET", "/api/todo?cwd="+testCWD, nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("todo read: got %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["text"], "seeded task") {
		t.Fatalf("todo read: %q, want the seeded task", body["text"])
	}

	rec = doReq(t, h, "GET", "/api/todo?cwd="+testCWD+"&all=true", nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("todo ReadAll: got %d", rec.Code)
	}
	var body2 map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body2); err != nil {
		t.Fatal(err)
	}
	if body2["text"] == "" {
		t.Fatal("todo ReadAll: empty text")
	}
}

func TestSchedulerVerbatim(t *testing.T) {
	srv, tok := newTestServer(t)
	rec := doReq(t, srv.Handler(), "GET", "/api/scheduler?cwd="+testCWD, nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("scheduler: got %d (body %s)", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["text"], "digest") {
		t.Fatalf("scheduler: %q, want the seeded job", body["text"])
	}
}

func TestSchedulerCreate(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	q := "?cwd=" + testCWD

	post := func(body string) *httptest.ResponseRecorder {
		hdr := both(bearer(tok), "Origin", "http://127.0.0.1:7777")
		hdr.Set("Content-Type", "application/json")
		return doReq(t, h, "POST", "/api/scheduler"+q, strings.NewReader(body), hdr)
	}

	rec := post(`{"name":"nightly","prompt":"do the nightly","cron":"0 3 * * *"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got %d (body %s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	reply, _ := created["reply"].(string)
	if !strings.Contains(reply, "nightly") || !strings.Contains(reply, testCWD) {
		t.Fatalf("create: reply %q, want the job named with its cwd", reply)
	}
	rec = doReq(t, h, "GET", "/api/scheduler"+q, nil, bearer(tok))
	var after map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after["text"], "nightly") {
		t.Fatalf("list after create: %q, want the new job", after["text"])
	}

	rec = post(`{"name":"oncejob","prompt":"once","cron":"once","at":"2026-01-02T03:04:05Z"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("once create: got %d (body %s)", rec.Code, rec.Body.String())
	}
	if r := post(`{"name":"oncejob","prompt":"twice","cron":"0 4 * * *"}`); r.Code == http.StatusOK {
		t.Fatal("a second create with the same name: want the duplicate refusal")
	}

	rec = post(`{"name":"nightly","prompt":"again","cron":"0 5 * * *"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate: got %d, want 400", rec.Code)
	}
	var dup map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &dup); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dup["error"], "already exists") {
		t.Fatalf("duplicate: %q, want the named refusal", dup["error"])
	}

	rec = post(`{"name":"badopt","prompt":"p","cron":"bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cron: got %d, want 400", rec.Code)
	}

	rec = post(`{"name":"noat","prompt":"p","cron":"once"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("once without at: got %d, want 400", rec.Code)
	}

	rec = post(`{"prompt":"p","cron":"0 6 * * *"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name: got %d, want 400", rec.Code)
	}
	rec = post(`{"name":"noprompt","cron":"0 7 * * *"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty prompt: got %d, want 400", rec.Code)
	}

	rec = post(`{"name":"gjob","prompt":"p","cron":"0 8 * * *"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got %d (body %s)", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, "GET", "/api/scheduler"+q, nil, bearer(tok))
	var gbody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &gbody); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gbody["text"], "gjob") {
		t.Fatalf("job missing from the list: %q", gbody["text"])
	}

	rec = doReq(t, h, "POST", "/api/scheduler"+q, strings.NewReader(`{"name":"x","prompt":"p","cron":"0 9 * * *"}`), bearer(tok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no origin: got %d, want 403", rec.Code)
	}
	foreign := http.Header{"Origin": {"http://evil.example"}, "Authorization": {"Bearer " + tok}}
	rec = doReq(t, h, "POST", "/api/scheduler"+q, strings.NewReader(`{"name":"x","prompt":"p","cron":"0 9 * * *"}`), foreign)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: got %d, want 403", rec.Code)
	}
	big := strings.Repeat("x", maxWriteBytes+1)
	rec = doReq(t, h, "POST", "/api/scheduler"+q, strings.NewReader(big), both(bearer(tok), "Origin", "http://127.0.0.1:7777"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-cap: got %d, want 400", rec.Code)
	}

	rec = doReq(t, h, "DELETE", "/api/scheduler"+q, nil, bearer(tok))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE: got %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
		t.Fatalf("DELETE: Allow %q, want GET and POST", allow)
	}
}

func TestSchedulerNoFleetRefusesCreateAndNamesTheFile(t *testing.T) {
	home := seedHome(t)
	srv, err := New(Options{Home: home, CWD: testCWD, Models: modelsTable(t), Crontab: &fakeCrontab{}, Natives: []string{"bash", "read"}, Root: home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	tok, _, err := srv.Token()
	if err != nil {
		t.Fatal(err)
	}
	srv.origins = []string{"http://127.0.0.1:7777"}
	h := srv.Handler()
	q := "?cwd=" + testCWD

	rec := doReq(t, h, "GET", "/api/scheduler"+q, nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: got %d (body %s)", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["worker"] != "" {
		t.Fatalf("no fleet: the worker field = %q, want empty (the view renders the refusal)", got["worker"])
	}

	hdr := both(bearer(tok), "Origin", "http://127.0.0.1:7777")
	hdr.Set("Content-Type", "application/json")
	rec = doReq(t, h, "POST", "/api/scheduler"+q, strings.NewReader(`{"name":"x","prompt":"p","cron":"0 9 * * *"}`), hdr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no-fleet create: got %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	var errBody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatal(err)
	}
	want := "scheduler: no workers configured (" + filepath.Join(home, "workers.json") + " names the model)"
	if errBody["error"] != want {
		t.Fatalf("the voice = %q, want %q", errBody["error"], want)
	}
}

func TestSchedulerCreateDefaultsToTheFleetModel(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	q := "?cwd=" + testCWD

	rec := doReq(t, h, "GET", "/api/scheduler"+q, nil, bearer(tok))
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["worker"] != "worker-test" {
		t.Fatalf("the GET reply carries the fleet's model: worker = %q, want worker-test", got["worker"])
	}

	hdr := both(bearer(tok), "Origin", "http://127.0.0.1:7777")
	hdr.Set("Content-Type", "application/json")
	rec = doReq(t, h, "POST", "/api/scheduler"+q, strings.NewReader(`{"name":"fleetdefault","prompt":"p","cron":"0 9 * * *"}`), hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("fleet create: got %d (body %s)", rec.Code, rec.Body.String())
	}
	sdb, err := srv.stores.scheduler()
	if err != nil {
		t.Fatal(err)
	}
	var m string
	if err := sdb.DB.QueryRow(`SELECT model FROM jobs WHERE name = 'fleetdefault'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if m != "worker-test" {
		t.Fatalf("the job's model = %q, want the fleet's model worker-test", m)
	}
}

func TestSchedulerDoors(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	q := "?cwd=" + testCWD
	hdr := func() http.Header {
		x := both(bearer(tok), "Origin", "http://127.0.0.1:7777")
		x.Set("Content-Type", "application/json")
		return x
	}
	list := func() string {
		rec := doReq(t, h, "GET", "/api/scheduler"+q, nil, bearer(tok))
		if rec.Code != http.StatusOK {
			t.Fatalf("list: got %d", rec.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body["text"]
	}
	verb := func(target, id string) *httptest.ResponseRecorder {
		return doReq(t, h, "POST", target+q, strings.NewReader(`{"id":"`+id+`"}`), hdr())
	}
	say := func(rec *httptest.ResponseRecorder) string {
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if r, ok := body["reply"].(string); ok {
			return r
		}
		if e, ok := body["error"].(string); ok {
			return e
		}
		return rec.Body.String()
	}

	rec := verb("/api/scheduler/pause", "j99")
	if rec.Code != http.StatusBadRequest || !strings.Contains(say(rec), "no job 'j99'") {
		t.Fatalf("pause of an unknown id: got %d %q, want 400 with the store's refusal", rec.Code, say(rec))
	}
	rec = verb("/api/scheduler/pause", "../x")
	if rec.Code != http.StatusBadRequest || !strings.Contains(say(rec), "jN") {
		t.Fatalf("pause of a malformed id: got %d %q, want 400 naming the tool's shape", rec.Code, say(rec))
	}
	rec = verb("/api/scheduler/pause", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("pause with no id: got %d, want 400", rec.Code)
	}

	rec = doReq(t, h, "POST", "/api/scheduler/pause"+q, strings.NewReader(`{"id":"j1"}`), bearer(tok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no origin: got %d, want 403", rec.Code)
	}
	foreign := http.Header{"Origin": {"http://evil.example"}, "Authorization": {"Bearer " + tok}}
	rec = doReq(t, h, "POST", "/api/scheduler/resume"+q, strings.NewReader(`{"id":"j1"}`), foreign)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: got %d, want 403", rec.Code)
	}
	rec = doReq(t, h, "POST", "/api/scheduler/update"+q, strings.NewReader(strings.Repeat("x", maxWriteBytes+1)), hdr())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-cap update: got %d, want 400", rec.Code)
	}
	rec = doReq(t, h, "GET", "/api/scheduler/pause"+q, nil, bearer(tok))
	if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Header().Get("Allow"), "POST") {
		t.Fatalf("GET on a door: got %d allow %q, want 405 naming POST", rec.Code, rec.Header().Get("Allow"))
	}
	rec = doReq(t, h, "POST", "/api/scheduler/runs?id=j1", nil, hdr())
	if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Header().Get("Allow"), "GET") {
		t.Fatalf("POST on runs: got %d allow %q, want 405 naming GET", rec.Code, rec.Header().Get("Allow"))
	}

	rec = doReq(t, h, "POST", "/api/scheduler"+q, strings.NewReader(`{"name":"doorjob","prompt":"p","cron":"0 3 * * *"}`), hdr())
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got %d (body %s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	idRe := regexp.MustCompile(`j\d+`)
	jobID := idRe.FindString(created["reply"].(string))
	if jobID == "" {
		t.Fatalf("create: no id in the reply %q", created["reply"])
	}

	rec = verb("/api/scheduler/pause", jobID)
	if rec.Code != http.StatusOK || !strings.Contains(say(rec), jobID+" doorjob -> paused") {
		t.Fatalf("pause: got %d %q, want the store's voice naming the new state", rec.Code, say(rec))
	}
	if !strings.Contains(list(), jobID+" doorjob paused") {
		t.Fatalf("list after pause: %q, want the job paused", list())
	}
	rec = verb("/api/scheduler/pause", jobID)
	if rec.Code != http.StatusBadRequest || !strings.Contains(say(rec), "already paused") {
		t.Fatalf("pause of a paused job: got %d %q, want the named refusal", rec.Code, say(rec))
	}
	rec = verb("/api/scheduler/resume", jobID)
	if rec.Code != http.StatusOK || !strings.Contains(say(rec), jobID+" doorjob -> active") {
		t.Fatalf("resume: got %d %q, want the store's voice", rec.Code, say(rec))
	}
	if !strings.Contains(list(), jobID+" doorjob active") {
		t.Fatalf("list after resume: %q, want the job active", list())
	}
	rec = verb("/api/scheduler/resume", jobID)
	if rec.Code != http.StatusBadRequest || !strings.Contains(say(rec), "not paused") {
		t.Fatalf("resume of an active job: got %d %q, want the named refusal", rec.Code, say(rec))
	}

	rec = doReq(t, h, "POST", "/api/scheduler/update"+q, strings.NewReader(`{"id":"`+jobID+`","model":"worker-test"}`), hdr())
	if rec.Code != http.StatusOK || !strings.Contains(say(rec), "updated "+jobID+" 'doorjob'") {
		t.Fatalf("update: got %d %q, want the store's voice", rec.Code, say(rec))
	}
	if !strings.Contains(list(), "worker-test") {
		t.Fatalf("list after update: %q, want the new model", list())
	}
	rec = doReq(t, h, "POST", "/api/scheduler/update"+q, strings.NewReader(`{"id":"`+jobID+`"}`), hdr())
	if rec.Code != http.StatusBadRequest || !strings.Contains(say(rec), "update needs a change") {
		t.Fatalf("update with no change: got %d %q, want the named refusal", rec.Code, say(rec))
	}
	rec = doReq(t, h, "POST", "/api/scheduler/update"+q, strings.NewReader(`{"id":"`+jobID+`","cron":"bogus"}`), hdr())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("update with a bad cron: got %d %q, want the verb's refusal", rec.Code, say(rec))
	}
	rec = doReq(t, h, "POST", "/api/scheduler/update"+q, strings.NewReader(`{"id":"j99","model":"x"}`), hdr())
	if rec.Code != http.StatusBadRequest || !strings.Contains(say(rec), "no job 'j99'") {
		t.Fatalf("update of an unknown id: got %d %q, want the named refusal", rec.Code, say(rec))
	}

	rec = verb("/api/scheduler/remove", jobID)
	if rec.Code != http.StatusOK || !strings.Contains(say(rec), jobID+" doorjob -> removed") {
		t.Fatalf("remove: got %d %q, want the store's voice", rec.Code, say(rec))
	}
	if strings.Contains(list(), "doorjob") {
		t.Fatalf("list after remove: %q, want the job gone", list())
	}
	rec = verb("/api/scheduler/remove", jobID)
	if rec.Code != http.StatusBadRequest || !strings.Contains(say(rec), "already removed") {
		t.Fatalf("remove of a removed job: got %d %q, want the named refusal", rec.Code, say(rec))
	}
	rec = doReq(t, h, "POST", "/api/scheduler/update"+q, strings.NewReader(`{"id":"`+jobID+`","model":"x"}`), hdr())
	if rec.Code != http.StatusBadRequest || !strings.Contains(say(rec), "is removed") {
		t.Fatalf("update of a removed id: got %d %q, want the named refusal", rec.Code, say(rec))
	}
	rec = verb("/api/scheduler/resume", jobID)
	if rec.Code != http.StatusBadRequest || !strings.Contains(say(rec), "not paused") {
		t.Fatalf("resume of a removed id: got %d %q, want the named refusal", rec.Code, say(rec))
	}

	i64 := func(v int64) *int64 { return &v }
	sdb, err := srv.stores.scheduler()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sched.RecordRun(context.Background(), sdb, sched.RunRecordInput{
		ID: "j1", Status: "ok", Exit: i64(0), Duration: i64(10), Log: "runs/x/a.log",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.RecordRun(context.Background(), sdb, sched.RunRecordInput{
		ID: "j1", Status: "skip", Reason: "busy",
	}); err != nil {
		t.Fatal(err)
	}

	rec = doReq(t, h, "GET", "/api/scheduler/runs?id=j1", nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("runs: got %d (body %s)", rec.Code, rec.Body.String())
	}
	var runsBody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &runsBody); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runsBody["text"], "j1") || !strings.Contains(runsBody["text"], "runs") ||
		!strings.Contains(runsBody["text"], "ok") || !strings.Contains(runsBody["text"], "busy") {
		t.Fatalf("runs: %q, want the audit trail (the seeded statuses)", runsBody["text"])
	}
	rec = doReq(t, h, "GET", "/api/scheduler/runs?id=j1&n=1", nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("runs n=1: got %d (body %s)", rec.Code, rec.Body.String())
	}
	var one map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(one["text"], "1 run") || !strings.Contains(one["text"], "busy") || strings.Contains(one["text"], "ok") {
		t.Fatalf("runs n=1: %q, want exactly the newest run", one["text"])
	}
	for _, bad := range []string{"n=0", "n=101", "n=x"} {
		rec = doReq(t, h, "GET", "/api/scheduler/runs?id=j1&"+bad, nil, bearer(tok))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("runs %s: got %d, want 400", bad, rec.Code)
		}
	}
	rec = doReq(t, h, "GET", "/api/scheduler/runs?id=j99", nil, bearer(tok))
	if rec.Code != http.StatusNotFound || !strings.Contains(say(rec), "no job 'j99'") {
		t.Fatalf("runs of an unknown id: got %d %q, want the named 404", rec.Code, say(rec))
	}
	rec = doReq(t, h, "GET", "/api/scheduler/runs?id=../x", nil, bearer(tok))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("runs of a malformed id: got %d, want 400", rec.Code)
	}
}

func TestMemoryRouteIsGone(t *testing.T) {
	srv, tok := newTestServer(t)
	rec := doReq(t, srv.Handler(), "GET", "/api/memory?cwd="+testCWD, nil, bearer(tok))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("memory: got %d, want 404", rec.Code)
	}
	rec = doReq(t, srv.Handler(), "GET", "/", nil, bearer(tok))
	if strings.Contains(rec.Body.String(), `data-view="memory"`) {
		t.Fatal("the page still carries a memory tab")
	}
}

func TestModelsTable(t *testing.T) {
	srv, tok := newTestServer(t)
	rec := doReq(t, srv.Handler(), "GET", "/api/models", nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("models: got %d", rec.Code)
	}
	var body struct {
		Models []struct {
			ID      string   `json:"id"`
			Role    string   `json:"role"`
			Effort  string   `json:"effort"`
			Efforts []string `json:"efforts"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Models) != 2 {
		t.Fatalf("models: %d rows, want 2", len(body.Models))
	}
	byID := map[string]struct {
		ID      string
		Role    string
		Effort  string
		Efforts []string
	}{}
	for _, m := range body.Models {
		byID[m.ID] = struct {
			ID      string
			Role    string
			Effort  string
			Efforts []string
		}{m.ID, m.Role, m.Effort, m.Efforts}
	}
	g, ok := byID["gpt-test"]
	if !ok || g.Role != "interactive" || g.Effort != "high" || len(g.Efforts) != 3 {
		t.Fatalf("gpt-test row %+v, want interactive/high/3 efforts", g)
	}
	w, ok := byID["worker-test"]
	if !ok || w.Role != "worker" {
		t.Fatalf("worker-test row %+v, want worker role", w)
	}
}

func TestPluginsListing(t *testing.T) {
	srv, tok := newTestServer(t)
	rec := doReq(t, srv.Handler(), "GET", "/api/plugins", nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("plugins: got %d", rec.Code)
	}
	var body struct {
		Loaded []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"loaded"`
		Pending []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"pending"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Loaded) != 1 || body.Loaded[0].Name != "loaded_one" {
		t.Fatalf("loaded %v, want loaded_one", body.Loaded)
	}
	if body.Loaded[0].Description != "the loaded plugin" {
		t.Fatalf("loaded description %q, want the file's DESCRIPTION", body.Loaded[0].Description)
	}
	if len(body.Pending) != 1 || body.Pending[0].Name != "pending_one" {
		t.Fatalf("pending %v, want pending_one", body.Pending)
	}
	if body.Pending[0].Description != "the pending plugin" {
		t.Fatalf("pending description %q, want the file's DESCRIPTION", body.Pending[0].Description)
	}

	if err := os.MkdirAll(filepath.Join(srv.home, "plugins", "disabled"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srv.home, "plugins", "disabled", "off_one.py"), []byte("DESCRIPTION = \"the off plugin\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = doReq(t, srv.Handler(), "GET", "/api/plugins", nil, bearer(tok))
	var body2 struct {
		Loaded []struct {
			Name string `json:"name"`
		} `json:"loaded"`
		Disabled []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"disabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body2); err != nil {
		t.Fatal(err)
	}
	if len(body2.Disabled) != 1 || body2.Disabled[0].Name != "off_one" || body2.Disabled[0].Description != "the off plugin" {
		t.Fatalf("disabled %v, want off_one with its DESCRIPTION", body2.Disabled)
	}
	if len(body2.Loaded) != 1 {
		t.Fatalf("a disabled plugin is not loaded: %v", body2.Loaded)
	}
}

func TestPluginsCreate(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()

	post := func(body string) *httptest.ResponseRecorder {
		hdr := both(bearer(tok), "Origin", "http://127.0.0.1:7777")
		hdr.Set("Content-Type", "application/json")
		return doReq(t, h, "POST", "/api/plugins", strings.NewReader(body), hdr)
	}

	rec := post(`{"name":"hello","description":"says hi","code":"return \"hello\" + str(args)"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got %d (body %s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	reply, _ := created["reply"].(string)
	if !strings.Contains(reply, "pending") {
		t.Fatalf("create: reply %q, want the pending zone named", reply)
	}
	path := filepath.Join(srv.home, "plugins", "pending", "hello.py")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the created file: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, `DESCRIPTION = "says hi"`) {
		t.Fatalf("file missing the DESCRIPTION:\n%s", src)
	}
	if !strings.Contains(src, "SCHEMA = {") || !strings.Contains(src, "\"type\": \"object\"") {
		t.Fatalf("file missing the SCHEMA object:\n%s", src)
	}
	if !strings.Contains(src, "def run(args):") || !strings.Contains(src, `return "hello" + str(args)`) {
		t.Fatalf("file missing the run body:\n%s", src)
	}
	rec = doReq(t, h, "GET", "/api/plugins", nil, bearer(tok))
	var body struct {
		Pending []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"pending"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range body.Pending {
		if p.Name == "hello" && p.Description == "says hi" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending listing after create: %v, want hello", body.Pending)
	}

	if err := os.WriteFile(filepath.Join(srv.home, "plugins", "pending", "dropped.py"),
		[]byte("DESCRIPTION = \"dropped in later\"\nSCHEMA = {\"type\": \"object\"}\n\ndef run(args):\n    return \"ok\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = doReq(t, h, "GET", "/api/plugins", nil, bearer(tok))
	if !strings.Contains(rec.Body.String(), "dropped") {
		t.Fatalf("live listing: %s, want the later-dropped file", rec.Body.String())
	}

	rec = post(`{"name":"hello","description":"again","code":"return 1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate: got %d, want 400", rec.Code)
	}
	var dup map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &dup); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dup["error"], "hello") || !strings.Contains(dup["error"], "already") {
		t.Fatalf("duplicate: %q, want the named refusal", dup["error"])
	}

	rec = post(`{"name":"loaded_one","description":"x","code":"return 1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("loaded collision: got %d, want 400", rec.Code)
	}

	for _, bad := range []string{"Hello", "1abc", "a/b", "has space"} {
		rec = post(`{"name":"` + bad + `","description":"d","code":"return 1"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad name %q: got %d, want 400", bad, rec.Code)
		}
	}

	rec = post(`{"name":"emptybody","description":"d","code":"  \n"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code: got %d, want 400", rec.Code)
	}

	rec = doReq(t, h, "POST", "/api/plugins", strings.NewReader(`{"name":"x","description":"d","code":"return 1"}`), bearer(tok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no origin: got %d, want 403", rec.Code)
	}
	big := strings.Repeat("x", maxWriteBytes+1)
	rec = doReq(t, h, "POST", "/api/plugins", strings.NewReader(big), both(bearer(tok), "Origin", "http://127.0.0.1:7777"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-cap: got %d, want 400", rec.Code)
	}
}

func TestPluginDisableEnableDoors(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	hdr := func() http.Header {
		x := both(bearer(tok), "Origin", "http://127.0.0.1:7777")
		x.Set("Content-Type", "application/json")
		return x
	}
	list := func() map[string]string {
		rec := doReq(t, h, "GET", "/api/plugins", nil, bearer(tok))
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		out := map[string]string{}
		for _, z := range []string{"loaded", "pending", "disabled"} {
			rows, _ := body[z].([]any)
			names := []string{}
			for _, r := range rows {
				m, _ := r.(map[string]any)
				names = append(names, m["name"].(string))
			}
			out[z] = strings.Join(names, ",")
		}
		return out
	}

	rec := doReq(t, h, "POST", "/api/plugins/disable", strings.NewReader(`{"name":"loaded_one"}`), hdr())
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: got %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	reply, _ := body["reply"].(string)
	if !strings.Contains(reply, "disabled 'loaded_one' (plugins -> plugins/disabled); hidden next turn") {
		t.Fatalf("disable: reply %q, want the command's voice", reply)
	}
	if _, err := os.Stat(filepath.Join(srv.home, "plugins", "loaded_one.py")); err == nil {
		t.Fatal("disable: the file must leave plugins/")
	}
	if _, err := os.Stat(filepath.Join(srv.home, "plugins", "disabled", "loaded_one.py")); err != nil {
		t.Fatalf("disable: the file must land in plugins/disabled/: %v", err)
	}
	got := list()
	if got["loaded"] != "" || got["disabled"] != "loaded_one" {
		t.Fatalf("list after disable: loaded=%q disabled=%q, want loaded_one moved", got["loaded"], got["disabled"])
	}

	rec = doReq(t, h, "POST", "/api/plugins/enable", strings.NewReader(`{"name":"loaded_one"}`), hdr())
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: got %d %s", rec.Code, rec.Body.String())
	}
	body = map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	reply, _ = body["reply"].(string)
	if !strings.Contains(reply, "enabled 'loaded_one' (plugins/disabled -> plugins); live at the next plugins reload") {
		t.Fatalf("enable: reply %q, want the command's voice", reply)
	}
	if _, err := os.Stat(filepath.Join(srv.home, "plugins", "loaded_one.py")); err != nil {
		t.Fatalf("enable: the file must return to plugins/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(srv.home, "plugins", "disabled", "loaded_one.py")); err == nil {
		t.Fatal("enable: the file must leave plugins/disabled/")
	}
	got = list()
	if got["loaded"] != "loaded_one" || got["disabled"] != "" {
		t.Fatalf("list after enable: loaded=%q disabled=%q, want loaded_one back", got["loaded"], got["disabled"])
	}

	rec = doReq(t, h, "POST", "/api/plugins/disable", strings.NewReader(`{"name":"pending_one"}`), hdr())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disable of a pending plugin: got %d, want 404", rec.Code)
	}
	if rec = doReq(t, h, "POST", "/api/plugins/disable", strings.NewReader(`{"name":"loaded_one"}`), hdr()); rec.Code != http.StatusOK {
		t.Fatalf("disable again: got %d %s", rec.Code, rec.Body.String())
	}
	if rec = doReq(t, h, "POST", "/api/plugins/disable", strings.NewReader(`{"name":"loaded_one"}`), hdr()); rec.Code != http.StatusNotFound {
		t.Fatalf("disable an already-disabled plugin: got %d, want 404 (no such plugin in plugins/)", rec.Code)
	}
	if err := os.WriteFile(filepath.Join(srv.home, "plugins", "loaded_one.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec = doReq(t, h, "POST", "/api/plugins/disable", strings.NewReader(`{"name":"loaded_one"}`), hdr()); rec.Code != http.StatusConflict {
		t.Fatalf("disable with a stale duplicate: got %d, want 409 (already in the disabled zone)", rec.Code)
	}
	if err := os.Remove(filepath.Join(srv.home, "plugins", "loaded_one.py")); err != nil {
		t.Fatal(err)
	}
	if rec = doReq(t, h, "POST", "/api/plugins/enable", strings.NewReader(`{"name":"loaded_one"}`), hdr()); rec.Code != http.StatusOK {
		t.Fatalf("enable again: got %d %s", rec.Code, rec.Body.String())
	}
	if rec = doReq(t, h, "POST", "/api/plugins/enable", strings.NewReader(`{"name":"loaded_one"}`), hdr()); rec.Code != http.StatusNotFound {
		t.Fatalf("enable an already-loaded plugin: got %d, want 404 (no such plugin in plugins/disabled/)", rec.Code)
	}
	if err := os.WriteFile(filepath.Join(srv.home, "plugins", "disabled", "loaded_one.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec = doReq(t, h, "POST", "/api/plugins/enable", strings.NewReader(`{"name":"loaded_one"}`), hdr()); rec.Code != http.StatusConflict {
		t.Fatalf("enable with a stale duplicate: got %d, want 409 (already in the plugins zone)", rec.Code)
	}

	rec = doReq(t, h, "POST", "/api/plugins/disable", strings.NewReader(`{"name":"loaded_one"}`), bearer(tok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no origin: got %d, want 403", rec.Code)
	}
	if rec = doReq(t, h, "GET", "/api/plugins/disable", nil, bearer(tok)); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET disable: got %d, want 405", rec.Code)
	}
}

func TestStaticAssets(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()

	for path, wants := range map[string][]string{
		"/": {"<!doctype html", `id="nav-toggle" class="nav-toggle"`, `id="cwd-add"`, `id="browse-btn"`, `data-view="plugins"`},
		"/static/app.js": {
			"renderSessions",
			"parseTodo",
			"parseScheduler",
			"progressBar",
			"addCwd",
			"setNavOpen",
			"highlightPython",
			"editorEl",
			"openForge",
			"browseTo",
			"toolBlock",
			"plugins/disable",
			"plugins/enable",
			"/api/scheduler/pause",
			"/api/scheduler/resume",
			"/api/scheduler/remove",
			"/api/scheduler/update",
			"/api/scheduler/runs",
			"schedacts",
			"schedup",
			"schedconfirm",
		},
		"/static/style.css": {"--accent", "@media (max-width: 720px)", ".nav-open", ".nav-toggle {\n  display: none;", ".editor", "--effort-xhigh", ".schedacts", ".schedup"},
	} {
		rec := doReq(t, h, "GET", path, nil, bearer(tok))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d", path, rec.Code)
		}
		for _, want := range wants {
			if !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("%s: body missing %q", path, want)
			}
		}
	}

	rec := doReq(t, h, "GET", "/static/nope.js", nil, bearer(tok))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown static: got %d, want 404", rec.Code)
	}

	rec = doReq(t, h, "GET", "/static/../web.go", nil, bearer(tok))
	if rec.Code == http.StatusOK {
		t.Fatalf("traversal: got 200, want a refusal")
	}
}

func TestStaticAssetsSchedulerPhoneRow(t *testing.T) {
	srv, tok := newTestServer(t)
	rec := doReq(t, srv.Handler(), "GET", "/static/style.css", nil, bearer(tok))
	body := rec.Body.String()
	if !strings.Contains(body, ".rowact { display: inline-block; min-height: 44px; padding: 10px 12px; }") {
		t.Fatal("phone: the 44px tap target rule is gone")
	}
	if strings.Contains(body, "flex-direction: column") {
		t.Fatal("phone: the job row still stacks its controls")
	}
}

func TestForgeSourceSaveApprove(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	hdr := func() http.Header {
		x := both(bearer(tok), "Origin", "http://127.0.0.1:7777")
		x.Set("Content-Type", "application/json")
		return x
	}

	rec := doReq(t, h, "GET", "/api/plugins/source?name=loaded_one&zone=loaded", nil, bearer(tok))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "the loaded plugin") {
		t.Fatalf("loaded source: got %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, "GET", "/api/plugins/source?name=pending_one&zone=pending", nil, bearer(tok))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "the pending plugin") {
		t.Fatalf("pending source: got %d %s", rec.Code, rec.Body.String())
	}

	if rec = doReq(t, h, "GET", "/api/plugins/source?name=loaded_one&zone=elsewhere", nil, bearer(tok)); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad zone: got %d, want 400", rec.Code)
	}
	if rec = doReq(t, h, "GET", "/api/plugins/source?name=../x&zone=loaded", nil, bearer(tok)); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad name: got %d, want 400", rec.Code)
	}
	if rec = doReq(t, h, "GET", "/api/plugins/source?name=nope&zone=loaded", nil, bearer(tok)); rec.Code != http.StatusNotFound {
		t.Fatalf("absent: got %d, want 404", rec.Code)
	}

	src := "DESCRIPTION = \"drafted\"\nSCHEMA = {\"type\": \"object\"}\n\ndef run(args):\n    return \"draft\"\n"
	body, _ := json.Marshal(map[string]string{"name": "draft", "source": src})
	rec = doReq(t, h, "POST", "/api/plugins/save", strings.NewReader(string(body)), hdr())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "created 'draft'") {
		t.Fatalf("save: got %d %s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(srv.home, "plugins", "pending", "draft.py"))
	if err != nil || string(got) != src {
		t.Fatalf("saved file: %v %q", err, string(got))
	}
	body, _ = json.Marshal(map[string]string{"name": "draft", "source": strings.Replace(src, "draft", "draft2", 1)})
	rec = doReq(t, h, "POST", "/api/plugins/save", strings.NewReader(string(body)), hdr())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "updated 'draft'") {
		t.Fatalf("re-save: got %d %s", rec.Code, rec.Body.String())
	}

	body, _ = json.Marshal(map[string]string{"name": "nocontract", "source": "x = 1\n"})
	if rec = doReq(t, h, "POST", "/api/plugins/save", strings.NewReader(string(body)), hdr()); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "DESCRIPTION") {
		t.Fatalf("no contract: got %d %s", rec.Code, rec.Body.String())
	}
	body, _ = json.Marshal(map[string]string{"name": "My Plugin", "source": src})
	if rec = doReq(t, h, "POST", "/api/plugins/save", strings.NewReader(string(body)), hdr()); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "filename stem") {
		t.Fatalf("bad name: got %d %s", rec.Code, rec.Body.String())
	}
	body, _ = json.Marshal(map[string]string{"name": "bash", "source": src})
	if rec = doReq(t, h, "POST", "/api/plugins/save", strings.NewReader(string(body)), hdr()); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "native") {
		t.Fatalf("native collision: got %d %s", rec.Code, rec.Body.String())
	}
	body, _ = json.Marshal(map[string]string{"name": "draft", "source": src})
	if rec = doReq(t, h, "POST", "/api/plugins/save", strings.NewReader(string(body)), bearer(tok)); rec.Code != http.StatusForbidden {
		t.Fatalf("no origin: got %d, want 403", rec.Code)
	}

	body, _ = json.Marshal(map[string]any{"name": "draft"})
	rec = doReq(t, h, "POST", "/api/plugins/approve", strings.NewReader(string(body)), hdr())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "approved 'draft'") {
		t.Fatalf("approve: got %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(srv.home, "plugins", "draft.py")); err != nil {
		t.Fatalf("approved file missing: %v", err)
	}
	if rec = doReq(t, h, "POST", "/api/plugins/approve", strings.NewReader(string(body)), hdr()); rec.Code != http.StatusNotFound {
		t.Fatalf("approve twice: got %d, want 404", rec.Code)
	}

	rev := strings.Replace(src, "draft", "revised", -1)
	body, _ = json.Marshal(map[string]string{"name": "draft", "source": rev})
	if rec = doReq(t, h, "POST", "/api/plugins/save", strings.NewReader(string(body)), hdr()); rec.Code != http.StatusOK {
		t.Fatalf("save revision: got %d %s", rec.Code, rec.Body.String())
	}
	body, _ = json.Marshal(map[string]any{"name": "draft"})
	if rec = doReq(t, h, "POST", "/api/plugins/approve", strings.NewReader(string(body)), hdr()); rec.Code != http.StatusConflict {
		t.Fatalf("approve over installed: got %d, want 409", rec.Code)
	}
	body, _ = json.Marshal(map[string]any{"name": "draft", "replace": true})
	if rec = doReq(t, h, "POST", "/api/plugins/approve", strings.NewReader(string(body)), hdr()); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "replaced") {
		t.Fatalf("approve replace: got %d %s", rec.Code, rec.Body.String())
	}
	got, _ = os.ReadFile(filepath.Join(srv.home, "plugins", "draft.py"))
	if string(got) != rev {
		t.Fatalf("replace did not swap the file: %q", string(got))
	}

	if err := os.WriteFile(filepath.Join(srv.home, "plugins", "pending", "read.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(map[string]any{"name": "read"})
	if rec = doReq(t, h, "POST", "/api/plugins/approve", strings.NewReader(string(body)), hdr()); rec.Code != http.StatusBadRequest {
		t.Fatalf("native approve: got %d, want 400", rec.Code)
	}

	if rec = doReq(t, h, "GET", "/api/plugins/save", nil, bearer(tok)); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET save: got %d, want 405", rec.Code)
	}
}

func TestBrowseRootedAtHome(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	root := t.TempDir()
	srv.root = root
	for _, d := range []string{"alpha", "beta/inner", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Root   string `json:"root"`
		Path   string `json:"path"`
		Parent string `json:"parent"`
		Dirs   []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"dirs"`
	}
	get := func(q string) int {
		rec := doReq(t, h, "GET", "/api/fs"+q, nil, bearer(tok))
		body.Dirs = nil
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code
	}
	names := func() []string {
		out := []string{}
		for _, d := range body.Dirs {
			out = append(out, d.Name)
		}
		return out
	}

	if code := get(""); code != http.StatusOK {
		t.Fatalf("root: got %d", code)
	}
	if got := names(); strings.Join(got, ",") != "alpha,beta" {
		t.Fatalf("root dirs %v, want alpha,beta (no files, no hidden)", got)
	}
	if body.Parent != "" {
		t.Fatalf("root parent %q, want none", body.Parent)
	}

	if get("?hidden=true"); !strings.Contains(strings.Join(names(), ","), ".hidden") {
		t.Fatalf("hidden=true dirs %v, want .hidden", names())
	}
	if code := get("?path=" + filepath.Join(root, "beta")); code != http.StatusOK || strings.Join(names(), ",") != "inner" || body.Parent == "" {
		t.Fatalf("child: got %d dirs %v parent %q", code, names(), body.Parent)
	}
	if code := get("?path=/"); code != http.StatusForbidden {
		t.Fatalf("outside root: got %d, want 403", code)
	}
	if code := get("?path=" + filepath.Join(root, "..")); code != http.StatusForbidden {
		t.Fatalf("traversal: got %d, want 403", code)
	}
	if code := get("?path=" + filepath.Join(root, "nope")); code != http.StatusNotFound {
		t.Fatalf("absent: got %d, want 404", code)
	}
	if code := get("?path=" + filepath.Join(root, "file.txt")); code != http.StatusBadRequest {
		t.Fatalf("a file: got %d, want 400", code)
	}
	if rec := doReq(t, h, "POST", "/api/fs", nil, bearer(tok)); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST fs: got %d, want 405", rec.Code)
	}
}

func TestTodoStartAndComplete(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	q := "?cwd=" + testCWD
	hdr := func() http.Header {
		x := both(bearer(tok), "Origin", "http://127.0.0.1:7777")
		x.Set("Content-Type", "application/json")
		return x
	}
	read := func(all bool) string {
		target := "/api/todo" + q
		if all {
			target += "&all=true"
		}
		rec := doReq(t, h, "GET", target, nil, bearer(tok))
		var body map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body["text"]
	}
	rec := doReq(t, h, "POST", "/api/todo/start"+q, strings.NewReader(`{"id":"t1"}`), hdr())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "t1") {
		t.Fatalf("start: got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(read(false), "t1 [~]") {
		t.Fatalf("after start the read must show t1 active: %q", read(false))
	}
	rec = doReq(t, h, "POST", "/api/todo/complete"+q, strings.NewReader(`{"id":"t1"}`), hdr())
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(read(true), "t1 [x]") {
		t.Fatalf("after complete the history must show t1 done: %q", read(true))
	}

	if rec = doReq(t, h, "POST", "/api/todo/start"+q, strings.NewReader(`{"id":"t99"}`), hdr()); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown id: got %d, want 400", rec.Code)
	}
	if rec = doReq(t, h, "POST", "/api/todo/start"+q, strings.NewReader(`{"id":"../x"}`), hdr()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: got %d, want 400", rec.Code)
	}
	if rec = doReq(t, h, "POST", "/api/todo/start"+q, strings.NewReader(`{"id":"t1"}`), bearer(tok)); rec.Code != http.StatusForbidden {
		t.Fatalf("no origin: got %d, want 403", rec.Code)
	}
	if rec = doReq(t, h, "GET", "/api/todo/complete"+q, nil, bearer(tok)); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET complete: got %d, want 405", rec.Code)
	}
}

func TestPluginListingReadsParenthesizedDescriptions(t *testing.T) {
	srv, tok := newTestServer(t)
	src := "DESCRIPTION = (\"The box, read-only: \"\n               \"users and groups.\")\nSCHEMA = {\"type\": \"object\"}\n\ndef run(args):\n    return \"ok\"\n"
	if err := os.WriteFile(filepath.Join(srv.home, "plugins", "paren.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := doReq(t, srv.Handler(), "GET", "/api/plugins", nil, bearer(tok))
	if !strings.Contains(rec.Body.String(), "The box, read-only: users and groups.") {
		t.Fatalf("the parenthesized DESCRIPTION must be read whole: %s", rec.Body.String())
	}
}

func TestTodoRetryFromTheDashboard(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	q := "?cwd=" + testCWD
	hdr := both(bearer(tok), "Origin", "http://127.0.0.1:7777")
	hdr.Set("Content-Type", "application/json")
	if rec := doReq(t, h, "POST", "/api/todo/start"+q, strings.NewReader(`{"id":"t1"}`), hdr); rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	db, err := srv.stores.todo(testCWD)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Fail(context.Background(), db, todostore.Project{Key: scope.Key(testCWD), Label: scope.Label(testCWD)}, "t1", "dashboard"); err != nil {
		t.Fatal(err)
	}
	rec := doReq(t, h, "POST", "/api/todo/retry"+q, strings.NewReader(`{"id":"t1"}`), hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, "GET", "/api/todo"+q, nil, bearer(tok))
	if !strings.Contains(rec.Body.String(), "t1 [ ]") {
		t.Fatalf("after retry the task is pending again: %s", rec.Body.String())
	}
}

func TestWriteFromTheRequestsOwnFrontIsSameOrigin(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	q := "?cwd=" + testCWD
	for _, c := range []struct {
		name string
		hdr  http.Header
		host string
		want int
	}{
		{"tailnet name as Host", both(bearer(tok), "Origin", "http://battlestation:7777"), "battlestation:7777", http.StatusOK},
		{"forwarded host + https", both(both(bearer(tok), "Origin", "https://battlestation.tailb0b3f5.ts.net"), "X-Forwarded-Host", "battlestation.tailb0b3f5.ts.net"), "127.0.0.1:7777", http.StatusOK},
		{"foreign origin, real host", both(bearer(tok), "Origin", "http://evil.example"), "battlestation:7777", http.StatusForbidden},
		{"origin names another front", both(bearer(tok), "Origin", "http://battlestation:7777"), "127.0.0.1:7777", http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "forwarded host + https" {
				c.hdr.Set("X-Forwarded-Proto", "https")
			}
			req := httptest.NewRequest("POST", "/api/todo"+q, strings.NewReader("alpha\n"))
			for k, v := range c.hdr {
				req.Header[k] = v
			}
			req.Host = c.host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("got %d %s, want %d", rec.Code, rec.Body.String(), c.want)
			}
		})
	}
}

func gitInitWeb(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "-c", "user.email=test@rig", "-c", "user.name=rig", "add", "-A")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v (%s)", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "-c", "user.email=test@rig", "-c", "user.name=rig", "commit", "-q", "-m", "seed")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v (%s)", err, out)
	}
}

func TestTodoRoutesResolveThroughTheRepoScope(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	repo := t.TempDir()
	gitInitWeb(t, repo)
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := srv.stores.todo(testCWD)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Create(context.Background(), db, todostore.Project{Key: scope.Key(repo), Label: scope.Label(repo)}, []todostore.CreateItem{{Text: "repo plan"}}, "seed"); err != nil {
		t.Fatal(err)
	}
	rec := doReq(t, h, "GET", "/api/todo?cwd="+sub, nil, bearer(tok))
	if !strings.Contains(rec.Body.String(), "repo plan") {
		t.Fatalf("a subdirectory of the repo must read the repo's queue: %s", rec.Body.String())
	}
	rec = doReq(t, h, "GET", "/api/todo?cwd="+testCWD, nil, bearer(tok))
	if strings.Contains(rec.Body.String(), "repo plan") {
		t.Fatalf("another workspace must not see the repo queue: %s", rec.Body.String())
	}
}
