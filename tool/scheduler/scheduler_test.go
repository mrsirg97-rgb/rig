package scheduler_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	adapter "github.com/mrsirg97-rgb/rig/v2/tool/scheduler"
)

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
	db   sched.DB
	ct   *fakeCrontab
	home string
	tool core.Tool
}

func newHarness(t *testing.T, cwd string) *harness {
	return newHarnessModel(t, cwd, "qwen3.8-workers")
}

func newHarnessModel(t *testing.T, cwd, defModel string) *harness {
	t.Helper()
	home := t.TempDir()
	globalPath := filepath.Join(home, "global.sqlite")
	db, _, _, err := store.Open(globalPath, sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	ct := &fakeCrontab{text: "SHELL=/bin/bash\n"}
	tool := adapter.New(db, ct, "/x/rig run-job", defModel, home)
	return &harness{db: db, ct: ct, home: home, tool: tool}
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
		"come from list: copy them, never invent them",
		"busy:skip is the only policy: a fire waits for a free slot up to its timeout, or skips naming the holder",
		"eviction is the operator's act (the fleet is the resident model)",
		"(default: qwen3.8-workers)",
		"until the note clears",
		"re-create it to retry",
		"self-deletes after one fire",
		"running in its own workspace",
		"repair re-derives its crontab line",
	} {
		if !strings.Contains(d, want) {
			t.Fatalf("description missing voice fragment: %q", want)
		}
	}
}

