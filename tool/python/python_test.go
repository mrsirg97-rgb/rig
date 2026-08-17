// Package python tests: pane's named cases, ported in pane's order
// against the real kernel. The file-level tests share one kernel, exactly
// as pane's test file shares one; the order-dependent state cases hold
// because of that. The fake-host cases drive stdlib-only stand-ins through
// the NewWith seam and skip only when there is no python3 at all: the seam
// provides everything and does not bootstrap the shared venv.
package python

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var suite *Tool

func TestMain(m *testing.M) {
	suite = New()
	code := m.Run()
	suite.Close()
	os.Exit(code)
}

// --- gates and helpers ---

// pythonAvailable names a usable python3: the shared venv first, then PATH.
func pythonAvailable(t *testing.T) string {
	t.Helper()
	py := defaultInterpreter()
	if fi, err := os.Stat(py); err == nil && fi.Mode()&0o111 != 0 {
		return py
	}
	p, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no venv interpreter and no python3 on PATH; the kernel cases need one")
	}
	return p
}

// requireKernel is pythonAvailable plus the real-kernel gate: IPython,
// numpy, and pandas must be importable, or the suite skips cleanly on a
// bare box. On a box with no venv but a working python3 the suite kernel
// bootstraps the venv on first use (pane's ensureKernel); the importability
// of the system interpreter is the availability signal for that path.
func requireKernel(t *testing.T) string {
	t.Helper()
	py := pythonAvailable(t)
	out, err := exec.Command(py, "-c", "import IPython, numpy, pandas").CombinedOutput()
	if err != nil {
		t.Skipf("IPython, numpy or pandas not importable by %s (bare box): %s", py, bytes.TrimSpace(out))
	}
	return py
}

func run(params map[string]any) (string, bool) {
	payload, _ := json.Marshal(params)
	text, err := suite.Exec(context.Background(), payload)
	return text, err == nil
}

func mustRun(t *testing.T, params map[string]any) (string, bool) {
	t.Helper()
	text, ok := run(params)
	t.Logf("python: %s", text)
	return text, ok
}

// call returns the fed-back text and the error voice (pane's reply.error;
// pane's tests assert on the error field, not the rendered text).
func call(t *testing.T, tl *Tool, params map[string]any) (string, error) {
	t.Helper()
	payload, _ := json.Marshal(params)
	text, err := tl.Exec(context.Background(), payload)
	t.Logf("python(seam): %s (err=%v)", text, err)
	return text, err
}

func matches(t *testing.T, pattern, text string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("pattern %q not found in:\n%s", pattern, text)
	}
}

