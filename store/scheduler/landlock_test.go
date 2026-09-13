package scheduler_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

type landlockEnvSpawn struct {
	calls  []fakeCall
	env    []string
	result sched.SpawnResult
	err    error
}

func (f *landlockEnvSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string) (sched.SpawnResult, error) {
	f.calls = append(f.calls, fakeCall{Argv: argv, Cwd: cwd})
	f.env = env
	return f.result, f.err
}

func landlockRunOpts(h *harness, s sched.Spawn, abi int, abiErr error) sched.RunOpts {
	return sched.RunOpts{
		Home:        h.home,
		Crontab:     h.ct,
		Fetch:       fakeFetch(nil, fetchOpts{}),
		Spawn:       s,
		WorkerCmd:   []string{"/x/rig"},
		SwapURL:     "http://127.0.0.1:8090",
		Now:         func() time.Time { return runnerNow },
		Sandbox:     "landlock",
		RigHome:     h.home,
		LandlockABI: func() (int, error) { return abi, abiErr },
	}
}

func envAt(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix)
		}
	}
	return ""
}

func TestLandlockRunCarriesTheNamedEnvAndTheOneSocket(t *testing.T) {
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)
	if err := os.MkdirAll(filepath.Join(h.home, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	spawn := &landlockEnvSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, landlockRunOpts(h, spawn.spawn, 6, nil)))
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1 (landlock is a run, not a skip)", len(spawn.calls))
	}
	argv := spawn.calls[0].Argv
	if argv[0] != "/x/rig" || argv[1] != "-p" {
		t.Fatalf("the landlock worker is today's plain argv, got %v", argv)
	}
	baseIdx := -1
	for i, a := range argv {
		if a == "-base-url" {
			baseIdx = i
		}
	}
	scratch := filepath.Join(cwd, ".rig-job")
	if baseIdx < 0 || argv[baseIdx+1] != "unix:"+filepath.Join(cwd, ".rig-job.sock") {
		t.Fatalf("the worker must dial the one socket, got %v", argv)
	}
	env := spawn.env
	if len(env) != 6 {
		t.Fatalf("the env is the named list only (len %d: %v)", len(env), env)
	}
	for k, v := range map[string]string{
		"RIG_HOME": scratch,
		"HOME":     scratch,
		"TMPDIR":   filepath.Join(scratch, "tmp"),
	} {
		if got := envAt(env, k); got != v {
			t.Fatalf("env %s = %q, want the scratch %q", k, got, v)
		}
	}
	if path := envAt(env, "PATH"); !strings.Contains(path, "/usr/bin") || !strings.Contains(path, "/bin") {
		t.Fatalf("PATH = %q, want the jail's path", path)
	}
	if got := envAt(env, "RIG_EXEC_WRAPPER"); got != "/x/rig" {
		t.Fatalf("RIG_EXEC_WRAPPER = %q, want the worker binary (the exec helper)", got)
	}
	specJSON := envAt(env, "RIG_LANDLOCK")
	var spec sched.LandlockSpec
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		t.Fatalf("RIG_LANDLOCK must carry the spec json: %v (%q)", err, specJSON)
	}
	if spec.V != 1 || spec.Cwd != cwd || spec.Scratch != scratch ||
		spec.Kernel != filepath.Join(h.home, "kernel") || spec.Binary != "/x/rig" {
		t.Fatalf("the spec must carry the grants, got %+v", spec)
	}
	if _, err := os.Stat(filepath.Join(scratch, "tmp")); err != nil {
		t.Fatalf("the scratch tmp must exist before the worker runs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".rig-job.sock")); !os.IsNotExist(err) {
		t.Fatalf("the run's socket must be gone after the run (stat: %v)", err)
	}
	if got := os.Getenv("RIG_HOME"); got == scratch {
		t.Fatal("the scratch home must not leak into the runner's env after the run")
	}
}

func TestLandlockRunRefusesOnAnOldABI(t *testing.T) {
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)
	spawn := &landlockEnvSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, landlockRunOpts(h, spawn.spawn, 3, nil)))
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" {
		t.Fatalf("the refusal is a recorded skip, got %v", rec.Args)
	}
	reason := toString(rec.Args["reason"])
	for _, needle := range []string{"landlock", "ABI", "4"} {
		if !strings.Contains(reason, needle) {
			t.Fatalf("the refusal must name %q, got %q", needle, reason)
		}
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn on a fail-closed refusal")
	}
}

