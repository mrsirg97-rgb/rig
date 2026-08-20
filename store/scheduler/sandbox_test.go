package scheduler_test

// The runner's sandbox cases (SPEC_SANDBOX 1, 5, testing): fail closed
// when bwrap is absent (the outcome row carries the refusal), sandbox
// off runs unjailed with the one loud line, and the jailed run's
// scratch home rides the worker's env.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

// envSpawn is a spawn fake that also records the worker's env at the
// call (the scratch home's seam, SPEC_SANDBOX 1).
type envSpawn struct {
	calls   []fakeCall
	homeEnv []string
	result  sched.SpawnResult
	err     error
}

func (f *envSpawn) spawn(ctx context.Context, argv []string, cwd string) (sched.SpawnResult, error) {
	f.calls = append(f.calls, fakeCall{Argv: argv, Cwd: cwd})
	f.homeEnv = append(f.homeEnv, os.Getenv("RIG_HOME"))
	return f.result, f.err
}

// runSandboxOpts is the runner's opts with the sandbox seam set.
func runSandboxOpts(h *harness, s sched.Spawn, profile string) sched.RunOpts {
	return sched.RunOpts{
		Home:      h.home,
		Crontab:   h.ct,
		Fetch:     fakeFetch(nil, fetchOpts{}),
		Spawn:     s,
		WorkerCmd: []string{"/x/rig"},
		SwapURL:   "http://127.0.0.1:8090",
		Now:       func() time.Time { return runnerNow },
		Sandbox:   profile,
		RigHome:   h.home,
	}
}

// captureStderr redirects os.Stderr around f and returns what it held.
func captureStderrRun(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	f()
	w.Close()
	os.Stderr = old
	buf, _ := io.ReadAll(r)
	return string(buf)
}

// TestJailedRunRefusesLoudWithoutBwrap (SPEC_SANDBOX 1, fail closed):
// profile jailed and no bwrap on $PATH refuses the run; the outcome
// row carries the refusal (bwrap and the profile named, both settings
// keys taught); nothing spawns; the crontab stays untouched.
func TestJailedRunRefusesLoudWithoutBwrap(t *testing.T) {
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, "", nil)
	t.Setenv("PATH", t.TempDir()) // scrubbed: no bwrap, nothing
	spawn := &envSpawn{}
	before := h.ct.text
	err := sched.RunJob(key, runSandboxOpts(h, spawn.spawn, ""))
	mustOK(t, err)
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" {
		t.Fatalf("the refusal is a recorded skip, got %v", rec.Args)
	}
	reason := toString(rec.Args["reason"])
	for _, needle := range []string{"bwrap", "jailed", "sandbox", "sandboxBinds"} {
		if !strings.Contains(reason, needle) {
			t.Fatalf("the refusal must name %q, got %q", needle, reason)
		}
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn on a fail-closed refusal")
	}
	if h.ct.text != before {
		t.Fatal("the crontab must stay untouched")
	}
}

// TestSandboxOffRunsUnjailedWithTheOneLoudLine (SPEC_SANDBOX 1, 5):
// the operator's explicit act — the worker runs as today (the plain
// argv, the swap endpoint), and exactly one loud line per worker run
// names the unjailed fact.
func TestSandboxOffRunsUnjailedWithTheOneLoudLine(t *testing.T) {
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, "", nil)
	spawn := &envSpawn{result: sched.SpawnResult{Exit: 0}}

	stderr := captureStderrRun(t, func() {
		mustOK(t, sched.RunJob(key, runSandboxOpts(h, spawn.spawn, "off")))
	})
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1 (off is a run, not a skip)", len(spawn.calls))
	}
	argv := spawn.calls[0].Argv
	if argv[0] != "/x/rig" || argv[1] != "-p" {
		t.Fatalf("the off profile is today's plain argv, got %v", argv)
	}
	baseIdx := -1
	for i, a := range argv {
		if a == "-base-url" {
			baseIdx = i
		}
	}
	if baseIdx < 0 || argv[baseIdx+1] != "http://127.0.0.1:8090/v1" {
		t.Fatalf("the off profile dials the swap endpoint as today: %v", argv)
	}
	if strings.Count(stderr, "sandbox off") != 1 {
		t.Fatalf("exactly one loud line per worker run, got %q", stderr)
	}
	if !strings.Contains(stderr, "unjailed") {
		t.Fatalf("the line must name the unjailed run: %q", stderr)
	}
}

// TestJailedRunCarriesTheScratchHome (SPEC_SANDBOX 1): the worker's
// rig home is the scratch home inside the job's cwd (RIG_HOME rides
// the spawn's env, restored after) — the worker's stores cannot land
// in the operator's home.
func TestJailedRunCarriesTheScratchHome(t *testing.T) {
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, "", nil)
	// the profile's kernel line (the operator home's kernel directory).
	if err := os.MkdirAll(filepath.Join(h.home, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	// bwrap resolvable (the refusal's named dependency).
	shimDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(shimDir, "bwrap"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	spawn := &envSpawn{result: sched.SpawnResult{Exit: 0}}
	err := sched.RunJob(key, runSandboxOpts(h, spawn.spawn, ""))
	mustOK(t, err)
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	argv := spawn.calls[0].Argv
	if argv[0] != filepath.Join(shimDir, "bwrap") || argv[1] != "--unshare-all" {
		t.Fatalf("the jailed profile is the bwrap command, got %v", argv)
	}
	if want := filepath.Join(cwd, ".rig-job"); spawn.homeEnv[0] != want {
		t.Fatalf("RIG_HOME during the spawn = %q, want the scratch home %q", spawn.homeEnv[0], want)
	}
	if got := os.Getenv("RIG_HOME"); got == filepath.Join(cwd, ".rig-job") {
		t.Fatal("the scratch home must not leak into the runner's env after the run")
	}
	// the socket is gone after the run (the hole is per-run).
	if _, err := os.Stat(filepath.Join(cwd, ".rig-job.sock")); !os.IsNotExist(err) {
		t.Fatalf("the run's socket must be removed after the spawn (stat: %v)", err)
	}
}

// TestJailedRunRefusesOnANonLinuxPlatform (SPEC_SANDBOX 1, testing):
// the named refusal on a non-linux build — the voice pins the
// linux-only fact, the platform, and the profile.
func TestJailedRunRefusesOnANonLinuxPlatform(t *testing.T) {
	v := sched.PlatformRefusal("windows")
	if !strings.Contains(v, "windows") || !strings.Contains(v, "linux") || !strings.Contains(v, "jailed") {
		t.Fatalf("the refusal must name the platform, the linux-only fact, and the profile: %q", v)
	}
}