func writeHost(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- pane's named cases, in pane's order ---

func TestExecutesCodeAndReportsTheResult(t *testing.T) {
	requireKernel(t)
	text, ok := mustRun(t, map[string]any{"code": "6 * 7"})
	if !ok {
		t.Fatalf("isError: %s", text)
	}
	matches(t, `Out\[.*\]: 42`, text)
}

func TestStatePersistsBetweenCalls(t *testing.T) {
	requireKernel(t)
	mustRun(t, map[string]any{"code": "x = 40"})
	text, ok := mustRun(t, map[string]any{"code": "x + 2"})
	if !ok {
		t.Fatalf("isError: %s", text)
	}
	matches(t, `Out\[.*\]: 42`, text)
}

func TestNumpyAndPandasAreImportable(t *testing.T) {
	requireKernel(t)
	text, ok := mustRun(t, map[string]any{"code": "import numpy, pandas; numpy.__version__"})
	if !ok {
		t.Fatalf("isError: %s", text)
	}
	matches(t, `Out\[.*\]: '\d+\.\d+\.\d+'`, text)
}

func TestVarsListsUserDefinedNamesOnly(t *testing.T) {
	requireKernel(t)
	// x from TestStatePersistsBetweenCalls: the shared kernel keeps state
	text, ok := mustRun(t, map[string]any{"action": "vars"})
	if !ok {
		t.Fatalf("isError: %s", text)
	}
	matches(t, `x: int`, text)
}

func TestResetClearsTheNamespace(t *testing.T) {
	requireKernel(t)
	mustRun(t, map[string]any{"action": "reset"})
	text, ok := mustRun(t, map[string]any{"action": "vars"})
	if !ok {
		t.Fatalf("isError: %s", text)
	}
	matches(t, `\(empty\)`, text)
}

func TestEmptyCallFailsLoudlyWithAClearMessage(t *testing.T) {
	requireKernel(t)
	text, ok := run(map[string]any{})
	if ok {
		t.Fatalf("empty call succeeded: %s", text)
	}
	matches(t, `no code supplied`, text)
}

func TestRuntimeErrorsAreReportedAsErrorsWithTracebackText(t *testing.T) {
	requireKernel(t)
	text, ok := run(map[string]any{"code": "1 / 0"})
	if ok {
		t.Fatalf("runtime error reported as success: %s", text)
	}
	matches(t, `ZeroDivisionError`, text)
}

func TestOversizedOutputIsClippedWithAnElisionMarker(t *testing.T) {
	requireKernel(t)
	text, ok := mustRun(t, map[string]any{"code": `print("a" * 100000)`})
	if !ok {
		t.Fatalf("isError: %s", text)
	}
	matches(t, `elided`, text)
	if len(text) >= 20_000 {
		t.Fatalf("output not clipped: %d chars", len(text))
	}
}

func TestHungCellTimesOutKernelRestartedCallerTold(t *testing.T) {
	requireKernel(t)
	text, ok := run(map[string]any{"code": "import time; time.sleep(30)", "timeoutMs": 1500})
	if ok {
		t.Fatalf("hung cell succeeded: %s", text)
	}
	matches(t, `timed out`, text)
	matches(t, `all variables are gone`, text)

	okText, ok := mustRun(t, map[string]any{"code": "1 + 1"})
	if !ok {
		t.Fatalf("kernel did not restart: %s", okText)
	}
	matches(t, `Out\[.*\]: 2`, okText)

	varsText, _ := mustRun(t, map[string]any{"action": "vars"})
	matches(t, `\(empty\)`, varsText)
}

func TestParallelCallsAreRoutedByIDNotCorrupted(t *testing.T) {
	requireKernel(t)
	var (
		wg  sync.WaitGroup
		out [2]string
		oks [2]bool
	)
	for i, code := range []string{"left = 100", "right = 200"} {
		wg.Add(1)
		go func(i int, code string) {
			defer wg.Done()
			out[i], oks[i] = run(map[string]any{"code": code})
		}(i, code)
	}
	wg.Wait()
	for i := range out {
		if !oks[i] {
			t.Fatalf("parallel call %d failed: %s", i, out[i])
		}
	}
	text, ok := mustRun(t, map[string]any{"code": "left + right"})
	if !ok {
		t.Fatalf("isError: %s", text)
	}
	matches(t, `Out\[.*\]: 300`, text)
}

func TestSiblingCallNotCollateralDamageWhenAnotherCellTimesOut(t *testing.T) {
	requireKernel(t)
	mustRun(t, map[string]any{"code": "keep = 7"})
	var (
		wg     sync.WaitGroup
		hung   string
		hungOK bool
		sib    string
		sibOK  bool
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		hung, hungOK = run(map[string]any{"code": "import time; time.sleep(30)", "timeoutMs": 1500})
	}()
	go func() {
		defer wg.Done()
		sib, sibOK = run(map[string]any{"code": "1 + 1"})
	}()
	wg.Wait()
	if hungOK {
		t.Fatalf("hung cell succeeded: %s", hung)
	}
	if !regexp.MustCompile(`timed out`).MatchString(hung) {
		t.Fatalf("timeout voice: %s", hung)
	}
	if !sibOK {
		t.Fatalf("sibling must not inherit the timeout's restart: %s", sib)
	}
	matches(t, `Out\[.*\]: 2`, sib)
}

func TestQueuedCallTimeoutDoesNotCountTimeSpentWaiting(t *testing.T) {
	requireKernel(t)
	var (
		wg      sync.WaitGroup
		first   string
		firstOK bool
		queued  string
		qOK     bool
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		first, firstOK = run(map[string]any{"code": "import time; time.sleep(2.5)", "timeoutMs": 8000})
	}()
	go func() {
		defer wg.Done()
		queued, qOK = run(map[string]any{"code": "9 * 9", "timeoutMs": 1500})
	}()
	wg.Wait()
	if !firstOK {
		t.Fatalf("first cell should have completed within its timeout: %s", first)
	}
	if !qOK {
		t.Fatalf("queue time must not be charged to the cell's timeout: %s", queued)
	}
	matches(t, `Out\[.*\]: 81`, queued)
}

const unwritableHostSrc = `
import json, sys, time, os
line = sys.stdin.readline()
if line:
    req = json.loads(line)
    print(json.dumps({"ok": True, "out": "one", "err": "", "result": None, "error": None, "id": req.get("id")}), flush=True)
# close the host's end of stdin (os.close: PEP 442's lazy wrapper close
# would leave fd 0 open) so the client's next write fails with EPIPE
# while the host stays alive (no death-path race)
os.close(0)
time.sleep(60)
`

func TestUnwritableKernelFailsFastInsteadOfWaitingOutTheTimeout(t *testing.T) {
	py := pythonAvailable(t)
	host := filepath.Join(t.TempDir(), "fake-host.py")
	writeHost(t, host, unwritableHostSrc)
	kt := NewWith(py, host)
	defer kt.Close()

	text, err := call(t, kt, map[string]any{"code": "1", "timeoutMs": 5000})
	if err != nil {
		t.Fatalf("first call failed: %s (%v)", text, err)
	}
	if !strings.Contains(text, "one") {
		t.Fatalf("first call: %s", text)
	}

	start := time.Now()
	text, err = call(t, kt, map[string]any{"code": "2", "timeoutMs": 30000})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("second call succeeded; the stdin is closed: %s", text)
	}
	if !strings.Contains(text, "kernel is not writable") && !strings.Contains(err.Error(), "kernel is not writable") {
		t.Fatalf("voice: %s (%v)", text, err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("should fail fast, took %v", elapsed)
	}
}

func TestMissingHostSurfacesStderrDiagnosticsAndSelfHeals(t *testing.T) {
	py := requireKernel(t)
	host := filepath.Join(t.TempDir(), "kernel_host.py") // absent at first
	kt := NewWith(py, host)
	defer kt.Close()

	text, err := call(t, kt, map[string]any{"code": "1 + 1"})
	if err == nil {
		t.Fatalf("missing host succeeded: %s", text)
	}
	matches(t, `kernel exited \(code \d+\)`, text)
	matches(t, `\[stderr\]`, text)

	// restore the host: the same tool self-heals on the next call
	writeHost(t, host, kernelHostSrc)
	healed, err := call(t, kt, map[string]any{"code": "1 + 1"})
	if err != nil {
		t.Fatalf("self-heal failed: %s (%v)", healed, err)
	}
	matches(t, `Out\[.*\]: 2`, healed)
}

func TestUnexpectedMidCallDeathIsAnnouncedToTheDyingCallAndTheNextCall(t *testing.T) {
	requireKernel(t)
	text, ok := run(map[string]any{"code": "import os; os._exit(9)"})
	if ok {
		t.Fatalf("mid-call death reported as success: %s", text)
	}
	matches(t, `kernel exited \(code 9\)`, text)

	again, ok := mustRun(t, map[string]any{"code": "1 + 1"})
	if !ok {
		t.Fatalf("next call failed: %s", again)
	}
	matches(t, `note: fresh kernel; previous kernel exited \(code 9\); all previous variables are gone`, again)
	noteIdx, outIdx := strings.Index(again, "note:"), strings.Index(again, "Out[")
	if noteIdx < 0 || outIdx < 0 || noteIdx > outIdx {
		t.Fatalf("note must render first:\n%s", again)
	}
	matches(t, `Out\[.*\]: 2`, again)
}

func TestQuiescentDeathBetweenCallsIsAnnouncedOnTheNextCallOnce(t *testing.T) {
	py := requireKernel(t)
	kt := NewWith(py, DefaultHost())
	defer kt.Close()

	seed, err := call(t, kt, map[string]any{"code": "seed = 1", "timeoutMs": 5000})
	if err != nil {
		t.Fatalf("seed failed: %s (%v)", seed, err)
	}
	p := kt.k.proc
	if p == nil {
		t.Fatal("kernel not started")
	}
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.dead:
	case <-time.After(5 * time.Second):
		t.Fatal("kernel did not die")
	}

	text, err := call(t, kt, map[string]any{"code": "1 + 1", "timeoutMs": 5000})
	if err != nil {
		t.Fatalf("next call failed: %s (%v)", text, err)
	}
	matches(t, `note: fresh kernel; previous kernel exited \(signal SIGKILL\); all previous variables are gone`, text)

	again, err := call(t, kt, map[string]any{"code": "1 + 1", "timeoutMs": 5000})
	if err != nil {
		t.Fatalf("second next call failed: %s (%v)", again, err)
	}
	if strings.Contains(again, "note: fresh kernel") {
		t.Fatalf("note is one-shot:\n%s", again)
	}
}

