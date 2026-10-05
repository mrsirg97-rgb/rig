package scheduler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
	tool adapter.Scheduler
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
		"come from `list` — copy them, never invent them",
		"if a different model is resident at fire time the send skips and names it",
		"eviction policy falls on the operator",
		"omit it and the job runs on what is resident",
		"until their notes clear",
		"re-create it to retry",
		"self-deletes after firing",
		"each job runs in its own workspace",
		"`repair` re-derives a crontab",
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
	if got, _ := id["description"].(string); got != "job id jN from list; required for pause/resume/remove/runs; repair takes it or none." {
		t.Fatalf("id description %q", got)
	}
	if _, ok := schema.Properties["workspace"]; ok {
		t.Fatal("the workspace arg must be gone from the schema (the job runs in the session's workspace)")
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

func TestModelSurfaceCarriesTheResidentRule(t *testing.T) {
	h := newHarnessModel(t, "/ws/sa-model", "brain")
	d := h.tool.Description()
	if !strings.Contains(d, "omit it and the job runs on what is resident") {
		t.Fatalf("description = %q, want the resident rule in words; the schema names the default", d)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(h.tool.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	model, _ := schema.Properties["model"].(map[string]any)
	want := "the worker model id; omit it and the job runs on whatever is resident (default brain when nothing is)"
	if got, _ := model["description"].(string); got != want {
		t.Fatalf("schema model description %q, want %q", got, want)
	}

	reply, err := exec(t, h, map[string]any{
		"action": "create", "name": "unnamed", "prompt": "p",
		"cron": "0 5 * * *",
	})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	var m string
	if err := h.db.DB.QueryRow(`SELECT model FROM jobs WHERE id = 'j1'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if m != "" {
		t.Fatalf("the unnamed job's model = %q, want empty (resolved at fire time)", m)
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
		t.Fatalf("the named model must be stored verbatim: %q", m)
	}
}

func TestUpdateModelNullClearsToTheUnnamedJob(t *testing.T) {
	h := newHarnessModel(t, "/ws/sa-null", "brain")
	reply, err := exec(t, h, map[string]any{
		"action": "create", "name": "named", "prompt": "p",
		"cron": "0 5 * * *", "model": "brain",
	})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	reply, err = exec(t, h, map[string]any{
		"action": "update", "id": "j1", "model": nil,
	})
	if err != nil {
		t.Fatalf("update: %v (%s)", err, reply)
	}
	var m string
	if err := h.db.DB.QueryRow(`SELECT model FROM jobs WHERE id = 'j1'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if m != "" {
		t.Fatalf("model = %q, want cleared to the unnamed job", m)
	}
	reply, err = exec(t, h, map[string]any{
		"action": "update", "id": "j1", "model": "brain",
	})
	if err != nil {
		t.Fatalf("re-name: %v (%s)", err, reply)
	}
	if err := h.db.DB.QueryRow(`SELECT model FROM jobs WHERE id = 'j1'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if m != "brain" {
		t.Fatalf("model = %q, want brain", m)
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

func TestExecWorkspaceLandsInTheStore(t *testing.T) {
	h := newHarness(t, "/ws/sa")
	inside := filepath.Join(h.home, "sub")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec(t, h, map[string]any{
		"action": "create", "name": "scoped", "prompt": "work",
		"cron": "0 3 * * *", "workspace": inside,
	}); err != nil {
		t.Fatal(err)
	}
	var cwd string
	if err := h.db.DB.QueryRow(`SELECT cwd FROM jobs WHERE id = 'j1'`).Scan(&cwd); err != nil {
		t.Fatal(err)
	}
	if cwd != inside {
		t.Fatalf("job cwd = %q, want the validated workspace %q", cwd, inside)
	}
	if _, err := exec(t, h, map[string]any{
		"action": "update", "id": "j1", "workspace": "/etc",
	}); err == nil || !strings.Contains(err.Error(), "outside the session's workspace") {
		t.Fatalf("a workspace outside the session root must be refused, got %v", err)
	}
}

func TestExecRunsHonorsTheCount(t *testing.T) {
	h := newHarness(t, "/ws/sa")
	if _, err := exec(t, h, map[string]any{
		"action": "create", "name": "audited", "prompt": "work", "cron": "0 3 * * *",
	}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := h.db.DB.Exec(`INSERT INTO runs (seq, job_id, started_at, ended_at, status, model) VALUES (?, 'j1', ?, ?, 'ok', 'm')`,
			i, fmt.Sprintf("2026-01-0%dT00:00:00Z", i), fmt.Sprintf("2026-01-0%dT00:01:00Z", i)); err != nil {
			t.Fatal(err)
		}
	}
	one, err := exec(t, h, map[string]any{"action": "runs", "id": "j1", "n": 1})
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if !strings.Contains(one, "j1 · 1 run") || !strings.Contains(one, "2026-01-03") {
		t.Fatalf("runs n=1 must show the newest run only:\n%s", one)
	}
	all, err := exec(t, h, map[string]any{"action": "runs", "id": "j1"})
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if !strings.Contains(all, "j1 · 3 runs") || !strings.Contains(all, "2026-01-01") {
		t.Fatalf("runs without n must show the default window, oldest first:\n%s", all)
	}
}

func TestSchedulerVerbsAndExecShareTheirChecks(t *testing.T) {
	h := newHarness(t, "/ws/sched")
	ctx := context.Background()

	_, createErr := h.tool.Create(ctx, adapter.CreateInput{Prompt: "the prompt", Cron: "0 3 * * *"})
	_, execErr := exec(t, h, map[string]any{"action": "create", "name": "  ", "prompt": "the prompt", "cron": "0 3 * * *"})
	if createErr == nil || execErr == nil {
		t.Fatalf("a job without a name refuses on both doors, create=%v exec=%v", createErr, execErr)
	}
	if createErr.Error() != "scheduler: create requires 'name'" || execErr.Error() != createErr.Error() {
		t.Fatalf("the refusal is the same words on either door: create %q exec %q", createErr, execErr)
	}

	_, createErr = h.tool.Create(ctx, adapter.CreateInput{Name: "sweep", Command: "ls", Cron: "0 3 * * *", Model: "some-model"})
	_, execErr = exec(t, h, map[string]any{"action": "create", "name": "sweep", "command": "ls", "cron": "0 3 * * *", "model": "some-model"})
	if createErr == nil || execErr == nil || createErr.Error() != execErr.Error() {
		t.Fatalf("a command job with a model refuses the same way on either door: create=%v exec=%v", createErr, execErr)
	}

	outside := filepath.Join(t.TempDir(), "elsewhere")
	_, createErr = h.tool.Create(ctx, adapter.CreateInput{Name: "sweep", Prompt: "the prompt", Cron: "0 3 * * *", Workspace: outside})
	_, execErr = exec(t, h, map[string]any{"action": "create", "name": "sweep", "prompt": "the prompt", "cron": "0 3 * * *", "workspace": outside})
	if createErr == nil || execErr == nil || createErr.Error() != execErr.Error() {
		t.Fatalf("a workspace outside the guard refuses the same way on either door: create=%v exec=%v", createErr, execErr)
	}

	_, updateErr := h.tool.Update(ctx, "", adapter.UpdateInput{Name: "n"})
	_, execErr = exec(t, h, map[string]any{"action": "update", "name": "n"})
	if updateErr == nil || execErr == nil || updateErr.Error() != "scheduler: update requires 'id' (jN)" || execErr.Error() != updateErr.Error() {
		t.Fatalf("an update without a job id refuses the same words on either door: update=%v exec=%v", updateErr, execErr)
	}

	_, pauseErr := h.tool.Pause(ctx, "")
	_, execErr = exec(t, h, map[string]any{"action": "pause"})
	if pauseErr == nil || execErr == nil || pauseErr.Error() != "scheduler: pause requires 'id' (jN)" || execErr.Error() != pauseErr.Error() {
		t.Fatalf("a pause without a job id refuses the same words on either door: pause=%v exec=%v", pauseErr, execErr)
	}

	_, runsErr := h.tool.Runs(ctx, "", 5)
	_, execErr = exec(t, h, map[string]any{"action": "runs", "n": 5})
	if runsErr == nil || execErr == nil || runsErr.Error() != "scheduler: runs requires 'id' (jN)" || execErr.Error() != runsErr.Error() {
		t.Fatalf("a runs without a job id refuses the same words on either door: runs=%v exec=%v", runsErr, execErr)
	}

	if _, err := h.tool.Create(ctx, adapter.CreateInput{Name: "nightly", Prompt: "the prompt", Cron: "0 3 * * *"}); err != nil {
		t.Fatalf("create through the verb: %v", err)
	}
	fromVerb, err := h.tool.List(ctx)
	if err != nil {
		t.Fatalf("list through the verb: %v", err)
	}
	fromDoor, err := exec(t, h, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("list through the door: %v", err)
	}
	if !strings.Contains(fromVerb, "nightly") {
		t.Fatalf("the job created through the verb is not on the board:\n%s", fromVerb)
	}
	if fromVerb != fromDoor {
		t.Fatalf("one listing replies the same bytes through either door:\nverb %q\ndoor %q", fromVerb, fromDoor)
	}
}
