package scheduler_test

import (
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

func slotsJSON(processing []bool) string {
	type slot struct {
		ID           int  `json:"id"`
		IsProcessing bool `json:"is_processing"`
	}
	var slots []slot
	for i, p := range processing {
		slots = append(slots, slot{ID: i, IsProcessing: p})
	}
	b, _ := json.Marshal(slots)
	return string(b)
}

type gateFetch struct {
	mu        sync.Mutex
	resident  []string
	statuses  map[string]string
	models    []swapModel
	slots     [][]bool
	failing   string
	slotReads int
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
	case strings.Contains(url, "/upstream/"):
		if len(f.slots) == 0 {
			return nil, jsonError("no slots fixture for " + url)
		}
		served := f.slots[0]
		if len(f.slots) > 1 {
			f.slots = f.slots[1:]
		}
		f.slotReads++
		return json.RawMessage(slotsJSON(served)), nil
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

func TestDelegateOneSlotHeldRefusesWithThePinnedVoice(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	fetch := &gateFetch{resident: []string{"qwen3.8-27b-workers"}, slots: [][]bool{{true}}}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.Models = modelTable(t, "qwen3.8-27b-workers")
	})
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("a one-slot model with its only slot processing must refuse")
	} else if !strings.Contains(err.Error(), "delegate: no free slot; this turn holds the only one") {
		t.Errorf("pinned voice: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatalf("no spawn may happen without a free slot: %d", spawn.count())
	}
}

func TestDelegateNoFreeSlotNamesTheSlotCount(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	fetch := &gateFetch{resident: []string{"qwen3.8-27b-workers"}, slots: [][]bool{{true, true, true}}}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.Models = modelTable(t, "qwen3.8-27b-workers")
	})
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("an all-processing slot set must refuse")
	} else if !strings.Contains(err.Error(), "no free slot (all 3 slots are processing)") {
		t.Errorf("count voice: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatalf("no spawn may happen: %d", spawn.count())
	}
}

func TestDelegateFreeSlotSpawnsTheWorker(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	fetch := &gateFetch{resident: []string{"qwen3.8-27b-workers"}, slots: [][]bool{{true, false}}}
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
		slots:    [][]bool{{false}},
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
	fetch := &gateFetch{resident: []string{"qwen3.8-27b"}, slots: [][]bool{{false}}}
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
	fetch := &gateFetch{resident: []string{"resident-a"}, slots: [][]bool{{false}}}
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

func TestDelegateSlotReadFailureFailsClosed(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	fetch := &gateFetch{resident: []string{"qwen3.8-27b-workers"}, slots: [][]bool{{true}}}
	fetch.failing = "slots endpoint down"
	in := gateDelegateInput(t, fetch, spawn.spawn, nil)
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("a failed slot read must refuse")
	} else if !strings.Contains(err.Error(), "gate check failed") || !strings.Contains(err.Error(), "slots endpoint down") {
		t.Errorf("gate voice: %v", err)
	}
	if spawn.count() != 0 {
		t.Fatalf("no spawn may happen on a failed check: %d", spawn.count())
	}
}

func TestFireWaitsForAFreeSlotThenSpawns(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	fetch := &gateFetch{resident: []string{"qwen3.8-27b-workers"}, slots: [][]bool{{true}, {false}}}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.WaitBusy = true
		in.Models = modelTable(t, "qwen3.8-27b-workers")
	})
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if fetch.slotReads < 2 {
		t.Fatalf("the wait must poll the slots before spawning (reads = %d)", fetch.slotReads)
	}
	if spawn.count() != 1 {
		t.Fatalf("spawn calls = %d, want 1 after the slot freed", spawn.count())
	}
}

func TestFireSkipNamesTheHolderWhenTheWaitExpires(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	fetch := &gateFetch{resident: []string{"qwen3.8-27b-workers"}, slots: [][]bool{{true}}}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.WaitBusy = true
		in.Timeout = 1200 * time.Millisecond
		in.Models = modelTable(t, "qwen3.8-27b-workers")
	})
	started := time.Now()
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("an expired wait must refuse")
	} else if !strings.Contains(err.Error(), "no free slot on qwen3.8-27b-workers") {
		t.Errorf("skip voice: %v", err)
	}
	if time.Since(started) < time.Second {
		t.Fatalf("the wait must ride the timeout before refusing (waited %v)", time.Since(started))
	}
	if spawn.count() != 0 {
		t.Fatalf("no spawn may happen on an expired wait: %d", spawn.count())
	}
}

func TestFireSkipsImmediatelyWhenAnotherModelIsResident(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0}}
	fetch := &gateFetch{resident: []string{"other-model"}}
	in := gateDelegateInput(t, fetch, spawn.spawn, func(in *sched.DelegateInput) {
		in.Model = "brain"
		in.WaitBusy = true
	})
	started := time.Now()
	if _, err := sched.Delegate(in); err == nil {
		t.Fatal("a different resident model must refuse even with the wait policy")
	} else if !strings.Contains(err.Error(), "held by other-model") {
		t.Errorf("holder voice: %v", err)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("the holder refusal must not wait for an eviction (took %v)", time.Since(started))
	}
	if spawn.count() != 0 {
		t.Fatalf("no spawn may happen on a holder refusal: %d", spawn.count())
	}
}

func TestRunJobWaitsForAFreeSlotUpToTheFireTimeout(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	gf := &gateFetch{resident: []string{"qwen3.8-27b-workers"}, slots: [][]bool{{true}, {false}}}
	opts := runOpts(h, []string{"qwen3.8-27b-workers"}, spawn, fetchOpts{})
	opts.Fetch = gf.fetch
	opts.Timeout = 30 * time.Second
	if err := sched.RunJob(key, opts); err != nil {
		t.Fatalf("run-job: %v", err)
	}
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1 after the slot freed", len(spawn.calls))
	}
}

func TestRunJobSkipNamesTheHolderWhenTheWaitExpires(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	spawn := &fakeSpawn{}
	gf := &gateFetch{resident: []string{"qwen3.8-27b-workers"}, slots: [][]bool{{true}}}
	opts := runOpts(h, []string{"qwen3.8-27b-workers"}, spawn, fetchOpts{})
	opts.Fetch = gf.fetch
	opts.Timeout = 1200 * time.Millisecond
	if err := sched.RunJob(key, opts); err != nil {
		t.Fatalf("run-job: %v", err)
	}
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" {
		t.Fatalf("status %v", rec.Args["status"])
	}
	if !regexp.MustCompile(`no free slot on qwen3\.8-27b-workers`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("reason must name the model and the wait: %v", rec.Args["reason"])
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn on an expired wait")
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