func TestDeliberateTimeoutRestartDoesNotProduceADeathNote(t *testing.T) {
	requireKernel(t)
	text, ok := run(map[string]any{"code": "import time; time.sleep(30)", "timeoutMs": 1500})
	if ok {
		t.Fatalf("hung cell succeeded: %s", text)
	}
	matches(t, `timed out`, text)

	okText, ok := mustRun(t, map[string]any{"code": "1 + 1"})
	if !ok {
		t.Fatalf("next call failed: %s", okText)
	}
	if strings.Contains(okText, "note: fresh kernel") {
		t.Fatalf("deliberate restart must not produce a death note:\n%s", okText)
	}
	matches(t, `Out\[.*\]: 2`, okText)
}

const fakePongHostSrc = `
import json, sys
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    req = json.loads(line)
    resp = {'ok': True, 'out': 'fake-pong', 'err': '', 'result': None, 'error': None}
    resp['id'] = req.get('id')
    print(json.dumps(resp), flush=True)
`

func TestConstructorOptionsSelectInterpreterAndHostInjectionSeam(t *testing.T) {
	py := pythonAvailable(t)
	host := filepath.Join(t.TempDir(), "fake-host.py")
	writeHost(t, host, fakePongHostSrc)
	kt := NewWith(py, host)
	defer kt.Close()

	text, err := call(t, kt, map[string]any{"code": "whatever", "timeoutMs": 5000})
	if err != nil {
		t.Fatalf("seam call failed: %s (%v)", text, err)
	}
	if text != "fake-pong" {
		t.Fatalf("out = %q, want fake-pong", text)
	}
}

