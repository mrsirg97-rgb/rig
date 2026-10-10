package scheduler_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

type gateFetch struct {
	mu       sync.Mutex
	resident []string
	statuses map[string]string
	models   []swapModel
	failing  string
}

func (f *gateFetch) fetch(url string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing != "" {
		return nil, jsonError(f.failing)
	}
	switch {
	case strings.HasSuffix(url, "/v1/models"):
		if f.models != nil {
			return json.RawMessage(swapModelsJSON(f.models)), nil
		}
		return json.RawMessage(modelsJSON(f.statuses)), nil
	case strings.HasSuffix(url, "/running"):
		return json.RawMessage(runningJSON(f.resident...)), nil
	}
	return nil, jsonError("unexpected url " + url)
}

func gateDelegateInput(t *testing.T, fetch *gateFetch, spawn sched.Spawn, mutate func(*sched.DelegateInput)) sched.DelegateInput {
	t.Helper()
	home := t.TempDir()
	db, _, _, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatalf("open scheduler store: %v", err)
	}
	in := sched.DelegateInput{
		DB:            db,
		Home:          home,
		Session:       "sess-super",
		Cwd:           t.TempDir(),
		Task:          "do the thing",
		DefaultModel:  "settings-model",
		WorkerSession: "worker-sess",
		Fetch:         fetch.fetch,
		Spawn:         spawn,
		WorkerCmd:     []string{"/x/rig"},
		SwapURL:       "http://127.0.0.1:8090",
		Timeout:       time.Minute,
		Sandbox:       "off",
	}
	if mutate != nil {
		mutate(&in)
	}
	return in
}

func spawnModel(t *testing.T, spawn *delegateSpawn) string {
	t.Helper()
	spawn.mu.Lock()
	defer spawn.mu.Unlock()
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	for i, a := range spawn.calls[0].Argv {
		if a == "-model" {
			return spawn.calls[0].Argv[i+1]
		}
	}
	t.Fatalf("no -model in the argv: %v", spawn.calls[0].Argv)
	return ""
}

func TestDelegateSendsAndWaits(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	fetch := &gateFetch{resident: []string{"qwen3.8-27b-workers"}}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.Models = modelTable(t, "qwen3.8-27b-workers")
	})
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("a resident model must send and wait on the server's queue: %v", err)
	}
	if spawn.count() != 1 {
		t.Fatalf("spawn calls = %d, want 1 (the server queues the request)", spawn.count())
	}
}

func TestDelegateUnnamedResidentSpawnsTheRow(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	fetch := &gateFetch{resident: []string{"qwen3.8-27b-workers"}}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.Models = modelTable(t, "qwen3.8-27b-workers")
	})
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if got := spawnModel(t, spawn); got != "qwen3.8-27b-workers" {
		t.Fatalf("the worker must run the named model, got %q", got)
	}
}

func TestDelegateUnnamedResidentAliasRunsTheTableRow(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	fetch := &gateFetch{
		resident: []string{"glm5.3-flash"},
		models:   []swapModel{{ID: "glm5.3-flash", Alias: []string{"ox-alpha"}}},
	}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.Model = ""
		in.Models = modelTable(t, "ox-alpha")
	})
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if got := spawnModel(t, spawn); got != "ox-alpha" {
		t.Fatalf("the resident's alias must run the models-table row, got %q", got)
	}
	row := in.DB.DB.QueryRow(`SELECT args FROM events WHERE op = 'create'`)
	var args string
	if err := row.Scan(&args); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(args), &record); err != nil {
		t.Fatal(err)
	}
	if record["model"] != "ox-alpha" {
		t.Fatalf("the ad-hoc record must name the row id: %v", record)
	}
}

func TestDelegateUnnamedResidentRowRunsUnchanged(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	fetch := &gateFetch{resident: []string{"qwen3.8-27b"}}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.Model = ""
		in.Models = modelTable(t, "qwen3.8-27b")
	})
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if got := spawnModel(t, spawn); got != "qwen3.8-27b" {
		t.Fatalf("a resident id that is itself a row must run unchanged, got %q", got)
	}
}

func TestDelegateUnnamedResidentWithoutARowRefusesNamingItAndTheKnownRows(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	fetch := &gateFetch{resident: []string{"glm5.3-flash"}}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.Model = ""
		in.Models = modelTable(t, "dsv4", "ox-alpha")
	})
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("a resident with no row must refuse")
	} else if !strings.Contains(err.Error(), "glm5.3-flash") || !strings.Contains(err.Error(), "known:") ||
		!strings.Contains(err.Error(), "dsv4") || !strings.Contains(err.Error(), "ox-alpha") {
		t.Errorf("the refusal must name the resident and the known rows: %v", err)
	} else if spawn.count() != 0 {
		t.Fatalf("no spawn may happen without a row: %d", spawn.count())
	}
}

