package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store/state"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

func writeFakeCrontab(t *testing.T, binDir, spool string) {
	t.Helper()
	script := `#!/bin/sh
SPOOL="` + spool + `"
case "${1:-}" in
  -l)
    if [ -f "$SPOOL" ]; then /bin/cat "$SPOOL"; else echo "no crontab for $(id -un)"; exit 1; fi
    ;;
  -)
    /bin/cat > "$SPOOL"
    ;;
  *)
    echo "crontab: unknown option" >&2; exit 2
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "crontab"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func newSwapServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":[{"id":"local","status":{"value":"unloaded"}}]}`))
	})
	mux.HandleFunc("/running", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"running":[]}`))
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

type fakeCrontabTest struct {
	mu   sync.Mutex
	text string
}

func (f *fakeCrontabTest) List() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text, nil
}

func (f *fakeCrontabTest) Install(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.text = text
	return nil
}

func newFakeCrontab() *fakeCrontabTest {
	return &fakeCrontabTest{text: "SHELL=/bin/bash\n"}
}

func (f *fakeCrontabTest) text_() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text
}

func scratchStores(t *testing.T, home, cwd string) sched.DB {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	db, _, _, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

var fixedNow = func() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) }

func countRows(t *testing.T, db sched.DB, query string) int {
	t.Helper()
	var n int
	if err := db.DB.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func containsStr(hay, needle string) bool { return strings.Contains(hay, needle) }

func TestRunJobColdShellFiresAndRecords(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	binDir := t.TempDir()
	workDir := t.TempDir()
	spool := filepath.Join(scratch, "spool")

	bin := filepath.Join(binDir, "rig")
	if out, err := exec.Command("go", "build", "-o", bin, filepath.Join(root, "cmd", "rig")).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	writeFakeCrontab(t, binDir, spool)

	swap := newSwapServer(t)

	home := filepath.Join(scratch, ".rig", "scheduler")
	fake := newFakeCrontab()
	st := scratchStores(t, home, "/ws/e2e")
	reply, err := sched.Create(context.Background(), st, fake, sched.CreateInput{
		Name: "e2e", Prompt: "say hi", Cron: "0 5 * * *",
		Cwd: workDir, Model: "local", Busy: "skip",
	}, "/ws/e2e", "sess-e2e", bin+" run-job", fixedNow)
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	key := "j1"
	if !containsStr(fake.text_(), "0 5 * * * "+bin+" run-job "+key) {
		t.Fatalf("crontab line missing: %q", fake.text_())
	}
	if err := os.WriteFile(spool, []byte(fake.text_()), 0o644); err != nil {
		t.Fatal(err)
	}
	sandboxOff(t, scratch)

	cmd := exec.Command(bin, "run-job", key)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+scratch,
		"XDG_CONFIG_HOME="+scratch,
		"RIG_SWAP_URL="+swap.URL,
		"RIG_BASE_URL=http://127.0.0.1:1/v1",
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("run-job exited non-zero (recorded outcomes exit 0): %v\n%s", runErr, out)
	}

	if runs := countRows(t, st, `SELECT count(*) FROM runs`); runs != 1 {
		t.Fatalf("runs rows = %d, want 1", runs)
	}
	var status string
	var exit any
	if err := st.DB.QueryRow(`SELECT last_status, last_exit FROM jobs WHERE id = 'j1'`).Scan(&status, &exit); err != nil {
		t.Fatal(err)
	}
	if status != "fail" {
		t.Fatalf("last_status %q, want fail", status)
	}
	if exit == nil || exit.(int64) == 0 {
		t.Fatalf("last_exit %v, want non-zero", exit)
	}

	logDir := filepath.Join(home, "runs", "j1")
	entries, err := filepath.Glob(filepath.Join(logDir, "*.log"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("run log entries = %v (%v)", entries, err)
	}
	log, err := os.ReadFile(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"== stdout ==", "rig: fault:", "key=j1"} {
		if !containsStr(string(log), want) {
			t.Fatalf("run log missing %q:\n%s", want, log)
		}
	}
}

func TestRowResolutionRefusalIsLoudBeforeStores(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "rig")
	if out, err := exec.Command("go", "build", "-o", bin, filepath.Join(root, "cmd", "rig")).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "-p", "hi", "-model", "nope")
	cmd.Dir = t.TempDir()

	cmd.Env = rigEnv(t.TempDir(), "")
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("an unknown model with no env must refuse: %q", out)
	}
	if !strings.Contains(string(out), `no row for "nope"`) || !strings.Contains(string(out), "known: local") {
		t.Fatalf("the refusal must name the id and the known ids: %q", out)
	}
}

func TestOneShotCompactsAndRecoversMidTurn(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	binDir := t.TempDir()
	workDir := t.TempDir()
	bin := filepath.Join(binDir, "rig")
	if out, err := exec.Command("go", "build", "-o", bin, filepath.Join(root, "cmd", "rig")).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		var sys, hasSummary string
		hasTool := false
		for _, m := range req.Messages {
			switch {
			case m.Role == "system":
				sys = m.Content
			case m.Role == "tool":
				hasTool = true
			case m.Role == "user" && strings.HasPrefix(m.Content, "[compaction] "):
				hasSummary = m.Content
			}
		}
		switch {
		case strings.HasPrefix(sys, "You write summaries of agent transcripts"):
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"SUM\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n")
		case hasSummary != "":
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"the answer\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n")
		case hasTool:
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"prompt is too long: context length exceeded"}}`)
		default:
			io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"id":"c1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"head -c 3200 /dev/zero\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n")
		}
	}))
	t.Cleanup(srv.Close)

	cmd := exec.Command(bin, "-p", strings.Repeat("p", 6000), "-model", "e2e", "-base-url", srv.URL+"/v1")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"HOME="+scratch,
		"XDG_CONFIG_HOME="+scratch,
		"RIG_MODEL_WINDOW=4000",
		"RIG_MODEL_RESERVE=100",
		"RIG_MODEL_KEEP_RECENT=1000",
		"RIG_MODEL_MAX_TOKENS=500",
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("one-shot must exit 0 after the recovery: %v\n%s", runErr, out)
	}
	if !strings.Contains(string(out), "the answer") {
		t.Fatalf("the worker stdout must be the final answer only: %q", out)
	}
	if strings.Contains(string(out), "fault") {
		t.Fatalf("the swallowed fault must never reach stdout: %q", out)
	}

	glob, _ := filepath.Glob(filepath.Join(scratch, ".rig", "sessions", "*.sqlite"))
	if len(glob) != 1 {
		t.Fatalf("sessions store = %v, want one", glob)
	}
	db, _, _, err := store.Open(glob[0], state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	var n int
	if err := db.DB.QueryRow(`SELECT count(*) FROM messages WHERE role = 'user' AND content LIKE '[compaction] %'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("summary row = %d (%v), want 1", n, err)
	}
	var sumSeq int64
	if err := db.DB.QueryRow(`SELECT seq FROM messages WHERE content LIKE '[compaction] %'`).Scan(&sumSeq); err != nil {
		t.Fatal(err)
	}
	var u int
	if err := db.DB.QueryRow(`SELECT count(*) FROM usage WHERE message_seq = $1`, sumSeq).Scan(&u); err != nil || u != 1 {
		t.Fatalf("summary usage = %d (%v), want 1", u, err)
	}
	var exit string
	if err := db.DB.QueryRow(`SELECT exit FROM sessions`).Scan(&exit); err != nil || exit != "ok" {
		t.Fatalf("session exit = %q (%v), want ok", exit, err)
	}
}