// fakeHostSrc is pane's env-steered protocol host, ported: deterministic
// dirty-death scenarios with a per-spawn counter, no sleeps.
const fakeHostSrc = `
import json, os, sys
state = os.environ.get('RIG_FAKE_STATE_DIR')
mode = os.environ.get('RIG_FAKE_MODE', 'normal')
count = 0
if state:
    p = os.path.join(state, 'count')
    try: count = int(open(p).read() or 0)
    except Exception: count = 0
    open(p, 'w').write(str(count + 1))
if mode == 'partial' and count == 0:
    sys.stdout.write('{"partial')
    sys.stdout.flush()
    sys.exit(0)
if mode == 'stderr' and count == 0:
    sys.stderr.write('old-error\n')
    sys.stderr.flush()
    sys.exit(4)
if mode == 'stderr' and count >= 1:
    sys.exit(5)
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    req = json.loads(line)
    resp = {'ok': True, 'out': 'fake-ok', 'err': '', 'result': None, 'error': None}
    resp['id'] = req.get('id')
    print(json.dumps(resp), flush=True)
`

func fakeKernel(t *testing.T, mode string) *Tool {
	t.Helper()
	py := pythonAvailable(t)
	dir := t.TempDir()
	host := filepath.Join(dir, "fake-host.py")
	writeHost(t, host, fakeHostSrc)
	t.Setenv("RIG_FAKE_STATE_DIR", dir)
	t.Setenv("RIG_FAKE_MODE", mode)
	kt := NewWith(py, host)
	t.Cleanup(kt.Close)
	return kt
}

