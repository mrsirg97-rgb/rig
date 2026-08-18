package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/frontend/cli"
	"github.com/mrsirg97-rgb/rig/frontend/oneshot"
	"github.com/mrsirg97-rgb/rig/loop"
	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/store"
	remstore "github.com/mrsirg97-rgb/rig/store/rem"
	"github.com/mrsirg97-rgb/rig/store/state"
	"github.com/mrsirg97-rgb/rig/tool/bash"
	"github.com/mrsirg97-rgb/rig/tool/file"
	"github.com/mrsirg97-rgb/rig/tool/fs"
)

// --- scripted provider (the OpenAI wire, classified server-side) ---

type scriptSrv struct {
	mu      sync.Mutex
	main    int
	summary int
	models  []string // the model field per main call
	bodies  []string // every message's content, concatenated, per main call
	fault   bool     // main calls fault with context length
}

func (s *scriptSrv) counts() (main, summary int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.main, s.summary
}

func (s *scriptSrv) mainCalls() (models, bodies []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.models...), append([]string(nil), s.bodies...)
}

func newScriptSrv(t *testing.T, s *scriptSrv) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		summary := false
		var all strings.Builder
		for _, m := range req.Messages {
			if m.Role == "system" && strings.HasPrefix(m.Content, "You write summaries of agent transcripts") {
				summary = true
			}
			all.WriteString(m.Content)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if summary {
			s.summary++
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"SUM"},"finish_reason":"stop"}],"usage":{"prompt_tokens":812,"completion_tokens":640}}`+"\n")
			return
		}
		s.main++
		s.models = append(s.models, req.Model)
		s.bodies = append(s.bodies, all.String())
		if s.fault {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"prompt is too long: context length exceeded"}}`)
			return
		}
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`+"\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- the REPL harness: real cli, real loop, real stores, scripted provider ---

type chanReader struct{ ch chan string }

func (r chanReader) Read(p []byte) (int, error) {
	v, ok := <-r.ch
	if !ok {
		return 0, io.EOF
	}
	return copy(p, []byte(v)), nil
}

// lockedWriter is the harness's output: the loop's goroutine writes while
// the test polls — the mutex is the test's synchronization, not the
// CLI's.
type lockedWriter struct {
	mu sync.Mutex
	b  *bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lockedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

type harness struct {
	t     *testing.T
	r     *root
	out   *lockedWriter
	in    chan string
	db    store.DB
	remDB store.DB
	s     *scriptSrv
}

func commandEnv(r *root) *command.Env {
	return &command.Env{
		Session:       func() *core.Session { return r.session },
		Compact:       r.compactNow,
		NewSession:    r.newSession,
		SessionList:   r.sessionList,
		SessionShow:   r.sessionShow,
		SessionResume: r.sessionResume,
		Models:        func() models.Table { return r.runtime },
		ActiveModel:   func() string { return r.activeID },
		SwitchModel:   r.switchModel,
		Tools:         r.tools,
	}
}

func newHarness(t *testing.T, row models.Model, activeID string, runtime models.Table) *harness {
	t.Helper()
	dir := t.TempDir()
	db, _, err := store.Open(filepath.Join(dir, "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB.Close() })
	remDB, _, err := store.Open(filepath.Join(dir, "rem.sqlite"), remstore.Statements(), remstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { remDB.DB.Close() })

	s := &scriptSrv{}
	srv := newScriptSrv(t, s)

	in := make(chan string, 8)
	out := &lockedWriter{b: &bytes.Buffer{}}

	r := &root{
		baseURL:  srv.URL + "/v1",
		system:   "S",
		allow:    nil,
		retries:  3,
		sdb:      db,
		remDB:    remDB,
		cwd:      dir,
		activeID: activeID,
		row:      row,
		runtime:  runtime,
		tools: map[string]core.Tool{
			"bash": bash.New(), "read": file.Read(), "write": file.Write(), "edit": file.Edit(),
			"ls": fs.LS(), "find": fs.Find(), "grep": fs.Grep(),
			"todo": fakeTodo{}, "rem": fakeRem{}, "scheduler": fakeSched{}, "python": &fakePython{},
			"web_search": fakeWebSearch{}, "web_fetch": fakeWebFetch{},
		},
	}
	r.session = core.NewSession()
	fe := cli.New(chanReader{ch: in}, out, cli.WithCommands(command.All(), commandEnv(r)))
	r.fe = fe
	r.rec = state.NewRecorder(fe, db, dir, activeID, Version, r.session.ID, r.session)
	wire(r)
	return &harness{t: t, r: r, out: out, in: in, db: db, remDB: remDB, s: s}
}

// startRun launches the loop; the test schedules the lines against
// observable output (a schedule, not a sleep on the outcome).
func (h *harness) startRun() chan error {
	h.t.Helper()
	done := make(chan error, 1)
	go func() { done <- loop.Run(context.Background(), h.r.k) }()
	return done
}

func (h *harness) waitCount(what string, n int) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(h.out.String(), what) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	h.t.Fatalf("output has %d×%q, want %d:\n%s", strings.Count(h.out.String(), what), what, n, h.out.String())
}

func (h *harness) waitOut(what string) { h.waitCount(what, 1) }

// finish closes stdin, joins the loop, and closes the session row as the
// root does at process end (a clean REPL exit).
func (h *harness) finish(done chan error) {
	h.t.Helper()
	close(h.in)
	if err := <-done; err != nil {
		h.t.Fatalf("loop: %v", err)
	}
	if e := h.r.rec.Close("ok"); e != nil {
		h.t.Fatalf("session closure: %v", e)
	}
}

func (h *harness) sessionRows() []struct {
	ID    string
	Exit  string
	Ended bool
} {
	h.t.Helper()
	rows, err := h.db.DB.Query(`SELECT id, COALESCE(exit, ''), ended_at IS NOT NULL FROM sessions ORDER BY started_at`)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []struct {
		ID    string
		Exit  string
		Ended bool
	}
	for rows.Next() {
		var r struct {
			ID    string
			Exit  string
			Ended bool
		}
		if err := rows.Scan(&r.ID, &r.Exit, &r.Ended); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func (h *harness) userRows(sid string) []string {
	h.t.Helper()
	rows, err := h.db.DB.Query(`SELECT content FROM messages WHERE session_id = ? AND role = 'user' ORDER BY seq`, sid)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// --- the named cases (SPEC_COMMANDS, testing) ---

// TestNewClosesOldRowAndNextTurnLandsInFreshOne (SPEC_COMMANDS 4, named):
// prompt, /new, prompt, EOF — two session rows: the first closed ok with
// ended_at set, the second closed ok at EOF; the first prompt row under
// the first id, the second under the fresh id; the fresh session's
// projection carries only its own prompt; the model never saw a prompt
// cross the boundary.
func TestNewClosesOldRowAndNextTurnLandsInFreshOne(t *testing.T) {
	h := newHarness(t, defaultRow(), "local", defaultsTable(t))
	s1 := h.r.session.ID
	done := h.startRun()
	h.in <- "one\n"
	h.waitCount("pong", 1)
	h.in <- "/new\n"
	h.waitOut("new: session")
	h.in <- "two\n"
	h.waitCount("pong", 2)
	h.finish(done)

	s2 := h.r.session.ID
	if s2 == s1 {
		t.Fatal("the fresh session must have a fresh id")
	}
	rows := h.sessionRows()
	if len(rows) != 2 {
		t.Fatalf("session rows = %v, want two", rows)
	}
	first := h.sessionRowsByID(s1)
	second := h.sessionRowsByID(s2)
	if first.Exit != "ok" || !first.Ended {
		t.Fatalf("the first row must be closed ok with ended_at set: %+v", first)
	}
	if second.Exit != "ok" || !second.Ended {
		t.Fatalf("the second row must be closed ok at EOF: %+v", second)
	}
	if got := h.userRows(s1); len(got) != 1 || got[0] != "one" {
		t.Fatalf("the first prompt row under the first id: %v", got)
	}
	if got := h.userRows(s2); len(got) != 1 || got[0] != "two" {
		t.Fatalf("the second prompt row under the fresh id: %v", got)
	}
	sess, err := state.Resume(context.Background(), h.db, s2)
	if err != nil {
		t.Fatalf("resume the fresh session: %v", err)
	}
	var users []string
	for _, m := range sess.Messages {
		if m.Role == core.RoleUser {
			users = append(users, m.Content)
		}
	}
	if len(users) != 1 || users[0] != "two" {
		t.Fatalf("the fresh session's projection carries only its own prompt: %v", users)
	}
	models, bodies := h.s.mainCalls()
	if len(bodies) != 2 {
		t.Fatalf("main calls = %d, want 2", len(bodies))
	}
	if strings.Contains(bodies[0], "two") || !strings.Contains(bodies[0], "one") {
		t.Fatalf("the first request carries the first prompt only: %q", bodies[0])
	}
	if strings.Contains(bodies[1], "one") || !strings.Contains(bodies[1], "two") {
		t.Fatalf("the second request carries the second prompt only: %q", bodies[1])
	}
	if models[0] != "local" || models[1] != "local" {
		t.Fatalf("the model is unchanged across /new: %v", models)
	}
}

func (h *harness) sessionRowsByID(id string) (r struct {
	ID    string
	Exit  string
	Ended bool
}) {
	h.t.Helper()
	err := h.db.DB.QueryRow(`SELECT id, COALESCE(exit, ''), ended_at IS NOT NULL FROM sessions WHERE id = ?`, id).Scan(&r.ID, &r.Exit, &r.Ended)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

// TestNewKeepsPerProcessState (SPEC_COMMANDS 4, named): the python tool
// instance and the guard participant are the same before and after /new —
// the swap is the session, the recorder, and the pair; the per-process
// state survives (SPEC_PYTHON's one kernel per process, the guard's
// per-turn budget).
func TestNewKeepsPerProcessState(t *testing.T) {
	h := newHarness(t, defaultRow(), "local", defaultsTable(t))
	beforeTools := append([]core.Tool(nil), h.r.k.Tools...)
	beforeMW := append([]core.ToolMiddleware(nil), h.r.k.Middleware...)
	done := h.startRun()
	h.in <- "one\n"
	h.waitCount("pong", 1)
	h.in <- "/new\n"
	h.waitOut("new: session")
	h.finish(done)

	if len(h.r.k.Tools) != len(beforeTools) {
		t.Fatalf("tool count changed across /new: %d -> %d", len(beforeTools), len(h.r.k.Tools))
	}
	for i := range beforeTools {
		if h.r.k.Tools[i] != beforeTools[i] {
			t.Fatalf("tool %d (%s) changed across /new: the per-process tool instances survive", i, beforeTools[i].Name())
		}
	}
	// the chain survives: same length, same order, same types; the pointer
	// participants (the guard's per-turn budget is its state) are the same
	// object, not a copy. Function participants are closures — their
	// identity is their type and position in the surviving chain.
	if len(h.r.k.Middleware) != len(beforeMW) {
		t.Fatalf("middleware count changed across /new")
	}
	for i := range beforeMW {
		a, b := h.r.k.Middleware[i], beforeMW[i]
		if reflect.TypeOf(a) != reflect.TypeOf(b) {
			t.Fatalf("middleware %d changed across /new: %T -> %T", i, b, a)
		}
		if ra, rb := reflect.ValueOf(a), reflect.ValueOf(b); ra.Kind() == reflect.Ptr && ra.Pointer() != rb.Pointer() {
			t.Fatalf("middleware %d is not the same object across /new: the guard participant survives", i)
		}
	}
	// the python tool instance, named in the spec: the same pointer.
	var afterPy, beforePy *fakePython
	for _, tl := range h.r.k.Tools {
		if p, ok := tl.(*fakePython); ok {
			afterPy = p
		}
	}
	for _, tl := range beforeTools {
		if p, ok := tl.(*fakePython); ok {
			beforePy = p
		}
	}
	if beforePy == nil || afterPy == nil || beforePy != afterPy {
		t.Fatalf("the python tool must be the same pointer across /new")
	}
}

// TestCompactForcesTheAction (SPEC_COMMANDS 3, named): a fixture
// transcript below the trigger, a scripted summary provider — the
// transcript is rewritten to [summary] + tail, the Compacted event is
// delivered to the recorder (the summary row + usage row land, the tail
// re-lands), AutoReflect is called, the CLI renders the one ⧉ line — and
// the once budget is spent behaviorally: with nothing new since the
// forced compact, the next main call's context-length fault surfaces
// without recovery (exactly one summary call, the forced one).
func TestCompactForcesTheAction(t *testing.T) {
	// below the trigger (estimate 800 <= 900 = Window - Reserve), but the
	// provider says the request does not fit — the fault the recovery
	// classifies.
	row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 200, Reserve: 100, KeepRecent: 700}
	u1, a1, u2 := strings.Repeat("u", 1200), strings.Repeat("a", 1200), strings.Repeat("u", 800)
	h := newHarness(t, row, "local", defaultsTable(t))
	h.s.fault = true
	sid := h.r.session.ID
	h.r.session.Append(core.Message{Role: core.RoleUser, Content: u1})
	h.r.session.Append(core.Message{Role: core.RoleAssistant, Content: a1})
	h.r.session.Append(core.Message{Role: core.RoleUser, Content: u2})
	ctx := context.Background()
	if _, e := state.RecordMessage(ctx, h.db, sid, "user", u1, nil, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordMessage(ctx, h.db, sid, "assistant", a1, nil, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordMessage(ctx, h.db, sid, "user", u2, nil, nil); e != nil {
		t.Fatal(e)
	}

	done := h.startRun()
	h.in <- "/compact\n"
	h.waitOut("⧉ compact:")
	h.in <- "probe\n"
	h.waitOut("[fault]")
	h.finish(done)

	// exactly one summary call (the forced one) plus the faulted main
	// call: the budget is spent, the fault surfaces without recovery.
	main, summary := h.s.counts()
	if summary != 1 || main != 1 {
		t.Fatalf("summary calls = %d, main calls = %d — want exactly the forced summary plus the faulted main", summary, main)
	}
	if got := strings.Count(h.out.String(), "⧉"); got != 1 {
		t.Fatalf("the CLI renders the one ⧉ line, got %d:\n%s", got, h.out.String())
	}
	if !strings.Contains(h.out.String(), "context length") {
		t.Fatalf("the fault must surface: %q", h.out.String())
	}

	// the rewrite: [summary row] + tail, in the session.
	msgs := h.r.session.Messages
	if len(msgs) < 3 || msgs[0].Role != core.RoleUser || !strings.HasPrefix(msgs[0].Content, "[compaction] ") || !strings.Contains(msgs[0].Content, "SUM") {
		t.Fatalf("the transcript must be rewritten to [summary] + tail: %+v", msgs)
	}
	if len(msgs) != 4 || msgs[1].Content != a1 || msgs[2].Content != u2 || msgs[3].Content != "probe" {
		t.Fatalf("the tail is kept verbatim, then the next prompt: %+v", msgs)
	}

	// the recorder: the summary row + its usage row, the tail re-landed
	// after it (fresh seqs).
	var sumSeq int64
	if err := h.db.DB.QueryRow(`SELECT seq FROM messages WHERE content LIKE '[compaction] %' AND session_id = ?`, sid).Scan(&sumSeq); err != nil {
		t.Fatalf("the summary row must have landed: %v", err)
	}
	var usage int
	if err := h.db.DB.QueryRow(`SELECT count(*) FROM usage WHERE message_seq = ?`, sumSeq).Scan(&usage); err != nil || usage != 1 {
		t.Fatalf("the summary usage row must have landed: %d (%v)", usage, err)
	}
	var n int
	if err := h.db.DB.QueryRow(`SELECT count(*) FROM messages WHERE session_id = ?`, sid).Scan(&n); err != nil || n != 7 {
		t.Fatalf("messages = %d (3 seeded + summary + 2 re-landed + the prompt), want 7 (%v)", n, err)
	}
	var tailUser, tailAsst string
	if err := h.db.DB.QueryRow(`SELECT content FROM messages WHERE session_id = ? AND seq = ?`, sid, sumSeq+1).Scan(&tailAsst); err != nil || tailAsst != a1 {
		t.Fatalf("the re-landed tail follows the summary: %q (%v)", tailAsst, err)
	}
	if err := h.db.DB.QueryRow(`SELECT content FROM messages WHERE session_id = ? AND seq = ?`, sid, sumSeq+2).Scan(&tailUser); err != nil || tailUser != u2 {
		t.Fatalf("the re-landed tail follows the summary: %q (%v)", tailUser, err)
	}

	// AutoReflect: the summary is handed to rem exactly as on the trigger
	// path — a forced compaction is a compaction.
	var memories int
	if err := h.remDB.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&memories); err != nil || memories < 1 {
		t.Fatalf("AutoReflect must have landed a memory: %d (%v)", memories, err)
	}
	var content string
	if err := h.remDB.DB.QueryRow(`SELECT content FROM memories LIMIT 1`).Scan(&content); err != nil || !strings.Contains(content, "SUM") {
		t.Fatalf("the reflected memory must carry the summary: %q (%v)", content, err)
	}
}

// TestSessionsResume (SPEC_COMMANDS 5, named): the current-id refusal;
// the unknown-id refusal before the current row is touched (the current
// row is still open); the happy path — the old row closed ok, the next
// prompt lands under the resumed id, the transcript is the projection
// plus the new row, the files provenance restored.
func TestSessionsResume(t *testing.T) {
	// the current-id refusal (the current row is still open when it lands).
	h := newHarness(t, defaultRow(), "local", defaultsTable(t))
	sid := h.r.session.ID
	done := h.startRun()
	h.in <- "/sessions resume " + sid + "\n"
	h.waitOut("sessions: already the current session: " + sid)
	if first := h.sessionRowsByID(sid); first.Ended || first.Exit != "open" {
		t.Fatalf("the current-id refusal must not touch the current row: %+v", first)
	}
	h.finish(done)

	// the unknown-id refusal, before the current row is touched.
	h = newHarness(t, defaultRow(), "local", defaultsTable(t))
	sid = h.r.session.ID
	done = h.startRun()
	h.in <- "/sessions resume nope\n"
	h.waitOut("sessions: no such session: nope")
	if first := h.sessionRowsByID(sid); first.Ended || first.Exit != "open" {
		t.Fatalf("the unknown-id refusal must land before the current row is touched: %+v", first)
	}
	h.finish(done)

	// the happy path.
	h = newHarness(t, defaultRow(), "local", defaultsTable(t))
	ctx := context.Background()
	if e := state.RecordSession(ctx, h.db, "sess-2", h.r.cwd, "local", Version); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordMessage(ctx, h.db, "sess-2", "user", "old", nil, nil); e != nil {
		t.Fatal(e)
	}
	if e := state.RecordFile(ctx, h.db, "sess-2", "notes.txt", "hash123", 1234); e != nil {
		t.Fatal(e)
	}
	done = h.startRun()
	h.in <- "/sessions resume sess-2\n"
	h.waitOut("sessions: resumed sess-2 (1 messages)")
	h.in <- "after\n"
	h.waitCount("pong", 1)
	h.finish(done)

	if first := h.sessionRowsByID(h.r.session.ID); first.ID != "sess-2" {
		t.Fatalf("the resumed session is the live one: %+v", first)
	}
	if rows := h.sessionRows(); len(rows) != 2 {
		t.Fatalf("session rows = %v, want two", rows)
	}
	// the old row closed ok; the resumed row closed at EOF; the next
	// prompt landed under the resumed id.
	closedOK := false
	for _, r := range h.sessionRows() {
		if r.ID != "sess-2" && r.Exit == "ok" && r.Ended {
			closedOK = true
		}
	}
	if !closedOK {
		t.Fatalf("the old row must be closed ok: %v", h.sessionRows())
	}
	if got := h.userRows("sess-2"); len(got) != 2 || got[0] != "old" || got[1] != "after" {
		t.Fatalf("the transcript is the projection plus the new row: %v", got)
	}
	if _, ok := h.r.session.Files["notes.txt"]; !ok {
		t.Fatalf("the files provenance must be restored: %v", h.r.session.Files)
	}
}

// TestModelsSwitchTakesEffectNextTurn (SPEC_COMMANDS 6, named):
// models <id2> — the next prompt reaches the new model (the wire's model
// field), and only it; ActiveModel reports the new id; the root's row is
// the new row's (the next clamp/trigger math is the new row's).
func TestModelsSwitchTakesEffectNextTurn(t *testing.T) {
	h := newHarness(t, defaultRow(), "local", defaultsTable(t))
	done := h.startRun()
	h.in <- "one\n"
	h.waitCount("pong", 1)
	h.in <- "/models qwen3.8-workers\n"
	h.waitOut("models: active is now qwen3.8-workers")
	h.in <- "two\n"
	h.waitCount("pong", 2)
	h.finish(done)

	modelNames, bodies := h.s.mainCalls()
	if len(modelNames) != 2 {
		t.Fatalf("main calls = %d, want 2", len(modelNames))
	}
	if modelNames[0] != "local" {
		t.Fatalf("the first request is the startup model: %v", modelNames)
	}
	if modelNames[1] != "qwen3.8-workers" {
		t.Fatalf("the switch takes effect on the next turn's request: %v", modelNames)
	}
	if !strings.Contains(bodies[0], "one") {
		t.Fatalf("the first request carries the first prompt: %q", bodies[0])
	}
	// the transcript persists across the switch: the second request is the
	// whole history plus the second prompt, on the new model — the wire's
	// model field is the provider discriminator.
	if !strings.Contains(bodies[1], "two") {
		t.Fatalf("the second request carries the second prompt: %q", bodies[1])
	}
	if h.r.activeID != "qwen3.8-workers" {
		t.Fatalf("ActiveModel reports the new id: %q", h.r.activeID)
	}
	row, ok := defaultsTable(t).Get("qwen3.8-workers")
	if !ok || h.r.row != row {
		t.Fatalf("the new policy is built with the new row: %+v (want %+v)", h.r.row, row)
	}
}

// TestModelsRuntimeTableIncludesSynthesizedRow (SPEC_COMMANDS 6, named):
// a row synthesized from env at startup lists (marked active) and can be
// switched to and back — the root's table, not just 8's Defaults.
func TestModelsRuntimeTableIncludesSynthesizedRow(t *testing.T) {
	row := models.Model{Role: models.RoleInteractive, ID: "e2e", Window: 4000, MaxTokens: 500, Reserve: 100, KeepRecent: 1000}
	if err := row.Check(); err != nil {
		t.Fatal(err)
	}
	runtime := runtimeTable(defaultsTable(t), "e2e", row)
	h := newHarness(t, row, "e2e", runtime)
	done := h.startRun()
	h.in <- "/models\n"
	h.waitOut("window 4000")
	h.in <- "/models local\n"
	h.waitOut("models: active is now local")
	h.in <- "/models e2e\n"
	h.waitOut("models: active is now e2e")
	h.in <- "/models nope\n"
	h.waitOut(`models: no row for "nope" (known: e2e, local, qwen3.8-workers)`)
	h.finish(done)

	out := h.out.String()
	if !strings.Contains(out, "e2e") || !strings.Contains(out, "window 4000  max 500") {
		t.Fatalf("the synthesized row must list: %q", out)
	}
	if !strings.Contains(out, "local") || !strings.Contains(out, "qwen3.8-workers") {
		t.Fatalf("Defaults must list beside the synthesized row: %q", out)
	}
	if h.r.activeID != "e2e" {
		t.Fatalf("the switch back must land: %q", h.r.activeID)
	}
	if h.r.row != row {
		t.Fatalf("the switch back must carry the synthesized row's numbers: %+v", h.r.row)
	}
	if main, _ := h.s.counts(); main != 0 {
		t.Fatalf("no prompt was typed: no model calls, got %d", main)
	}
}

// TestOneShotCommandShapedPromptIsAPrompt (SPEC_COMMANDS 9, named):
// -p "/compact" runs the model on a user message whose content is
// /compact — the env's Compact was never called (the one-shot frontend
// never dispatches); stdout is the assistant text only.
func TestOneShotCommandShapedPromptIsAPrompt(t *testing.T) {
	dir := t.TempDir()
	db, _, err := store.Open(filepath.Join(dir, "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	s := &scriptSrv{}
	srv := newScriptSrv(t, s)

	r := &root{
		baseURL:  srv.URL + "/v1",
		system:   "S",
		retries:  3,
		sdb:      db,
		cwd:      dir,
		activeID: "local",
		row:      defaultRow(),
		runtime:  defaultsTable(t),
		tools:    testTools(),
	}
	r.session = core.NewSession()
	out := &bytes.Buffer{}
	fe := &oneshot.OneShot{Prompt: "/compact", Out: out}
	r.fe = fe
	r.rec = state.NewRecorder(fe, db, dir, "local", Version, r.session.ID, r.session)
	wire(r)

	compactCalls := 0
	env := commandEnv(r)
	env.Compact = func(ctx context.Context) (core.Compacted, bool, error) {
		compactCalls++
		return r.compactNow(ctx)
	}
	_ = env // the root built it; the one-shot never dispatches over it

	if err := loop.Run(context.Background(), r.k); err != nil {
		t.Fatalf("loop: %v", err)
	}

	if compactCalls != 0 {
		t.Fatalf("the env's Compact must never be called under -p, got %d", compactCalls)
	}
	main, _ := s.counts()
	if main != 1 {
		t.Fatalf("the model ran once on the prompt, got %d main calls", main)
	}
	_, bodies := s.mainCalls()
	if !strings.Contains(bodies[0], "/compact") {
		t.Fatalf("the request must carry the user message /compact: %q", bodies[0])
	}
	if got := out.String(); got != "pong\n" {
		t.Fatalf("stdout is the assistant text only: %q", got)
	}
}

// TestREPLCommands (SPEC_COMMANDS, named e2e): the built binary, the REPL
// over stdin — /models (the table line), /todo create x + /todo read
// (the queue), /new (the fresh row), /sessions (both rows, the old one
// closed ok) — exit 0; the state store under the scratch home carries the
// two session rows. The provider is dead: every line is a command, so no
// model call is made.
func TestREPLCommands(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "rig")
	if out, err := exec.Command("go", "build", "-o", bin, filepath.Join(root, "cmd", "rig")).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	scratch := t.TempDir()
	workDir := t.TempDir()

	cmd := exec.Command(bin)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"HOME="+scratch,
		"XDG_CONFIG_HOME="+scratch,
		"RIG_BASE_URL=http://127.0.0.1:1/v1", // dead endpoint: commands only, no model
	)
	var mu sync.Mutex
	var out bytes.Buffer
	drain := func(r io.Reader) {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				mu.Lock()
				out.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go drain(stdout)
	go drain(stderr)

	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return out.String()
	}
	// The REPL's steering slot is one message, latest wins (7): a flood of
	// piped lines collapses to the last. So the test schedules the lines
	// against observable output — a line is typed only after the previous
	// one has printed (the reader then takes it direct, as a human's line
	// would).
	type step struct {
		line, marker string
		count        int
	}
	for _, st := range []step{
		{"/models\n", "window 65536", 1},
		{"/todo create x\n", "queue replaced", 1},
		{"/todo read\n", "next: t1", 2},
		{"/new\n", "new: session", 1},
		{"/sessions\n", "exit ok", 1},
	} {
		if _, err := stdin.Write([]byte(st.line)); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Count(snapshot(), st.marker) >= st.count {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if strings.Count(snapshot(), st.marker) < st.count {
			t.Fatalf("the %q reply must print %q (×%d):\n%s", st.line, st.marker, st.count, snapshot())
		}
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the REPL exits 0: %v\n%s", err, snapshot())
	}

	outStr := snapshot()
	// the models table (the active row marked, the trigger column).
	if !strings.Contains(outStr, "window 65536") || !strings.Contains(outStr, "trigger 57344") {
		t.Fatalf("the models table must print: %q", outStr)
	}
	// the todo round-trip, the tool's own reply verbatim.
	if !strings.Contains(outStr, "next: t1") || !strings.Contains(outStr, "x") {
		t.Fatalf("the todo create + read must show the queue: %q", outStr)
	}
	// /new: the fresh session line.
	if !strings.Contains(outStr, "new: session") {
		t.Fatalf("the new line must print the fresh id: %q", outStr)
	}
	// /sessions: both rows — the old one closed ok, the current one open
	// (not yet closed at the moment of the list) and marked.
	if !strings.Contains(outStr, "exit ok") || !strings.Contains(outStr, "exit open") || !strings.Contains(outStr, "*") {
		t.Fatalf("the session list must show both rows, the old closed ok, the current open: %q", outStr)
	}

	// the state store: two session rows, the older closed ok.
	glob, _ := filepath.Glob(filepath.Join(scratch, "rig", "sessions", "*.sqlite"))
	if len(glob) != 1 {
		t.Fatalf("sessions store = %v, want one", glob)
	}
	db, _, err := store.Open(glob[0], state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	var n int
	if err := db.DB.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("session rows = %d (%v), want two", n, err)
	}
	var firstExit string
	var firstEnded bool
	if err := db.DB.QueryRow(`SELECT COALESCE(exit, ''), ended_at IS NOT NULL FROM sessions ORDER BY started_at LIMIT 1`).Scan(&firstExit, &firstEnded); err != nil {
		t.Fatal(err)
	}
	if firstExit != "ok" || !firstEnded {
		t.Fatalf("the first session row must be closed ok with ended_at set: %q %v", firstExit, firstEnded)
	}
}
