package scheduler_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

type envSpawn struct {
	calls  []fakeCall
	envs   [][]string
	result sched.SpawnResult
	err    error
}

func (f *envSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
	f.calls = append(f.calls, fakeCall{Argv: argv, Cwd: cwd, Ctx: ctx})
	f.envs = append(f.envs, env)
	return f.result, f.err
}

func carriesSetenv(argv []string, key, value string) bool {
	for i := 0; i+2 < len(argv); i++ {
		if argv[i] == "--setenv" && argv[i+1] == key && argv[i+2] == value {
			return true
		}
	}
	return false
}

func linuxJail(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the worker jail is linux-only")
	}
}

func linuxLandlock(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("the landlock profile is linux/amd64 or linux/arm64")
	}
}

func shortSock(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "sched")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return filepath.Join(d, "rig.sock")
}

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
		RigHome:   h.rigHome,
	}
}

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

func TestJailedRunRefusesLoudWithoutBwrap(t *testing.T) {
	linuxJail(t)
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)
	t.Setenv("PATH", t.TempDir())
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

func TestSandboxOffRunsUnjailedWithTheOneLoudLine(t *testing.T) {
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)
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

func TestJailedRunCarriesTheScratchHome(t *testing.T) {
	linuxJail(t)
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)

	if err := os.MkdirAll(filepath.Join(h.home, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}

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
	if want := filepath.Join(cwd, ".rig-job"); !carriesSetenv(argv, "RIG_HOME", want) {
		t.Fatalf("the jailed argv must carry RIG_HOME=%q as a --setenv pair: %v", want, argv)
	}
	if got := os.Getenv("RIG_HOME"); got == filepath.Join(cwd, ".rig-job") {
		t.Fatal("the scratch home must not leak into the runner's env after the run")
	}

	if _, err := os.Stat(filepath.Join(cwd, ".rig-job", key+".sock")); !os.IsNotExist(err) {
		t.Fatalf("the run's socket must be removed after the spawn (stat: %v)", err)
	}
}

func TestJailedRunCarriesTheFleetEnvIntoTheJail(t *testing.T) {
	linuxJail(t)
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)

	if err := os.MkdirAll(filepath.Join(h.home, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(shimDir, "bwrap"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	spawn := &envSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, runSandboxOpts(h, spawn.spawn, "")))
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	argv := spawn.calls[0].Argv
	if !carriesSetenv(argv, sched.FleetEnv, "0") {
		t.Fatalf("the jailed worker must learn the fleet door inside the jail (%s=0 as a --setenv pair), got %v", sched.FleetEnv, argv)
	}
	if _, w, ok := sched.FleetFrom(spawn.calls[0].Ctx); !ok || w == nil {
		t.Fatalf("the jailed spawn must carry the fleet pipe as a file descriptor, not only as a name (fleet in ctx: %v)", ok)
	}
}

func TestOverlappingFiresDoNotStompEachOthersSocket(t *testing.T) {
	linuxJail(t)
	cwd := t.TempDir()
	h, key1 := setupJob(t, cwd, nil)
	if _, err := h.create(sched.CreateInput{
		Name: "job-two", Prompt: "the second", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Busy: "skip",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(h.home, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(shimDir, "bwrap"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	entered := make(chan string, 2)
	release := make(chan struct{})
	spawn := func(ctx context.Context, argv []string, c string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
		for i, a := range argv {
			if a == "-base-url" {
				entered <- argv[i+1]
			}
		}
		<-release
		return sched.SpawnResult{Exit: 0}, nil
	}
	done := make(chan error, 2)
	for _, key := range []string{key1, "j2"} {
		key := key
		go func() {
			done <- sched.RunJob(key, runSandboxOpts(h, spawn, ""))
		}()
	}
	var socks []string
	for i := 0; i < 2; i++ {
		socks = append(socks, <-entered)
	}
	if socks[0] == socks[1] {
		t.Fatalf("two overlapping fires must each own a socket, both got %s", socks[0])
	}
	for _, sock := range socks {
		path := strings.TrimPrefix(sock, "unix:")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("each in-flight fire's socket must exist (%s): %v", path, err)
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		mustOK(t, <-done)
	}
}

func TestJailedRunCarriesThePixelsBinariesIntoTheJail(t *testing.T) {
	linuxJail(t)
	ffDir, brDir := t.TempDir(), t.TempDir()
	mk := func(dir, name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ff, br := mk(ffDir, "ffmpeg"), mk(brDir, "chrome-headless-shell")
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)
	if err := os.MkdirAll(filepath.Join(h.home, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(shimDir, "bwrap"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	spawn := &envSpawn{result: sched.SpawnResult{Exit: 0}}
	opts := runSandboxOpts(h, spawn.spawn, "")
	opts.FFmpeg = ff
	opts.Browser = br
	mustOK(t, sched.RunJob(key, opts))
	argv := spawn.calls[0].Argv
	joined := strings.Join(argv, " ")
	for _, c := range []struct{ knob, bin string }{
		{"RIG_FFMPEG", ff},
		{"RIG_BROWSER", br},
	} {
		if !carriesSetenv(argv, c.knob, c.bin) {
			t.Fatalf("%s must ride the jail env: %v", c.knob, argv)
		}
		dir := filepath.Dir(c.bin)
		if !strings.Contains(joined, "--ro-bind "+dir+" "+dir) {
			t.Fatalf("the binary's directory must ride the jail read-only, got %v", argv)
		}
	}

	spawn2 := &envSpawn{result: sched.SpawnResult{Exit: 0}}
	opts2 := runSandboxOpts(h, spawn2.spawn, "")
	mustOK(t, sched.RunJob(key, opts2))
	joined = strings.Join(spawn2.calls[0].Argv, " ")
	if strings.Contains(joined, "RIG_FFMPEG") || strings.Contains(joined, "RIG_BROWSER") {
		t.Fatalf("an unset knob must not ride the env: %v", spawn2.calls[0].Argv)
	}
}

func TestJailedRunRefusesOnANonLinuxPlatform(t *testing.T) {
	v := sched.PlatformRefusal("windows")
	if !strings.Contains(v, "windows") || !strings.Contains(v, "linux") || !strings.Contains(v, "jailed") {
		t.Fatalf("the refusal must name the platform, the linux-only fact, and the profile: %q", v)
	}
}

func TestJailedRunCarriesTheInterpreterIntoTheJail(t *testing.T) {
	linuxJail(t)
	venv := t.TempDir()
	py := venvAt(t, venv)
	for _, c := range []struct {
		name   string
		python string
		want   bool
	}{
		{"set", py, true},
		{"unset", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cwd := t.TempDir()
			h, key := setupJob(t, cwd, nil)
			if err := os.MkdirAll(filepath.Join(h.home, "kernel"), 0o755); err != nil {
				t.Fatal(err)
			}
			shimDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(shimDir, "bwrap"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			spawn := &envSpawn{result: sched.SpawnResult{Exit: 0}}
			opts := runSandboxOpts(h, spawn.spawn, "")
			opts.Python = c.python
			mustOK(t, sched.RunJob(key, opts))
			if len(spawn.calls) != 1 {
				t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
			}
			argv := spawn.calls[0].Argv
			if got := carriesSetenv(argv, "RIG_PYTHON", py); got != c.want {
				t.Fatalf("RIG_PYTHON on the jail env = %v, want %v: %v", got, c.want, argv)
			}
			if got := strings.Contains(strings.Join(argv, " "), "--ro-bind "+venv+" "+venv); got != c.want {
				t.Fatalf("the interpreter's venv ro-bind = %v, want %v: %v", got, c.want, argv)
			}
		})
	}
}
