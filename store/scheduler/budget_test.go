package scheduler_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/store/state"
)

type budgetSpawn struct {
	mu      sync.Mutex
	calls   []string
	onSpawn func(argv []string)
}

func (f *budgetSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(argv, " "))
	f.mu.Unlock()
	if f.onSpawn != nil {
		f.onSpawn(argv)
	}
	return sched.SpawnResult{Exit: 0, Stdout: "done\n"}, nil
}

func (f *budgetSpawn) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func remoteModels(t *testing.T) models.Table {
	t.Helper()
	tbl, err := models.New(models.Model{
		ID: "qwen3.8-workers", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384,
		Role: models.RoleWorker, Remote: true, Provider: "openrouter",
		BaseURL: "https://openrouter.ai/api/v1", APIKey: "sk-test",
	})
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	return tbl
}

func TestScheduledJobBudgetStopsFiringAtTheCap(t *testing.T) {
	cwd := realCwd(t, "job")
	rigHome := t.TempDir()
	h, key := setupJob(t, cwd, func(in *sched.CreateInput) {
		in.Budget = 5
	})
	spawn := &budgetSpawn{}
	spawn.onSpawn = func(argv []string) {
		session := ""
		for i, a := range argv {
			if a == "-session-id" && i+1 < len(argv) {
				session = argv[i+1]
			}
		}
		if session == "" {
			t.Fatalf("the fire's argv must carry the worker session id: %v", argv)
		}
		path := state.StorePath(rigHome, cwd)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("state dir: %v", err)
		}
		db, _, _, err := store.Open(path, state.Statements(), state.SchemaVersion, state.Migration())
		if err != nil {
			t.Fatalf("state store: %v", err)
		}
		defer db.DB.Close()
		ctx := context.Background()
		if err := state.RecordSession(ctx, db, session, cwd, "m", "v"); err != nil {
			t.Fatal(err)
		}
		if _, err := state.RecordMessage(ctx, db, session, "user", "task", nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		seq, err := state.RecordMessage(ctx, db, session, "assistant", "done", nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.RecordUsage(ctx, db, seq, 10, 5, 0, 0, 6); err != nil {
			t.Fatal(err)
		}
	}
	opts := sched.RunOpts{
		Home:      h.home,
		Crontab:   h.ct,
		Fetch:     fakeFetch([]string{"qwen3.8-27b"}, fetchOpts{statuses: map[string]string{"qwen3.8-27b-workers": "loaded"}}),
		Spawn:     spawn.spawn,
		WorkerCmd: []string{"/x/rig"},
		SwapURL:   "http://127.0.0.1:8090",
		Now:       func() time.Time { return runnerNow },
		Sandbox:   "off",
		RigHome:   rigHome,
	}

	if err := sched.RunJob(key, opts); err != nil {
		t.Fatalf("first fire: %v", err)
	}
	if spawn.count() != 1 {
		t.Fatalf("first fire must spawn, got %d calls", spawn.count())
	}
	if err := sched.RunJob(key, opts); err != nil {
		t.Fatalf("second fire: %v", err)
	}
	if spawn.count() != 1 {
		t.Fatalf("the fire at the cap must not spawn, got %d calls", spawn.count())
	}
	rec := runEvents(t, h, "")[1]
	reason, _ := rec.Args["reason"].(string)
	if rec.Args["status"] != "skip" || !strings.Contains(reason, "budget") {
		t.Fatalf("the at-cap fire must record a skip naming the budget, got status %v reason %q", rec.Args["status"], reason)
	}
}

func TestRemoteJobFireSkipsTheBusyProbe(t *testing.T) {
	cwd := realCwd(t, "job")
	rigHome := t.TempDir()
	h, key := setupJob(t, cwd, func(in *sched.CreateInput) {
		in.Model = "qwen3.8-workers"
	})
	probeCalls := 0
	fetch := func(url string) (json.RawMessage, error) {
		probeCalls++
		return nil, jsonError("the swap must never be consulted: " + url)
	}
	spawn := &budgetSpawn{}
	opts := sched.RunOpts{
		Home:      h.home,
		Crontab:   h.ct,
		Fetch:     fetch,
		Spawn:     spawn.spawn,
		WorkerCmd: []string{"/x/rig"},
		SwapURL:   "http://127.0.0.1:8090",
		Now:       func() time.Time { return runnerNow },
		Sandbox:   "off",
		RigHome:   rigHome,
	}
	opts.Models = func() models.Table { return remoteModels(t) }

	if err := sched.RunJob(key, opts); err != nil {
		t.Fatalf("remote fire: %v", err)
	}
	if probeCalls != 0 {
		t.Fatalf("the swap was probed %d times; a remote job fire must never consult it", probeCalls)
	}
	if spawn.count() != 1 {
		t.Fatalf("the remote fire must spawn, got %d calls", spawn.count())
	}
}
