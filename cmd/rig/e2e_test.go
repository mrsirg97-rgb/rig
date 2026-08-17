package main

// The cold-shell end-to-end case: the built binary, driven through argv
// and env only, fires a job with every seam scripted — fake crontab on
// PATH, scratch config home, scripted swap endpoint, dead worker
// endpoint. The worker faults (no model), exits non-zero, and the run
// records as a failure with its log — the full record path through real
// processes.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

// --- scripted seams ---

func writeFakeCrontab(t *testing.T, binDir, spool string) {
	t.Helper()
	script := `#!/bin/sh
SPOOL="` + spool + `"
case "${1:-}" in
  -l)
    if [ -f "$SPOOL" ]; then cat "$SPOOL"; else echo "no crontab for $(id -un)"; exit 1; fi
    ;;
  -)
    cat > "$SPOOL"
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
		w.Write([]byte(`{"data":[{"id":"qwen3.8-workers","status":{"value":"unloaded"}}]}`))
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

func scratchStores(t *testing.T, home, cwd string) sched.Stores {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	global, _, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	key, err := sched.ParseKey("cwd-" + sched.CwdHash(cwd) + ":j1")
	if err != nil {
		t.Fatal(err)
	}
	cw, _, err := store.Open(sched.StorePathFor(home, key), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	return sched.Stores{Global: global, Cwd: cw}
}

var fixedNow = func() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) }

func countRows(t *testing.T, st sched.Stores, query string) int {
	t.Helper()
	var n int
	if err := st.Cwd.DB.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func containsStr(hay, needle string) bool { return strings.Contains(hay, needle) }

// --- the case ---

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

	// the job: created in-process through the real verb with scripted
	// seams, exactly as the agent-side tool would create it.
	home := filepath.Join(scratch, "rig", "scheduler")
	fake := newFakeCrontab()
	st := scratchStores(t, home, "/ws/e2e")
	reply, err := sched.Create(context.Background(), st, fake, sched.CreateInput{
		Name: "e2e", Prompt: "say hi", Cron: "0 5 * * *", Scope: "cwd",
		Cwd: workDir, Model: "qwen3.8-workers", Busy: "skip",
	}, "/ws/e2e", "sess-e2e", bin+" run-job", fixedNow)
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	key := "cwd-" + sched.CwdHash("/ws/e2e") + ":j1"
	if !containsStr(fake.text_(), "0 5 * * * "+bin+" run-job "+key) {
		t.Fatalf("crontab line missing: %q", fake.text_())
	}
	if err := os.WriteFile(spool, []byte(fake.text_()), 0o644); err != nil {
		t.Fatal(err)
	}

	// the cold shell: argv plus env only.
	cmd := exec.Command(bin, "run-job", key)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+scratch,
		"XDG_CONFIG_HOME="+scratch,
		"RIG_SWAP_URL="+swap.URL,
		"RIG_BASE_URL=http://127.0.0.1:1/v1", // dead endpoint: the worker faults
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("run-job exited non-zero (recorded outcomes exit 0): %v\n%s", runErr, out)
	}

	// the record: a failed run, logged, attributed to the job.
	if runs := countRows(t, st, `SELECT count(*) FROM runs`); runs != 1 {
		t.Fatalf("runs rows = %d, want 1", runs)
	}
	var status string
	var exit any
	if err := st.Cwd.DB.QueryRow(`SELECT last_status, last_exit FROM jobs WHERE id = 'j1'`).Scan(&status, &exit); err != nil {
		t.Fatal(err)
	}
	if status != "fail" {
		t.Fatalf("last_status %q, want fail", status)
	}
	if exit == nil || exit.(int64) == 0 {
		t.Fatalf("last_exit %v, want non-zero", exit)
	}

	// the worker's fault voice survives in the run log.
	logDir := filepath.Join(home, "runs", "cwd-"+sched.CwdHash("/ws/e2e"), "j1")
	entries, err := filepath.Glob(filepath.Join(logDir, "*.log"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("run log entries = %v (%v)", entries, err)
	}
	log, err := os.ReadFile(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"== stdout ==", "rig: fault:", "key=cwd-"} {
		if !containsStr(string(log), want) {
			t.Fatalf("run log missing %q:\n%s", want, log)
		}
	}
}
