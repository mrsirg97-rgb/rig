package swarm_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/models"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
	"github.com/mrsirg97-rgb/rig/v2/swarm"
)

func allowArgv(argv []string) string {
	for i, a := range argv {
		if a == "-allow" {
			return argv[i+1]
		}
	}
	return ""
}

func TestSwarmWorkerAllowIsTheSessionListMinusDelegate(t *testing.T) {
	h := newHarness(t)
	h.create(t, "one task")
	ctl := swarm.New(swarm.Opts{
		TodoDB:  h.todoDB,
		SchedDB: h.schedDB,
		Home:    h.home,
		Project: func(ctx context.Context, session string) (todostore.Project, error) { return h.proj, nil },
		Cwd:     h.cwd, WorkerCmd: []string{"/x/rig"},
		Fetch: h.fetch.fetch, Spawn: h.spawn.spawn,
		SwapURL: "http://127.0.0.1:8090", Sandbox: "off",
		RigHome: h.rigHome, StateDir: t.TempDir(),
		DefaultModel: "qwen3.8-workers",
		Models:       func() models.Table { return modelRows(t) },
		Engine:       h.engine, Room: h.room,
		Allow: []string{"bash", "read", "todo", "delegate"},
	})
	t.Cleanup(func() { ctl.Stop() })
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := ctl.Start(ctx, swarm.StartOpts{Count: 1, Role: "worker"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	h.waitFor(t, "the worker's spawn", func() bool { return h.spawn.count() == 1 })
	if got := allowArgv(h.spawn.calls[0].Argv); got != "bash,read,todo" {
		t.Fatalf("the swarm worker's allow = %q, want the session's list minus delegate (2.14.1 keeps this list)", got)
	}
}
