package swarm_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/models"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
	"github.com/mrsirg97-rgb/rig/swarm"
)

func workerSessionFromArgv(argv string) string {
	fields := strings.Fields(argv)
	for i, f := range fields {
		if f == "-session-id" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

func recordWorkerCost(t *testing.T, rigHome, cwd, session string, cost float64) {
	t.Helper()
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
	if err := state.RecordUsage(ctx, db, seq, 10, 5, 0, 0, cost); err != nil {
		t.Fatal(err)
	}
}

func TestSwarmBudgetStopsClaimsWithANotice(t *testing.T) {
	h := newHarness(t)
	h.create(t, "first", "second")
	h.spawn.onCall = func(observe func([]byte)) {
		session := workerSessionFromArgv(h.spawn.argv(h.spawn.count() - 1))
		if session == "" {
			t.Fatalf("the spawn argv must carry the worker session id: %s", h.spawn.argv(h.spawn.count()-1))
		}
		recordWorkerCost(t, h.rigHome, h.cwd, session, 6)
	}
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker", Budget: 5})
	h.waitFor(t, "the budget notice", func() bool {
		for _, n := range h.fe.notices() {
			if strings.Contains(n.Text, "budget reached") {
				return true
			}
		}
		return false
	})
	h.waitFor(t, "the swarm to stop claiming", func() bool {
		rows, err := h.todoDB.DB.Query(`SELECT count(*) FROM tasks WHERE scope = 'swarm' AND status = 'pending'`)
		if err != nil {
			return false
		}
		defer rows.Close()
		rows.Next()
		var n int
		rows.Scan(&n)
		return n == 1
	})
	if got := h.spawn.count(); got != 1 {
		t.Fatalf("spawn calls = %d, want 1 (the second task must never be claimed at the cap)", got)
	}
	found := false
	for _, n := range h.fe.notices() {
		if strings.Contains(n.Text, "budget reached") && strings.Contains(n.Text, "$6.00") && strings.Contains(n.Text, "$5.00") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the notice must name the spend and the cap, got %v", h.fe.notices())
	}
}

func TestSwarmRemoteRowNeverConsultsTheSwap(t *testing.T) {
	h := newHarness(t)
	h.create(t, "remote task")
	h.fetch.failing = "the swap must never be consulted"
	tbl, err := models.New(models.Model{
		ID: "qwen3.8-workers", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384,
		Role: models.RoleWorker, Remote: true, Provider: "openrouter",
		BaseURL: "https://openrouter.ai/api/v1", APIKey: "sk-test", Concurrency: 2,
	})
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	h.ctl = swarm.New(swarm.Opts{
		TodoDB:        h.todoDB,
		SchedDB:       h.schedDB,
		Home:          h.home,
		Project:       func(ctx context.Context, session string) (todostore.Project, error) { return proj, nil },
		Cwd:           h.cwd,
		WorkerCmd:     []string{"/x/rig"},
		Fetch:         h.fetch.fetch,
		Spawn:         h.spawn.spawn,
		SwapURL:       "http://127.0.0.1:8090",
		Sandbox:       "off",
		RigHome:       h.rigHome,
		StateDir:      t.TempDir(),
		FleetModel:    "qwen3.8-workers",
		ReviewerModel: "qwen3.8-review",
		Models:        func() models.Table { return tbl },
		Poll:          20 * time.Millisecond,
		Frontend:      h.fe,
	})
	t.Cleanup(func() { h.ctl.Stop() })
	h.start(t, swarm.StartOpts{Count: 1, Role: "worker"})
	h.waitFor(t, "the task to complete", func() bool {
		return h.status(t, "t1") == "review"
	})
	if got := h.spawn.count(); got != 1 {
		t.Fatalf("spawn calls = %d, want 1", got)
	}
}