func TestLandlockRunRefusesWhenTheProbeFails(t *testing.T) {
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)
	spawn := &landlockEnvSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, landlockRunOpts(h, spawn.spawn, 0, errors.New("no landlock"))))
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" {
		t.Fatalf("the refusal is a recorded skip, got %v", rec.Args)
	}
	reason := toString(rec.Args["reason"])
	if !strings.Contains(reason, "landlock") || !strings.Contains(reason, "no landlock") {
		t.Fatalf("the refusal must name the probe failure, got %q", reason)
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn on a fail-closed refusal")
	}
}

func TestLandlockRunRefusesOnAMissingBind(t *testing.T) {
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)
	if err := os.MkdirAll(filepath.Join(h.home, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := landlockRunOpts(h, (&landlockEnvSpawn{result: sched.SpawnResult{Exit: 0}}).spawn, 6, nil)
	opts.SandboxBinds = []string{"/definitely/not/a/landlock/bind"}
	mustOK(t, sched.RunJob(key, opts))
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" {
		t.Fatalf("the refusal is a recorded skip, got %v", rec.Args)
	}
	reason := toString(rec.Args["reason"])
	if !strings.Contains(reason, "sandboxBinds[0]") || !strings.Contains(reason, "/definitely/not/a/landlock/bind") {
		t.Fatalf("the refusal must name the missing bind entry, got %q", reason)
	}
}

func TestApplyLandlockNoopWithoutASpec(t *testing.T) {
	if err := sched.ApplyLandlock(""); err != nil {
		t.Fatalf("no RIG_LANDLOCK means no confinement, got %v", err)
	}
}

func TestApplyLandlockRefusesMalformedSpec(t *testing.T) {
	err := sched.ApplyLandlock("{not json")
	if err == nil || !strings.Contains(err.Error(), "profile") || !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("a malformed spec must refuse naming the profile and the parse, got %v", err)
	}
}

func TestApplyLandlockRefusesUnknownVersion(t *testing.T) {
	err := sched.ApplyLandlock(`{"v":2,"cwd":"/tmp","scratch":"/tmp/x","binary":"/bin/true"}`)
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("an unknown spec version must refuse, got %v", err)
	}
}

func TestApplyLandlockRefusesMissingGrantPath(t *testing.T) {
	cwd := t.TempDir()
	scratch := filepath.Join(cwd, ".rig-job")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := map[string]any{
		"v": 1, "cwd": cwd, "scratch": scratch,
		"kernel": "/definitely/missing/rig/kernel", "binary": "/bin/true",
	}
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	err = sched.ApplyLandlock(string(b))
	if err == nil || !strings.Contains(err.Error(), "/definitely/missing/rig/kernel") {
		t.Fatalf("a missing grant path must refuse naming it, got %v", err)
	}
}

func TestLandlockRefusalVoices(t *testing.T) {
	v := sched.LandlockPlatformRefusal("darwin")
	if !strings.Contains(v, "darwin") || !strings.Contains(v, "linux") || !strings.Contains(v, "landlock") {
		t.Fatalf("the platform refusal must name the platform, linux, and the profile: %q", v)
	}
	v = sched.LandlockArchRefusal("386")
	if !strings.Contains(v, "386") || !strings.Contains(v, "amd64") || !strings.Contains(v, "arm64") {
		t.Fatalf("the arch refusal must name the arch and the supported ones: %q", v)
	}
	v = sched.LandlockABIRefusal(3)
	if !strings.Contains(v, "3") || !strings.Contains(v, "4") || !strings.Contains(v, "landlock") {
		t.Fatalf("the ABI refusal must name the ABI, the need, and the profile: %q", v)
	}
	v = sched.LandlockProbeRefusal(errors.New("ENOSYS"))
	if !strings.Contains(v, "ENOSYS") || !strings.Contains(v, "landlock") {
		t.Fatalf("the probe refusal must name the failure and the profile: %q", v)
	}
}