func TestDirtyDeathLeavesNoStaleBufferThatSwallowsTheNextKernelReply(t *testing.T) {
	kt := fakeKernel(t, "partial")

	first, err := call(t, kt, map[string]any{"code": "a", "timeoutMs": 5000})
	if err == nil {
		t.Fatalf("first call succeeded: %s", first)
	}
	matches(t, `kernel exited \(code 0\)`, first)

	// pane asserts second.ok and second.out == "fake-ok": the reply landed
	// (the one-shot note may prefix the render, it is not a failure)
	second, err := call(t, kt, map[string]any{"code": "b", "timeoutMs": 3000})
	if err != nil {
		t.Fatalf("stale buffer swallowed the reply: %s (%v)", second, err)
	}
	if !strings.Contains(second, "fake-ok") {
		t.Fatalf("out missing from: %q", second)
	}
}

func TestDeadKernelStderrDoesNotLeakIntoTheNextKernelDeathMessage(t *testing.T) {
	kt := fakeKernel(t, "stderr")

	first, err := call(t, kt, map[string]any{"code": "a", "timeoutMs": 5000})
	if err == nil {
		t.Fatalf("first call succeeded: %s", first)
	}
	matches(t, `kernel exited \(code 4\)`, err.Error())
	matches(t, `old-error`, err.Error())

	// pane asserts on second.error (the error voice), not the render: the
	// one-shot note legitimately carries the first death's stderr
	_, err = call(t, kt, map[string]any{"code": "b", "timeoutMs": 5000})
	if err == nil {
		t.Fatal("second call succeeded")
	}
	matches(t, `kernel exited \(code 5\)`, err.Error())
	if strings.Contains(err.Error(), "old-error") {
		t.Fatalf("dead kernel's stderr leaked: %v", err)
	}
}

func TestTimeoutMessageDescribesTheLazyRestartAccurately(t *testing.T) {
	requireKernel(t)
	text, ok := run(map[string]any{"code": "import time; time.sleep(30)", "timeoutMs": 1500})
	if ok {
		t.Fatalf("hung cell succeeded: %s", text)
	}
	matches(t, `will be restarted on the next call; all variables are gone`, text)
}

// The rig-side named case: the seam's contract. An explicit interpreter
// and host (what NewWith is, what RIG_PYTHON at the root selects) must
// not drag in the default venv's lazy bootstrap; the default path keeps
// it.
func TestNewWithSkipsTheDefaultVenvBootstrapTheDefaultPathKeepsIt(t *testing.T) {
	seam := NewWith("/opt/operator/python3", "/tmp/whatever/kernel_host.py")
	if seam.k.python != "/opt/operator/python3" {
		t.Fatalf("interpreter = %q, want the explicit one", seam.k.python)
	}
	if !seam.k.noBootstrap {
		t.Fatal("the seam must skip the default-venv bootstrap")
	}

	def := New()
	defer def.Close()
	if def.k.python != defaultInterpreter() {
		t.Fatalf("default interpreter = %q, want pane's venv", def.k.python)
	}
	if def.k.noBootstrap {
		t.Fatal("the default path keeps the lazy bootstrap")
	}

	if seam.Host() != "/tmp/whatever/kernel_host.py" {
		t.Fatalf("Host() = %q, want the explicit host", seam.Host())
	}
}
