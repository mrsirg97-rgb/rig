package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/store"
	remstore "github.com/mrsirg97-rgb/rig/store/rem"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
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

// seedHome builds a temp rig home with seeded stores and plugins, using the
// stores' own path helpers (the round-trip the dashboard relies on). The
// transcript golden's seed: one closed session with a user turn, an
// assistant turn (reasoning), a tool call and its result, and a usage row.
func seedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	ctx := context.Background()

	spath := state.StorePath(home, testCWD)
	if err := os.MkdirAll(filepath.Dir(spath), 0o755); err != nil {
		t.Fatal(err)
	}
	sdb, _, err := store.Open(spath, state.Statements(), state.SchemaVersion)
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
	if err := state.RecordToolCall(ctx, sdb, seq, "call_1", "bash", `{"cmd":"ls"}`); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolResult(ctx, sdb, "call_1", "file1\nfile2", nil); err != nil {
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

	tpath := todostore.StorePath(home, testCWD)
	if err := os.MkdirAll(filepath.Dir(tpath), 0o755); err != nil {
		t.Fatal(err)
	}
	tdb, _, err := store.Open(tpath, todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Create(ctx, tdb, []todostore.CreateItem{{Text: "seeded task"}}, "seed"); err != nil {
		t.Fatal(err)
	}
	if err := tdb.DB.Close(); err != nil {
		t.Fatal(err)
	}

	rpath := remstore.FilePath(home)
	if err := os.MkdirAll(filepath.Dir(rpath), 0o755); err != nil {
		t.Fatal(err)
	}
	rdb, _, err := store.Open(rpath, remstore.Statements(), remstore.SchemaVersion)
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
	gdb, _, err := store.Open(sched.StorePathFor(shome, sched.JobKey{Scope: "global"}), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	cdb, _, err := store.Open(sched.StorePathFor(shome, sched.JobKey{Scope: "cwd", Hash: sched.CwdHash(testCWD)}), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Create(ctx, sched.Stores{Global: gdb, Cwd: cdb}, &fakeCrontab{},
		sched.CreateInput{Name: "digest", Prompt: "the digest", Cron: "30 7 * * *", Scope: "cwd", Cwd: testCWD},
		testCWD, "seed", "rig run-job", time.Now); err != nil {
		t.Fatal(err)
	}
	if err := gdb.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cdb.DB.Close(); err != nil {
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

// newTestServer seeds a home, builds the dashboard, and returns it with the
// token and the allowed origin set (the write's Origin wall).
func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	home := seedHome(t)
	srv, err := New(Options{Home: home, CWD: testCWD, Models: modelsTable(t), Crontab: &fakeCrontab{}})
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

// --- the token gate (SPEC_SERVE 5) ---

func TestTokenGate(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	home := srv.home

	// no credential -> 401 (with the challenge).
	rec := doReq(t, h, "GET", "/", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential: got %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("no credential: missing WWW-Authenticate: Bearer, got %q", rec.Header().Get("WWW-Authenticate"))
	}

	// a wrong token -> 401.
	rec = doReq(t, h, "GET", "/", nil, bearer("wrong"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: got %d, want 401", rec.Code)
	}

	// the minted token (header) -> 200.
	rec = doReq(t, h, "GET", "/", nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer: got %d, want 200", rec.Code)
	}

	// ?token= sets the HttpOnly SameSite=Strict cookie, then reads as it.
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

	// the token file is 0600 and is not re-minted on a second read.
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

// --- the loopback refusal (SPEC_SERVE 5) ---

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

// --- the allow-list (SPEC_SERVE posture) ---

func TestAllowList404And405(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()

	rec := doReq(t, h, "GET", "/api/nonexistent", nil, bearer(tok))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path: got %d, want 404", rec.Code)
	}

	// a known path with the wrong method is a 405 with the Allow header.
	rec = doReq(t, h, "POST", "/api/models", nil, bearer(tok))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method: got %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") {
		t.Fatalf("wrong method: Allow %q, want GET in it", allow)
	}

	// the todo path allows both GET and POST.
	rec = doReq(t, h, "DELETE", "/api/todo", nil, bearer(tok))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("todo DELETE: got %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
		t.Fatalf("todo DELETE: Allow %q, want GET and POST", allow)
	}
}

// --- the write's walls (SPEC_SERVE posture) ---

func TestTodoCreateWalls(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	q := "?cwd=" + testCWD

	// no Origin -> refused (same-origin only).
	rec := doReq(t, h, "POST", "/api/todo"+q, strings.NewReader("a task\n"), bearer(tok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no origin: got %d, want 403", rec.Code)
	}

	// a foreign Origin -> refused.
	foreign := http.Header{"Origin": {"http://evil.example"}, "Authorization": {"Bearer " + tok}}
	rec = doReq(t, h, "POST", "/api/todo"+q, strings.NewReader("a task\n"), foreign)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: got %d, want 403", rec.Code)
	}

	// an empty body -> refused (no create with zero tasks).
	rec = doReq(t, h, "POST", "/api/todo"+q, strings.NewReader("   \n"), both(bearer(tok), "Origin", "http://127.0.0.1:7777"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: got %d, want 400", rec.Code)
	}

	// an over-cap body -> refused.
	big := strings.Repeat("x", maxWriteBytes+1)
	rec = doReq(t, h, "POST", "/api/todo"+q, strings.NewReader(big), both(bearer(tok), "Origin", "http://127.0.0.1:7777"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-cap: got %d, want 400", rec.Code)
	}

	// a same-Origin create -> the verb's reply verbatim, and the queue replaced.
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

	// the queue now carries the created tasks (Create is an upsert: the
	// seeded task stays, the new ones are added).
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

// --- the read views (SPEC_SERVE) ---

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

	// a cwd with no state store is a named refusal (fail closed).
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
	// the user turn, the assistant turn (reasoning + the tool call), and the
	// tool result (a tool message) — the transcript as structure.
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
	// the usage rows (the typed read beside Resume).
	if len(body.Usage) != 1 {
		t.Fatalf("usage: %d rows, want 1", len(body.Usage))
	}
	if body.Usage[0].Prompt != 100 || body.Usage[0].Completion != 42 {
		t.Fatalf("usage row %+v, want prompt 100 completion 42", body.Usage[0])
	}

	// an unknown session is a named refusal.
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
	// the history toggle (ReadAll) also returns the store's text.
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

// --- the scheduler create (SPEC_SERVE phase 2, decision 7) ---

func TestSchedulerCreate(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()
	q := "?cwd=" + testCWD

	post := func(body string) *httptest.ResponseRecorder {
		hdr := both(bearer(tok), "Origin", "http://127.0.0.1:7777")
		hdr.Set("Content-Type", "application/json")
		return doReq(t, h, "POST", "/api/scheduler"+q, strings.NewReader(body), hdr)
	}

	// a valid 5-field create -> the verb's reply, and the list carries it.
	rec := post(`{"name":"nightly","prompt":"do the nightly","cron":"0 3 * * *"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got %d (body %s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	reply, _ := created["reply"].(string)
	if !strings.Contains(reply, "nightly") || !strings.Contains(reply, "cwd") {
		t.Fatalf("create: reply %q, want the job named in its scope", reply)
	}
	rec = doReq(t, h, "GET", "/api/scheduler"+q, nil, bearer(tok))
	var after map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after["text"], "nightly") {
		t.Fatalf("list after create: %q, want the new job", after["text"])
	}

	// a 'once' plus a valid 'at' lands.
	rec = post(`{"name":"oncejob","prompt":"once","cron":"once","at":"2026-01-02T03:04:05Z"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("once create: got %d (body %s)", rec.Code, rec.Body.String())
	}
	if r := post(`{"name":"oncejob","prompt":"twice","cron":"0 4 * * *"}`); r.Code == http.StatusOK {
		t.Fatal("a second create with the same name: want the duplicate refusal")
	}

	// a duplicate name in the same scope is a named refusal.
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

	// a bad cron is the verb's refusal.
	rec = post(`{"name":"badopt","prompt":"p","cron":"bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cron: got %d, want 400", rec.Code)
	}
	// 'once' without an 'at' is a refusal.
	rec = post(`{"name":"noat","prompt":"p","cron":"once"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("once without at: got %d, want 400", rec.Code)
	}
	// empty name and empty prompt are refusals.
	rec = post(`{"prompt":"p","cron":"0 6 * * *"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name: got %d, want 400", rec.Code)
	}
	rec = post(`{"name":"noprompt","cron":"0 7 * * *"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty prompt: got %d, want 400", rec.Code)
	}

	// a global-scope create lands in the global section.
	rec = post(`{"name":"gjob","prompt":"p","cron":"0 8 * * *","scope":"global"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("global create: got %d (body %s)", rec.Code, rec.Body.String())
	}
	var gcreated map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &gcreated); err != nil {
		t.Fatal(err)
	}
	if g, _ := gcreated["reply"].(string); !strings.Contains(g, "global") {
		t.Fatalf("global create: reply %q, want the global scope", g)
	}
	rec = doReq(t, h, "GET", "/api/scheduler"+q, nil, bearer(tok))
	var gbody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &gbody); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gbody["text"], "gjob") {
		t.Fatalf("global job missing from the list: %q", gbody["text"])
	}

	// the walls: no Origin, a foreign Origin, an over-cap body.
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

	// a wrong method on the path is a 405 naming POST.
	rec = doReq(t, h, "DELETE", "/api/scheduler"+q, nil, bearer(tok))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE: got %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
		t.Fatalf("DELETE: Allow %q, want GET and POST", allow)
	}
}

func TestMemoryRecent(t *testing.T) {
	srv, tok := newTestServer(t)
	rec := doReq(t, srv.Handler(), "GET", "/api/memory?cwd="+testCWD, nil, bearer(tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("memory: got %d", rec.Code)
	}
	var body struct {
		Memories []string `json:"memories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range body.Memories {
		if m == "remembered fact" {
			found = true
		}
	}
	if !found {
		t.Fatalf("memory: %v, want the seeded memory", body.Memories)
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
}

// --- the plugin create (SPEC_SERVE phase 2, decision 7) ---

func TestPluginsCreate(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()

	post := func(body string) *httptest.ResponseRecorder {
		hdr := both(bearer(tok), "Origin", "http://127.0.0.1:7777")
		hdr.Set("Content-Type", "application/json")
		return doReq(t, h, "POST", "/api/plugins", strings.NewReader(body), hdr)
	}

	// a valid create -> the file lands in the pending zone with the
	// contract (DESCRIPTION, SCHEMA, the run body), and the listing
	// carries it (the live listing, decision 8).
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

	// the live listing: a file dropped after New is visible without a
	// rebuild.
	if err := os.WriteFile(filepath.Join(srv.home, "plugins", "pending", "dropped.py"),
		[]byte("DESCRIPTION = \"dropped in later\"\nSCHEMA = {\"type\": \"object\"}\n\ndef run(args):\n    return \"ok\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = doReq(t, h, "GET", "/api/plugins", nil, bearer(tok))
	if !strings.Contains(rec.Body.String(), "dropped") {
		t.Fatalf("live listing: %s, want the later-dropped file", rec.Body.String())
	}

	// a duplicate name in either zone is a named refusal, no overwrite.
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
	// a name colliding with the loaded zone is refused too.
	rec = post(`{"name":"loaded_one","description":"x","code":"return 1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("loaded collision: got %d, want 400", rec.Code)
	}

	// bad names: uppercase, a leading digit, a slash, empty.
	for _, bad := range []string{"Hello", "1abc", "a/b", "has space"} {
		rec = post(`{"name":"` + bad + `","description":"d","code":"return 1"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad name %q: got %d, want 400", bad, rec.Code)
		}
	}
	// an empty code is a refusal (a run with no body is no plugin).
	rec = post(`{"name":"emptybody","description":"d","code":"  \n"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code: got %d, want 400", rec.Code)
	}

	// the walls hold as with the todo create.
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

// --- the static assets are reachable and served ---

func TestStaticAssets(t *testing.T) {
	srv, tok := newTestServer(t)
	h := srv.Handler()

	// the page, its assets, and this round's surfaces (SPEC_SERVE phase 2:
	// the mobile drawer, the new-workspace picker, the TUI-homage
	// renderers for the todo and scheduler text).
	for path, wants := range map[string][]string{
		"/": {"<!doctype html", `id="nav-toggle"`, `id="cwd-add"`},
		"/static/app.js": {
			"renderSessions",
			"parseTodo",
			"parseScheduler",
			"progressBar",
			"addCwd",
			"setNavOpen",
		},
		"/static/style.css": {"--accent", "@media (max-width: 720px)", ".nav-open"},
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
	// an unknown static file is a 404.
	rec := doReq(t, h, "GET", "/static/nope.js", nil, bearer(tok))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown static: got %d, want 404", rec.Code)
	}
	// a path traversal is refused (not a 200).
	rec = doReq(t, h, "GET", "/static/../web.go", nil, bearer(tok))
	if rec.Code == http.StatusOK {
		t.Fatalf("traversal: got 200, want a refusal")
	}
}