func TestSchemaCarriesTheParameterVoicesAndNoScope(t *testing.T) {
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
	if want := []string{"create", "update", "list", "pause", "resume", "remove", "runs", "repair"}; len(enum) != len(want) {
		t.Fatalf("action enum %v", enum)
	} else {
		for i, v := range want {
			if enum[i].(string) != v {
				t.Fatalf("action enum[%d] %v", i, enum)
			}
		}
	}
	if _, ok := schema.Properties["scope"]; ok {
		t.Fatal("the scope arg must be gone from the schema")
	}
	id, _ := schema.Properties["id"].(map[string]any)
	if got, _ := id["description"].(string); got != "Job id jN (as shown by list). Required for pause/resume/remove/runs; repair takes it or none (none repairs every drifting job)." {
		t.Fatalf("id description %q", got)
	}
	cwd, _ := schema.Properties["workspace"].(map[string]any)
	if got, _ := cwd["description"].(string); got != "the workspace the job runs in (default: this session's workspace)." {
		t.Fatalf("cwd description %q", got)
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
	if _, err := exec(t, h, map[string]any{"action": "update"}); err == nil || !strings.Contains(err.Error(), "scheduler: update requires 'id' (jN)") {
		t.Fatalf("update-id voice: %v", err)
	}
}

func TestExecUpdateLandsInTheStore(t *testing.T) {
	h := newHarness(t, "/ws/sa")
	if _, err := exec(t, h, map[string]any{
		"action": "create", "name": "upd", "prompt": "old prompt",
		"cron": "0 3 * * *",
	}); err != nil {
		t.Fatal(err)
	}
	before := h.ct.text
	reply, err := exec(t, h, map[string]any{
		"action": "update", "id": "j1", "prompt": "new prompt", "model": "brain",
	})
	if err != nil {
		t.Fatalf("update: %v (%s)", err, reply)
	}
	if !strings.HasPrefix(reply, "updated j1 'upd'") {
		t.Fatalf("update reply %q", reply)
	}
	if h.ct.text != before {
		t.Fatalf("a non-cadence update must not touch the crontab: %q", h.ct.text)
	}
	var prompt, model string
	if err := h.db.DB.QueryRow(`SELECT prompt, model FROM jobs WHERE id = 'j1'`).Scan(&prompt, &model); err != nil {
		t.Fatal(err)
	}
	if prompt != "new prompt" || model != "brain" {
		t.Fatalf("fields not overlaid: %q %q", prompt, model)
	}

	reply, err = exec(t, h, map[string]any{
		"action": "update", "id": "j1", "cron": "0 4 * * *",
	})
	if err != nil {
		t.Fatalf("cadence update: %v (%s)", err, reply)
	}
	if !strings.Contains(h.ct.text, "0 4 * * * /x/rig run-job j1  # rig-scheduler:"+sched.TagHome(h.home)+":j1") {
		t.Fatalf("the line must be rewritten: %q", h.ct.text)
	}
	if strings.Contains(h.ct.text, "0 3 * * * /x/rig run-job j1") {
		t.Fatalf("the old line must be gone: %q", h.ct.text)
	}
	if _, err := exec(t, h, map[string]any{"action": "update", "id": "j9"}); err == nil || !strings.Contains(err.Error(), "no job 'j9'") {
		t.Fatalf("unknown-id voice: %v", err)
	}
}

func TestExecMappingLandsInTheStore(t *testing.T) {
	h := newHarness(t, "/ws/sa")
	reply, err := exec(t, h, map[string]any{
		"action": "create", "name": "surface", "prompt": "do the thing",
		"cron": "0 3 * * *",
	})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	if !strings.HasPrefix(reply, "created j1 'surface'") {
		t.Fatalf("create reply %q", reply)
	}
	if !strings.Contains(h.ct.text, "0 3 * * * /x/rig run-job j1  # rig-scheduler:"+sched.TagHome(h.home)+":j1") {
		t.Fatalf("crontab line missing: %q", h.ct.text)
	}
	list, err := exec(t, h, map[string]any{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "j1") || !strings.Contains(list, "surface") {
		t.Fatalf("list reply %q", list)
	}
	paused, err := exec(t, h, map[string]any{"action": "pause", "id": "j1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(paused, "paused") {
		t.Fatalf("pause reply %q", paused)
	}
}

func TestExecRepairLandsInTheStore(t *testing.T) {
	h := newHarness(t, "/ws/sa")
	if _, err := exec(t, h, map[string]any{"action": "create", "name": "drifty", "prompt": "p", "cron": "0 3 * * *"}); err != nil {
		t.Fatal(err)
	}
	h.ct.mu.Lock()
	h.ct.text = strings.Replace(h.ct.text, "0 3 * * * /x/rig run-job j1", "59 23 * * * /x/rig run-job j1", 1)
	h.ct.mu.Unlock()

	reply, err := exec(t, h, map[string]any{"action": "repair", "id": "j1"})
	if err != nil {
		t.Fatalf("repair: %v (%s)", err, reply)
	}
	if !strings.Contains(reply, "'j1' repaired: cron differs (crontab: 59 23 * * *)") {
		t.Fatalf("repair reply %q", reply)
	}
	if !strings.Contains(h.ct.text, "0 3 * * * /x/rig run-job j1  # rig-scheduler:"+sched.TagHome(h.home)+":j1") {
		t.Fatalf("line not re-derived: %q", h.ct.text)
	}
	reply, err = exec(t, h, map[string]any{"action": "repair", "id": "j1"})
	if err != nil {
		t.Fatalf("second repair: %v (%s)", err, reply)
	}
	if reply != "'j1' is in sync" {
		t.Fatalf("in-sync reply %q", reply)
	}
}

func TestExecRepairAllWalksEveryDriftingJob(t *testing.T) {
	h := newHarness(t, "/ws/sa")
	for _, name := range []string{"one", "two"} {
		if _, err := exec(t, h, map[string]any{"action": "create", "name": name, "prompt": "p", "cron": "0 3 * * *"}); err != nil {
			t.Fatal(err)
		}
	}
	h.ct.mu.Lock()
	h.ct.text = strings.Replace(h.ct.text, "0 3 * * * /x/rig run-job j2", "59 23 * * * /x/rig run-job j2", 1)
	h.ct.mu.Unlock()

	reply, err := exec(t, h, map[string]any{"action": "repair"})
	if err != nil {
		t.Fatalf("repair all: %v (%s)", err, reply)
	}
	if !strings.Contains(reply, "'j2' repaired: cron differs (crontab: 59 23 * * *)") {
		t.Fatalf("walk reply %q, want one line per repaired job", reply)
	}
	if strings.Contains(reply, "j1") {
		t.Fatalf("an in-sync job must not be listed: %q", reply)
	}
	reply, err = exec(t, h, map[string]any{"action": "repair"})
	if err != nil {
		t.Fatalf("second walk: %v (%s)", err, reply)
	}
	if reply != "nothing drifted" {
		t.Fatalf("second walk %q", reply)
	}
}

func TestDefaultJobModelRidesTheSurface(t *testing.T) {
	h := newHarnessModel(t, "/ws/sa-model", "brain")
	d := h.tool.Description()
	if !strings.Contains(d, "(default: brain)") {
		t.Fatalf("description = %q, want the passed default named", d)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(h.tool.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	model, _ := schema.Properties["model"].(map[string]any)
	if got, _ := model["description"].(string); got != "worker model id (default brain)." {
		t.Fatalf("schema model description %q, want the passed default named", got)
	}

	reply, err := exec(t, h, map[string]any{
		"action": "create", "name": "defaulted", "prompt": "p",
		"cron": "0 5 * * *",
	})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	var m string
	if err := h.db.DB.QueryRow(`SELECT model FROM jobs WHERE id = 'j1'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if m != "brain" {
		t.Fatalf("the job's model = %q, want the passed default brain", m)
	}

	reply, err = exec(t, h, map[string]any{
		"action": "create", "name": "explicit", "prompt": "p",
		"cron": "0 6 * * *", "model": "qwen3.8-workers",
	})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	if err := h.db.DB.QueryRow(`SELECT model FROM jobs WHERE id = 'j2'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if m != "qwen3.8-workers" {
		t.Fatalf("the explicit model must beat the default: %q", m)
	}
}

func TestExecAttributionFallsBackToAnon(t *testing.T) {
	h := newHarness(t, "/ws/sa")

	reply, err := exec(t, h, map[string]any{
		"action": "create", "name": "anon-case", "prompt": "p",
		"cron": "0 4 * * *",
	})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	var sess any
	if err := h.db.DB.QueryRow(`SELECT session FROM events WHERE op = 'create' ORDER BY seq DESC LIMIT 1`).Scan(&sess); err != nil {
		t.Fatal(err)
	}
	if sess != "anon" {
		t.Fatalf("create session %v, want anon", sess)
	}
}

func TestCreateRefusesACwdOutsideTheSessionRoot(t *testing.T) {
	home := t.TempDir()
	globalPath := filepath.Join(home, "global.sqlite")
	db, _, _, err := store.Open(globalPath, sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	ct := &fakeCrontab{text: "SHELL=/bin/bash\n"}
	tool := adapter.New(db, ct, "/x/rig run-job", "qwen3.8-workers", home)
	raw, _ := json.Marshal(map[string]any{
		"action": "create", "name": "escape", "prompt": "p", "cron": "1 0 * * *", "workspace": "/etc",
	})
	_, err = tool.Exec(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "outside the session's workspace") {
		t.Fatalf("a cwd outside the session's workspace and the rig home must be refused, got %v", err)
	}
}
