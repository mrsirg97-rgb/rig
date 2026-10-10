package scheduler_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

func gateDelegateInput(t *testing.T, fetch sched.Fetch, spawn sched.Spawn, mutate func(*sched.DelegateInput)) sched.DelegateInput {
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
		Fetch:         fetch,
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

func spawnModel(t *testing.T, spawn *fakeSpawn) string {
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
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	in := gateDelegateInput(t, fakeFetch([]string{"qwen3.8-27b-workers"}, fetchOpts{}), spawn.spawn, func(in *sched.DelegateInput) {
		in.Models = modelTable(t, "qwen3.8-27b-workers")
	})
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("a resident model must send and wait on the server's queue: %v", err)
	}
	if spawn.count() != 1 {
		t.Fatalf("spawn calls = %d, want 1 (the server queues the request)", spawn.count())
	}
}

func TestDelegateUnnamedResidentWithoutARowRefusesNamingItAndTheKnownRows(t *testing.T) {
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	in := gateDelegateInput(t, fakeFetch([]string{"glm5.3-flash"}, fetchOpts{}), spawn.spawn, func(in *sched.DelegateInput) {
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

func TestDelegateNothingResidentLoadsTheSessionDefault(t *testing.T) {
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	in := gateDelegateInput(t, fakeFetch(nil, fetchOpts{}), spawn.spawn, func(in *sched.DelegateInput) { in.Model = "" })
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if got := spawnModel(t, spawn); got != "settings-model" {
		t.Fatalf("with nothing resident the worker must load the session default, got %q", got)
	}
}

func TestDelegateNamedModelWhileAnotherResidentRefusesNamingTheHolder(t *testing.T) {
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	in := gateDelegateInput(t, fakeFetch([]string{"other-model"}, fetchOpts{}), spawn.spawn, func(in *sched.DelegateInput) { in.Model = "brain" })
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
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	fetch := fakeFetch([]string{"qwen3.8-27b-workers"}, fetchOpts{failing: "models endpoint down"})
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
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	in := gateDelegateInput(t, fakeFetch(nil, fetchOpts{}), spawn.spawn, func(in *sched.DelegateInput) {
		in.Timeout = -1
	})
	deadlineSeen := false
	spawn.onSpawn = func(ctx context.Context, argv []string) {
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
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	in := gateDelegateInput(t, fakeFetch(nil, fetchOpts{}), spawn.spawn, func(in *sched.DelegateInput) { in.Timeout = 0 })
	var limit time.Duration
	spawn.onSpawn = func(ctx context.Context, argv []string) {
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

func TestCreateRefusesBusyForce(t *testing.T) {
	h := newHarness(t, realCwd(t, "session"))
	_, err := h.create(sched.CreateInput{
		Name: "job", Prompt: "p", Cron: "0 3 * * *", Model: "m", Busy: "force",
	})
	if err == nil || !strings.Contains(err.Error(), "force is retired") {
		t.Fatalf("create must refuse force naming the retirement: %v", err)
	}
}
