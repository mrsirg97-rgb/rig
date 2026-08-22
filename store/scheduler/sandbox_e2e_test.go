package scheduler_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

func requireJailBox(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the worker jail is linux-only")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap absent; the jail e2e needs bubblewrap")
	}
	probe := exec.Command("bwrap",
		"--unshare-all", "--die-with-parent",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/lib", "/lib",
		"--ro-bind", "/lib64", "/lib64",
		"--ro-bind", "/bin", "/bin",
		"--ro-bind", "/sbin", "/sbin",
		"--ro-bind", "/etc", "/etc",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
		"--", "/usr/bin/true")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("bwrap cannot run unprivileged on this box (check kernel.apparmor_restrict_unprivileged_userns): %v\n%s", err, out)
	}
}

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

func sharedRigBin(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		root, err := filepath.Abs("../../..")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(os.TempDir(), "rig-jail-e2e-bin", "rig")
		out, err := exec.Command("go", "build", "-o", binPath, filepath.Join(root, "cmd", "rig")).CombinedOutput()
		if err != nil {
			binErr = fmt.Errorf("%v\n%s", err, out)
			return
		}
	})
	if binErr != nil {
		t.Fatalf("build the rig binary: %v", binErr)
	}
	return binPath
}

type jailSrv struct {
	mu      sync.Mutex
	bodies  [][]byte
	replies []string
}

func newJailSrv(t *testing.T, s *jailSrv) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method == "GET" {
			w.Write([]byte(`{"data":[]}`))
			return
		}
		s.mu.Lock()
		i := len(s.bodies)
		s.bodies = append(s.bodies, body)
		reply := s.replies[len(s.replies)-1]
		if i < len(s.replies) {
			reply = s.replies[i]
		}
		s.mu.Unlock()
		w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *jailSrv) body(i int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.bodies) {
		return nil
	}
	return s.bodies[i]
}

func (s *jailSrv) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func bashCall(t *testing.T, id, command string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	return toolCallReplyJail(id, "bash", string(b))
}

func pythonCall(t *testing.T, id, code string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatal(err)
	}
	return toolCallReplyJail(id, "python", string(b))
}

func toolCallReplyJail(id, name, argumentsJSON string) string {
	esc := strings.ReplaceAll(argumentsJSON, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	return `data: {"choices":[{"delta":{"tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":"` + esc + `"}}]},"finish_reason":"tool_calls"}]}` + "\n"
}

const jailFinalReply = `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}` + "\n"

func toolResultOfJail(t *testing.T, body []byte) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal the captured body: %v", err)
	}
	last := ""
	for _, m := range req.Messages {
		if m.Role == "tool" {
			last = m.Content
		}
	}
	if last == "" {
		t.Fatal("the request carries no tool message")
	}
	return last
}

