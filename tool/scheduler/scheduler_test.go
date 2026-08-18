package scheduler_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	adapter "github.com/mrsirg97-rgb/rig/tool/scheduler"
)

// fakeCrontab: the surgical seam, mutex-guarded (the adapter tests drive
// single-threaded calls; the guard keeps future concurrency honest).
type fakeCrontab struct {
	mu   sync.Mutex
	text string
}

func (f *fakeCrontab) List() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text, nil
}

func (f *fakeCrontab) Install(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.text = text
	return nil
}

type harness struct {
	st   sched.Stores
	ct   *fakeCrontab
	tool core.Tool
}

func newHarness(t *testing.T, cwd string) *harness {
	return newHarnessModel(t, cwd, "qwen3.8-workers")
}

// newHarnessModel is the harness with an explicit default job model:
// the constructor's parameter the root passes (SPEC_CONFIG 5).
func newHarnessModel(t *testing.T, cwd, defModel string) *harness {
	t.Helper()
	home := t.TempDir()
	globalPath := filepath.Join(home, "global.sqlite")
	if _, _, err := store.Open(globalPath, sched.Statements(), sched.SchemaVersion); err != nil {
		t.Fatal(err)
	}
	key, err := sched.ParseKey("cwd-" + sched.CwdHash(cwd) + ":j1")
	if err != nil {
		t.Fatal(err)
	}
	cwdPath := sched.StorePathFor(home, key)
	if _, _, err := store.Open(cwdPath, sched.Statements(), sched.SchemaVersion); err != nil {
		t.Fatal(err)
	}
	var (
		gd store.DB
		cd store.DB
	)
	// re-open for live handles (the first Opens validated the schema)
	if gd, _, err = store.Open(globalPath, sched.Statements(), sched.SchemaVersion); err != nil {
		t.Fatal(err)
	}
	if cd, _, err = store.Open(cwdPath, sched.Statements(), sched.SchemaVersion); err != nil {
		t.Fatal(err)
	}
	st := sched.Stores{Global: gd, Cwd: cd}
	ct := &fakeCrontab{text: "SHELL=/bin/bash\n"}
	tool := adapter.New(st, ct, "/x/rig run-job", defModel)
	return &harness{st: st, ct: ct, tool: tool}
}

func exec(t *testing.T, h *harness, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return h.tool.Exec(context.Background(), raw)
}

func TestDescriptionCarriesTheVoices(t *testing.T) {
	h := newHarness(t, "/ws/sa")
	d := h.tool.Description()
	for _, want := range []string{
		"never reused - copy them from list, never invent",
		"busy:'skip' default; 'force' evicts",
		"ids jN are per scope",
		"Default model: qwen3.8-workers",
		"the drift note is gone",
		"re-create to retry",
	} {
		if !strings.Contains(d, want) {
			t.Fatalf("description missing voice fragment: %q", want)
		}
	}
}

