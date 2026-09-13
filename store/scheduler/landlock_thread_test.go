package scheduler_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

// The worker's own goroutines must be inside the domain. An in-process
// restrict cannot cover them (restrict_self commits per-thread creds and
// Go's runtime has threads before main), so the domain arrives with the
// image: the runner spawns rig -exec rig -p ..., the exec'd worker's every
// thread inherits the wall. This test drives the worker's in-process tools
// against a path outside every grant.
func TestLandlockWorkerThreadsAreInsideTheDomain(t *testing.T) {
	requireLandlockBox(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(home, ".bashrc")
	if _, err := os.Stat(probe); err != nil {
		t.Skipf("the probe file is absent: %v", err)
	}

	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)
	if err := os.MkdirAll(filepath.Join(h.home, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(cwd, ".rig-job")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	modelsJSON := `[{"id":"qwen3.8-workers","window":65536,"maxTokens":8192,"reserve":8192,"keepRecent":16384,"role":"worker"}]`
	if err := os.WriteFile(filepath.Join(scratch, "models.json"), []byte(modelsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &jailSrv{replies: []string{
		fileCall(t, "c1", "read", map[string]any{"path": probe}),
		jailFinalReply,
	}}
	srv := newJailSrv(t, s)
	bin := sharedRigBin(t)

	err = sched.RunJob(key, sched.RunOpts{
		Home:      h.home,
		Crontab:   h.ct,
		Fetch:     fakeFetch(nil, fetchOpts{}),
		Spawn:     sched.RealSpawn,
		WorkerCmd: []string{bin},
		SwapURL:   srv.URL,
		Sandbox:   "landlock",
		RigHome:   h.home,
		Now:       func() time.Time { return runnerNow },
	})
	mustOK(t, err)

	rec := runEvents(t, h, "")
	if len(rec) != 1 || rec[0].Args["status"] != "ok" {
		t.Fatalf("the worker must complete, got %v", rec)
	}
	result := toolResultOfJail(t, lastBodyOf(t, s))
	if !strings.Contains(result, "permission denied") {
		t.Fatalf("an in-process read from the worker's threads must refuse, got %q", result)
	}
}