func TestDelegateUnnamedModelRunsTheResidentModel(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	fetch := &gateFetch{resident: []string{"resident-a"}}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.Model = ""
		in.Models = modelTable(t, "resident-a")
	})
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if got := spawnModel(t, spawn); got != "resident-a" {
		t.Fatalf("an unnamed worker must run the resident model, got %q", got)
	}
}

func TestDelegateNothingResidentLoadsTheSessionDefault(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	fetch := &gateFetch{}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) { in.Model = "" })
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if got := spawnModel(t, spawn); got != "settings-model" {
		t.Fatalf("with nothing resident the worker must load the session default, got %q", got)
	}
}

func TestDelegateNamedModelWhileAnotherResidentRefusesNamingTheHolder(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	fetch := &gateFetch{resident: []string{"other-model"}}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) { in.Model = "brain" })
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("a named model while another is resident must refuse")
	} else if !strings.Contains(err.Error(), "held by other-model") {
		t.Errorf("holder voice: %v", err)
	} else if !strings.Contains(err.Error(), "once-job") {
		t.Errorf("the refusal must teach the escape hatch: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatalf("no spawn may happen on a holder refusal: %d", spawn.count())
	}
}

func TestDelegateGateReadFailureFailsClosed(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	fetch := &gateFetch{resident: []string{"qwen3.8-27b-workers"}}
	fetch.failing = "models endpoint down"
	in := gateDelegateInput(t, fetch, spawn.spawn, nil)
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("a failed gate read must refuse")
	} else if !strings.Contains(err.Error(), "gate check failed") || !strings.Contains(err.Error(), "models endpoint down") {
		t.Errorf("gate voice: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatalf("no spawn may happen on a failed check: %d", spawn.count())
	}
}

func TestDelegateNegativeTimeoutRunsOnTheCallerContext(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	fetch := &gateFetch{}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.Timeout = -1
	})
	deadlineSeen := false
	spawn.onSpawn = func(ctx context.Context, observe func([]byte)) {
		if _, ok := ctx.Deadline(); ok {
			deadlineSeen = true
		}
	}
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if deadlineSeen {
		t.Fatal("a negative timeout must run on the caller's context alone (no deadline)")
	}
}

func TestDelegateZeroTimeoutKeepsTheDefault(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	fetch := &gateFetch{}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) { in.Timeout = 0 })
	var limit time.Duration
	spawn.onSpawn = func(ctx context.Context, observe func([]byte)) {
		deadline, ok := ctx.Deadline()
		if ok {
			limit = time.Until(deadline)
		}
	}
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if limit <= 0 || limit > sched.DefaultRunTimeout {
		t.Fatalf("the zero timeout must wrap the default run timeout, got %v", limit)
	}
}

func TestRunJobSendsAndWaitsUpToTheFireTimeout(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	gf := &gateFetch{resident: []string{"qwen3.8-27b-workers"}}
	opts := runOpts(h, []string{"qwen3.8-27b-workers"}, spawn, fetchOpts{})
	opts.Fetch = gf.fetch
	opts.Timeout = 30 * time.Second
	if err := sched.RunJob(key, opts); err != nil {
		t.Fatalf("run-job: %v", err)
	}
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1 (the fire's request queues at the server)", len(spawn.calls))
	}
}

func TestRunJobSendsAndWaits(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	gf := &gateFetch{resident: []string{"qwen3.8-27b-workers"}}
	opts := runOpts(h, []string{"qwen3.8-27b-workers"}, spawn, fetchOpts{})
	opts.Fetch = gf.fetch
	if err := sched.RunJob(key, opts); err != nil {
		t.Fatalf("run-job: %v", err)
	}
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] == "skip" {
		t.Fatalf("the fire must not skip: %v", rec.Args)
	}
}

func TestRunJobHolderSkipNamesTheResidentImmediately(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	spawn := &fakeSpawn{}
	gf := &gateFetch{resident: []string{"qwen3.8-27b"}}
	opts := runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{})
	opts.Fetch = gf.fetch
	if err := sched.RunJob(key, opts); err != nil {
		t.Fatalf("run-job: %v", err)
	}
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" {
		t.Fatalf("status %v", rec.Args["status"])
	}
	if !regexp.MustCompile(`held by qwen3\.8-27b`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("reason must name the holder: %v", rec.Args["reason"])
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn on a holder skip")
	}
}

func TestCreateRefusesBusyForce(t *testing.T) {
	h := newHarness(t, realCwd(t, "session"))
	_, err := h.create(sched.CreateInput{
		Name: "job", Prompt: "p", Cron: "0 3 * * *", Model: "m", Busy: "force",
	})
	if err == nil || !strings.Contains(err.Error(), "force is retired") {
		t.Fatalf("create must refuse force naming the retirement: %v", err)
	}
}