func TestSchemaCarriesTheParameterVoices(t *testing.T) {
	h := newHarness(t, "/ws/sa")
	var schema struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(h.tool.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	action, ok := schema.Properties["action"].(map[string]any)
	if !ok {
		t.Fatal("schema missing action")
	}
	enum, _ := action["enum"].([]any)
	if want := []string{"create", "list", "pause", "resume", "remove", "runs"}; len(enum) != len(want) {
		t.Fatalf("action enum %v", enum)
	} else {
		for i, v := range want {
			if enum[i].(string) != v {
				t.Fatalf("action enum[%d] %v", i, enum)
			}
		}
	}
	id, _ := schema.Properties["id"].(map[string]any)
	if got, _ := id["description"].(string); got != "Job id jN (as shown by list). Required for pause/resume/remove/runs." {
		t.Fatalf("id description %q", got)
	}
}

func TestExecVoices(t *testing.T) {
	h := newHarness(t, "/ws/sa")
	if _, err := exec(t, h, map[string]any{}); err == nil || !strings.Contains(err.Error(), "scheduler: unknown action ''") {
		t.Fatalf("missing-action voice: %v", err)
	}
	if _, err := exec(t, h, map[string]any{"action": "create"}); err == nil || !strings.Contains(err.Error(), "scheduler: create requires 'name'") {
		t.Fatalf("create-name voice: %v", err)
	}
	if _, err := exec(t, h, map[string]any{"action": "create", "name": "x", "prompt": "p"}); err == nil || !strings.Contains(err.Error(), "scheduler: create requires 'cron'") {
		t.Fatalf("create-cron voice: %v", err)
	}
	if _, err := exec(t, h, map[string]any{"action": "pause"}); err == nil || !strings.Contains(err.Error(), "scheduler: pause requires 'id' (jN)") {
		t.Fatalf("pause-id voice: %v", err)
	}
	if _, err := exec(t, h, map[string]any{"action": "runs"}); err == nil || !strings.Contains(err.Error(), "scheduler: runs requires 'id' (jN)") {
		t.Fatalf("runs-id voice: %v", err)
	}
	if _, err := exec(t, h, map[string]any{"action": "list", "scope": "everywhere"}); err == nil || !strings.Contains(err.Error(), "scheduler: scope must be 'global' or 'cwd', got 'everywhere'") {
		t.Fatalf("scope voice: %v", err)
	}
}

func TestExecMappingLandsInTheStore(t *testing.T) {
	h := newHarness(t, "/ws/sa")
	reply, err := exec(t, h, map[string]any{
		"action": "create", "name": "surface", "prompt": "do the thing",
		"cron": "0 3 * * *", "scope": "cwd",
	})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	if !strings.HasPrefix(reply, "created j1 'surface' (cwd)") {
		t.Fatalf("create reply %q", reply)
	}
	if !strings.Contains(h.ct.text, "0 3 * * * /x/rig run-job") {
		t.Fatalf("crontab line missing: %q", h.ct.text)
	}
	list, err := exec(t, h, map[string]any{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "j1") || !strings.Contains(list, "surface") {
		t.Fatalf("list reply %q", list)
	}
	paused, err := exec(t, h, map[string]any{"action": "pause", "id": "j1", "scope": "cwd"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(paused, "paused") {
		t.Fatalf("pause reply %q", paused)
	}
}

// TestDefaultJobModelRidesTheSurface (SPEC_CONFIG 5, named): the
// default job model is the constructor's parameter — the description,
// the schema text, and the job the tool creates are all built from the
// passed value (the file's defaultJobModel at the root); an explicit
// model arg beats it, as 0.2.0.
func TestDefaultJobModelRidesTheSurface(t *testing.T) {
	h := newHarnessModel(t, "/ws/sa-model", "brain")
	d := h.tool.Description()
	if !strings.Contains(d, "Default model: brain") {
		t.Fatalf("description = %q, want the passed default named", d)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(h.tool.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	model, _ := schema.Properties["model"].(map[string]any)
	if got, _ := model["description"].(string); got != "pi model id (default brain)." {
		t.Fatalf("schema model description %q, want the passed default named", got)
	}
	// create with no model: the job row carries the passed default
	reply, err := exec(t, h, map[string]any{
		"action": "create", "name": "defaulted", "prompt": "p",
		"cron": "0 5 * * *", "scope": "cwd",
	})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	var m string
	if err := h.st.Cwd.DB.QueryRow(`SELECT model FROM jobs WHERE id = 'j1'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if m != "brain" {
		t.Fatalf("the job's model = %q, want the passed default brain", m)
	}
	// an explicit model still beats it
	reply, err = exec(t, h, map[string]any{
		"action": "create", "name": "explicit", "prompt": "p",
		"cron": "0 6 * * *", "scope": "cwd", "model": "qwen3.8-workers",
	})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	if err := h.st.Cwd.DB.QueryRow(`SELECT model FROM jobs WHERE id = 'j2'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if m != "qwen3.8-workers" {
		t.Fatalf("the explicit model must beat the default: %q", m)
	}
}

func TestExecAttributionFallsBackToAnon(t *testing.T) {
	h := newHarness(t, "/ws/sa")
	// unthreaded ctx: attribution lands as anon, store write still proceeds
	reply, err := exec(t, h, map[string]any{
		"action": "create", "name": "anon-case", "prompt": "p",
		"cron": "0 4 * * *", "scope": "cwd",
	})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	var sess any
	if err := h.st.Cwd.DB.QueryRow(`SELECT session FROM events WHERE op = 'create' ORDER BY seq DESC LIMIT 1`).Scan(&sess); err != nil {
		t.Fatal(err)
	}
	if sess != "anon" {
		t.Fatalf("create session %v, want anon", sess)
	}
}
