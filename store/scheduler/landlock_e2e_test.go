package scheduler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

func requireLandlockBox(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("the landlock profile is linux/amd64 or linux/arm64")
	}
	abi, err := sched.LandlockABI()
	if err != nil {
		t.Skipf("landlock is unavailable on this box: %v", err)
	}
	if abi < 4 {
		t.Skipf("landlock ABI %d < 4 (no netless guarantee)", abi)
	}
}

func landlockFixture(t *testing.T, replies []string, binds []string, py string) (string, *jailSrv) {
	t.Helper()
	requireLandlockBox(t)
	bin := sharedRigBin(t)
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)

	opHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(opHome, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opHome, "marker"), []byte("operator data"), 0o644); err != nil {
		t.Fatal(err)
	}
	markerBefore, err := os.Stat(filepath.Join(opHome, "marker"))
	if err != nil {
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
	if py != "" {
		venv := filepath.Dir(filepath.Dir(py))
		binds = append(binds, venv)
		piHome := filepath.Join(scratch, ".pi", "agent")
		if err := os.MkdirAll(piHome, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(venv, filepath.Join(piHome, "kernel-venv")); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("HOME", "/home/fixture")

	s := &jailSrv{replies: replies}
	srv := newJailSrv(t, s)

	err = sched.RunJob(key, sched.RunOpts{
		Home:         h.home,
		Crontab:      h.ct,
		Fetch:        fakeFetch(nil, fetchOpts{}),
		Spawn:        sched.RealSpawn,
		WorkerCmd:    []string{bin},
		SwapURL:      srv.URL,
		Sandbox:      "landlock",
		SandboxBinds: binds,
		RigHome:      opHome,
		Now:          func() time.Time { return runnerNow },
	})
	mustOK(t, err)

	rec := runEvents(t, h, "")
	if len(rec) != 1 {
		t.Fatalf("exactly one run record, got %d", len(rec))
	}
	if rec[0].Args["status"] != "ok" {
		logRel := toString(rec[0].Args["log"])
		content, _ := os.ReadFile(filepath.Join(h.home, logRel))
		t.Fatalf("the fixture run must be ok, got %v\n%s", rec[0].Args, content)
	}
	if s.count() < 2 {
		t.Fatalf("the worker's model calls must ride the socket (requests = %d)", s.count())
	}
	if _, err := os.Stat(filepath.Join(cwd, ".rig-job.sock")); !os.IsNotExist(err) {
		t.Fatalf("the run's socket must be gone after the run (stat: %v)", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(scratch, "sessions")); len(entries) == 0 {
		t.Fatalf("the worker's session store must land in the scratch home")
	}

	markerAfter, err := os.Stat(filepath.Join(opHome, "marker"))
	if err != nil {
		t.Fatal(err)
	}
	if !markerBefore.ModTime().Equal(markerAfter.ModTime()) {
		t.Fatal("the operator's home must be untouched (the marker's mtime moved)")
	}
	return cwd, s
}

func TestLandlockOperatorHomeAbsent(t *testing.T) {
	requireLandlockBox(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	_, srv := landlockFixture(t, []string{
		bashCall(t, "c1", `rm -f /tmp/rigll-probe; ls `+home+` 2>&1; echo "home-rc=$?"; touch /usr/bin/rigll-probe 2>&1; echo "usr-rc=$?"; touch /tmp/rigll-probe 2>&1; echo "tmp-rc=$?"; touch /etc/rigll-probe 2>&1; echo "etc-rc=$?"; ls /usr/bin >/dev/null 2>&1; echo "usrread-rc=$?"`),
		jailFinalReply,
	}, nil, "")
	result := toolResultOfJail(t, lastBodyOf(t, srv))
	if !strings.Contains(result, "Permission denied") {
		t.Fatalf("the operator's home must be denied in the landlock profile, got %q", result)
	}
	if !strings.Contains(result, home) {
		t.Fatalf("the refusal must name the denied home, got %q", result)
	}
}

func TestLandlockNoTCP(t *testing.T) {
	requireLandlockBox(t)
	_, srv := landlockFixture(t, []string{
		bashCall(t, "c1", `curl -s -m 5 http://127.0.0.1:8090/ 2>&1; echo rc=$?`),
		jailFinalReply,
	}, nil, "")
	result := toolResultOfJail(t, lastBodyOf(t, srv))
	if !regexp.MustCompile(`rc=[1-9]`).MatchString(result) {
		t.Fatalf("the curl must fail (no TCP), got %q", result)
	}
}

func TestLandlockWriteInsideLandsOutsideRefuses(t *testing.T) {
	requireLandlockBox(t)
	cwd, srv := landlockFixture(t, []string{
		bashCall(t, "c1", `printf inside > inside.txt && cat inside.txt; echo rc=$?`),
		bashCall(t, "c2", `rm -f /etc/rigll-out; printf x > /etc/rigll-out 2>&1; echo rc=$?`),
		bashCall(t, "c3", `rm -f /tmp/rigll-out; printf x > /tmp/rigll-out 2>&1; echo rc=$?`),
		jailFinalReply,
	}, nil, "")

	got, err := os.ReadFile(filepath.Join(cwd, "inside.txt"))
	if err != nil || string(got) != "inside" {
		t.Fatalf("the inside write must land (err=%v, got=%q)", err, got)
	}
	result := toolResultOfJail(t, lastBodyOf(t, srv))
	if !strings.Contains(result, "Permission denied") {
		t.Fatalf("the outside writes must refuse with the landlock voice, got %q", result)
	}
	if _, err := os.Stat("/etc/rigll-out"); !os.IsNotExist(err) {
		t.Fatalf("the host's /etc must be untouched (stat: %v)", err)
	}
	if _, err := os.Stat("/tmp/rigll-out"); !os.IsNotExist(err) {
		t.Fatalf("the host's /tmp must be untouched (stat: %v)", err)
	}
}

func TestLandlockPythonSeesTheSameWalls(t *testing.T) {
	requireLandlockBox(t)
	py := kernelPython(t)
	cwd, srv := landlockFixture(t, []string{
		pythonCall(t, "c1", "open('pyfile.txt', 'w').write('py')\nprint('wrote: pyfile.txt')\ntry:\n    open('/etc/ll-py', 'w').write('x')\n    print('etc: writable')\nexcept PermissionError:\n    print('etc: denied')\ntry:\n    open('/tmp/rigll-py', 'w').write('x')\n    print('tmp: writable')\nexcept PermissionError:\n    print('tmp: denied')"),
		jailFinalReply,
	}, nil, py)
	result := toolResultOfJail(t, lastBodyOf(t, srv))
	if !strings.Contains(result, "wrote: pyfile.txt") {
		t.Fatalf("the python kernel must write in the cwd, got %q", result)
	}
	if !strings.Contains(result, "etc: denied") || !strings.Contains(result, "tmp: denied") {
		t.Fatalf("the python kernel must see the same walls, got %q", result)
	}
	if got, err := os.ReadFile(filepath.Join(cwd, "pyfile.txt")); err != nil || string(got) != "py" {
		t.Fatalf("the python write must land in the cwd (err=%v, got=%q)", err, got)
	}
	if _, err := os.Stat("/etc/ll-py"); !os.IsNotExist(err) {
		t.Fatalf("the host's /etc must be untouched (stat: %v)", err)
	}
	if _, err := os.Stat("/tmp/rigll-py"); !os.IsNotExist(err) {
		t.Fatalf("the host's /tmp must be untouched (stat: %v)", err)
	}
}

func TestLandlockSocketIsTheOnlyHole(t *testing.T) {
	requireLandlockBox(t)
	_, srv := landlockFixture(t, []string{
		bashCall(t, "c1", `echo through; echo rc=$?`),
		jailFinalReply,
	}, nil, "")
	body := lastBodyOf(t, srv)
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal the destination's request: %v", err)
	}
	if req.Model != "qwen3.8-workers" {
		t.Fatalf("the socket forwards the job's model call (model %q)", req.Model)
	}
}

func fileCall(t *testing.T, id, name string, args map[string]any) string {
	t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return toolCallReplyJail(id, name, string(b))
}

func TestLandlockInProcessReadRefusesOutside(t *testing.T) {
	requireLandlockBox(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	_, srv := landlockFixture(t, []string{
		fileCall(t, "c1", "read", map[string]any{"path": filepath.Join(home, ".bashrc")}),
		jailFinalReply,
	}, nil, "")
	result := toolResultOfJail(t, lastBodyOf(t, srv))
	if !strings.Contains(result, "permission denied") {
		t.Fatalf("an in-process read outside the grants must refuse, got %q", result)
	}
}

func TestLandlockInProcessWriteOutsideRefuses(t *testing.T) {
	requireLandlockBox(t)
	_, srv := landlockFixture(t, []string{
		fileCall(t, "c1", "write", map[string]any{"path": "/tmp/rigll-write", "content": "x"}),
		jailFinalReply,
	}, nil, "")
	result := toolResultOfJail(t, lastBodyOf(t, srv))
	if !strings.Contains(result, "permission denied") {
		t.Fatalf("an in-process write outside the cwd must refuse, got %q", result)
	}
	if _, err := os.Stat("/tmp/rigll-write"); !os.IsNotExist(err) {
		t.Fatalf("the host's /tmp must be untouched (stat: %v)", err)
	}
}