func jailFixture(t *testing.T, replies []string, binds []string, py string) (string, *jailSrv) {
	t.Helper()
	requireJailBox(t)
	bin := sharedRigBin(t)
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, "", nil)

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

	t.Setenv("HOME", "/home/fixture")
	if py != "" {
		t.Setenv("RIG_PYTHON", py)
	}

	s := &jailSrv{replies: replies}
	srv := newJailSrv(t, s)

	err = sched.RunJob(key, sched.RunOpts{
		Home:         h.home,
		Crontab:      h.ct,
		Fetch:        fakeFetch(nil, fetchOpts{}),
		Spawn:        sched.RealSpawn,
		WorkerCmd:    []string{bin},
		SwapURL:      srv.URL,
		Sandbox:      "jailed",
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
		t.Fatalf("the fixture run must be ok, got %v", rec[0].Args)
	}
	if s.count() < 2 {
		t.Fatalf("the worker's model calls must ride the socket (requests = %d)", s.count())
	}

	if _, err := os.Stat(filepath.Join(cwd, ".rig-job.sock")); !os.IsNotExist(err) {
		t.Fatalf("the run's socket must be gone after the run (stat: %v)", err)
	}

	if fi, err := os.Stat(filepath.Join(cwd, ".rig-job")); err != nil || !fi.IsDir() {
		t.Fatalf("the scratch home must exist in the job's cwd (stat: %v)", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(cwd, ".rig-job", "sessions")); len(entries) == 0 {
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

func TestJailHomeAbsent(t *testing.T) {
	requireJailBox(t)
	cwd, srv := jailFixture(t, []string{
		bashCall(t, "c1", `ls ~ 2>&1; echo rc=$?`),
		jailFinalReply,
	}, nil, "")
	_ = cwd
	result := toolResultOfJail(t, lastBodyOf(t, srv))
	if !strings.Contains(result, "No such file or directory") {
		t.Fatalf("the home must be absent in the jail, got %q", result)
	}
	if !strings.Contains(result, "/home/fixture") {
		t.Fatalf("the refusal must name the (absent) home, got %q", result)
	}
}

func TestJailNoNetwork(t *testing.T) {
	requireJailBox(t)
	_, srv := jailFixture(t, []string{
		bashCall(t, "c1", `curl -s -m 5 http://127.0.0.1:8090/ 2>&1; echo rc=$?`),
		jailFinalReply,
	}, nil, "")
	result := toolResultOfJail(t, lastBodyOf(t, srv))
	if !regexp.MustCompile(`rc=[1-9]`).MatchString(result) {
		t.Fatalf("the curl must fail (no network), got %q", result)
	}
}

func TestJailWriteInsideLandsOutsideRefuses(t *testing.T) {
	requireJailBox(t)
	cwd, srv := jailFixture(t, []string{
		bashCall(t, "c1", `printf inside > inside.txt && cat inside.txt; echo rc=$?`),
		bashCall(t, "c2", `printf x > /etc/jailtest 2>&1; echo rc=$?`),
		jailFinalReply,
	}, nil, "")

	got, err := os.ReadFile(filepath.Join(cwd, "inside.txt"))
	if err != nil || string(got) != "inside" {
		t.Fatalf("the inside write must land (err=%v, got=%q)", err, got)
	}

	result := toolResultOfJail(t, lastBodyOf(t, srv))
	if !strings.Contains(result, "Read-only file system") {
		t.Fatalf("the outside write must refuse on the ro system, got %q", result)
	}
	if _, err := os.Stat("/etc/jailtest"); !os.IsNotExist(err) {
		t.Fatalf("the host's /etc must be untouched (stat: %v)", err)
	}
}

func TestJailPythonSeesTheSameWalls(t *testing.T) {
	requireJailBox(t)
	py := kernelPython(t)
	venv := filepath.Dir(filepath.Dir(py))
	cwd, srv := jailFixture(t, []string{
		pythonCall(t, "c1", "import os\nhome = os.environ.get('HOME')\nprint('home_exists:', os.path.exists(home))\nopen('pyfile.txt', 'w').write('py')\nprint('wrote: pyfile.txt')"),
		jailFinalReply,
	}, []string{venv, "/etc/alternatives"}, py)
	result := toolResultOfJail(t, lastBodyOf(t, srv))
	if !strings.Contains(result, "home_exists: False") {
		t.Fatalf("the python kernel must see the absent home, got %q", result)
	}
	if !strings.Contains(result, "wrote: pyfile.txt") {
		t.Fatalf("the python kernel must write in the cwd, got %q", result)
	}
	if got, err := os.ReadFile(filepath.Join(cwd, "pyfile.txt")); err != nil || string(got) != "py" {
		t.Fatalf("the python write must land in the cwd (err=%v, got=%q)", err, got)
	}
}

func TestJailSocketIsTheOnlyHole(t *testing.T) {
	requireJailBox(t)

	_, srv := jailFixture(t, []string{
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

func lastBodyOf(t *testing.T, s *jailSrv) []byte {
	t.Helper()
	if b := s.body(s.count() - 1); b != nil {
		return b
	}
	t.Fatal("the fixture captured no request (the worker never dialed the socket)")
	return nil
}

func kernelPython(t *testing.T) string {
	t.Helper()
	if h, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(h, ".pi", "agent", "kernel-venv", "bin", "python")
		if fi, err := os.Stat(p); err == nil && fi.Mode()&0o111 != 0 {
			if _, err := exec.Command(p, "-c", "import IPython, numpy, pandas").CombinedOutput(); err == nil {
				return p
			}
		}
	}
	t.Skip("no shared kernel venv with IPython, numpy, pandas (the python fixture needs one)")
	return ""
}
