package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/frontend/cli"
	"github.com/mrsirg97-rgb/rig/loop"
	"github.com/mrsirg97-rgb/rig/plugins"
	"github.com/mrsirg97-rgb/rig/store"
	remstore "github.com/mrsirg97-rgb/rig/store/rem"
	"github.com/mrsirg97-rgb/rig/store/state"
	"github.com/mrsirg97-rgb/rig/tool/bash"
	"github.com/mrsirg97-rgb/rig/tool/diff"
	"github.com/mrsirg97-rgb/rig/tool/file"
	"github.com/mrsirg97-rgb/rig/tool/fs"
	pythontool "github.com/mrsirg97-rgb/rig/tool/python"
)

func okReply(s string) pythontool.Reply {
	return pythontool.Reply{Ok: true, Out: &s}
}

type kernelStub struct {
	mu      sync.Mutex
	cells   []string
	replies []pythontool.Reply
}

func (k *kernelStub) Run(ctx context.Context, code string, timeoutMs int) (pythontool.Reply, error) {
	k.mu.Lock()
	k.cells = append(k.cells, code)
	i := len(k.cells) - 1
	k.mu.Unlock()
	if i < len(k.replies) {
		return k.replies[i], nil
	}
	out := ""
	return pythontool.Reply{Ok: true, Out: &out}, nil
}

func (k *kernelStub) cellCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.cells)
}

func (k *kernelStub) Name() string { return "python" }

func (k *kernelStub) Description() string { return "the kernel stub (the python tool's surface)" }

func (k *kernelStub) Schema() json.RawMessage { return json.RawMessage(`{"type": "object"}`) }

func (k *kernelStub) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &a); err != nil {
		return "", err
	}
	reply, err := k.Run(ctx, a.Code, 0)
	if err != nil {
		return "", err
	}
	if reply.Ok && reply.Out != nil {
		return strings.TrimRight(*reply.Out, " \t\r\n"), nil
	}
	return "", errors.New("kernel stub: the canned reply is not OK")
}

type reloadHarness struct {
	t        *testing.T
	r        *root
	s        *pluginSrv
	allow    []string
	in       chan string
	out      *lockedWriter
	sent     int
	stopDone chan error
}

func newReloadHarness(t *testing.T, home string, kernel plugins.Kernel, replies []string) *reloadHarness {
	t.Helper()
	s := &pluginSrv{replies: replies}
	srv := newPluginSrv(t, s)
	return newReloadHarnessWith(t, home, kernel, srv, s)
}

func newReloadHarnessWith(t *testing.T, home string, kernel plugins.Kernel, srv *httptest.Server, s *pluginSrv) *reloadHarness {
	t.Helper()

	dir := t.TempDir()
	db, _, _, err := store.Open(filepath.Join(dir, "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB.Close() })
	remDB, _, _, err := store.Open(filepath.Join(dir, "rem.sqlite"), remstore.Statements(), remstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { remDB.DB.Close() })

	allow := append(append([]string{}, nativeToolNames...), "forged", "alpha")
	r := &root{
		baseURL:     srv.URL + "/v1",
		system:      "S",
		allow:       allow,
		retries:     3,
		sdb:         db,
		remDB:       remDB,
		cwd:         dir,
		activeID:    "local",
		row:         defaultRow(),
		runtime:     defaultsTableValue(),
		pluginsDir:  filepath.Join(home, "plugins"),
		pluginsHome: home,
		py:          kernel,
		tools: map[string]core.Tool{
			"bash": bash.New(), "read": file.Read(), "write": file.Write(), "edit": file.Edit(),
			"ls": fs.LS(), "find": fs.Find(), "grep": fs.Grep(),
			"todo": fakeTodo{}, "rem": fakeRem{}, "scheduler": fakeSched{}, "delegate": fakeDelegate{}, "python": kernel.(core.Tool),
			"web_search": fakeWebSearch{}, "web_fetch": fakeWebFetch{},
			"diff": diff.New(store.DB{}),
		},
	}
	natives := make(map[string]bool, len(nativeToolNames))
	for _, n := range nativeToolNames {
		natives[n] = true
	}
	r.natives = natives
	r.tools["plugins"] = plugins.NewEcosystem(home, natives, kernel, r.swapPlugins, func() (string, error) { return command.RenderPlugins(r.pluginInfos, ""), nil })

	r.session = core.NewSession()
	in := make(chan string, 8)
	out := &lockedWriter{b: &bytes.Buffer{}}
	fe := cli.New(chanReader{ch: in}, out, cli.WithCommands(command.All(), commandEnv(r)))
	r.fe = fe
	r.rec = state.NewRecorder(fe, db, r.cwd, "local", Version, r.session.ID, r.session)
	wire(r)
	return &reloadHarness{t: t, r: r, s: s, allow: allow, in: in, out: out}
}

func (h *reloadHarness) start() {
	h.t.Helper()
	done := make(chan error, 1)
	go func() { done <- loop.Run(context.Background(), h.r.k) }()
	h.stopDone = done
}

func (h *reloadHarness) line(text string) {
	h.t.Helper()
	h.sent++
	h.in <- text + "\n"
	h.waitPong(h.sent)
}

func (h *reloadHarness) stop() {
	h.t.Helper()
	close(h.in)
	if err := <-h.stopDone; err != nil {
		h.t.Fatalf("loop: %v", err)
	}
	if e := h.r.rec.Close("ok"); e != nil {
		h.t.Fatalf("session closure: %v", e)
	}
}

func (h *reloadHarness) waitPong(n int) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(h.out.String(), "pong") >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	h.t.Fatalf("output has %d×pong, want %d:\n%s", strings.Count(h.out.String(), "pong"), n, h.out.String())
}

func toolNames(body []byte) []string {
	var req struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		panic("unmarshal the captured body: " + err.Error())
	}
	out := make([]string, 0, len(req.Tools))
	for _, tl := range req.Tools {
		out = append(out, tl.Function.Name)
	}
	return out
}

func sameOrder(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func hasToolName(body []byte, name string) bool {
	for _, n := range toolNames(body) {
		if n == name {
			return true
		}
	}
	return false
}

func hasTool(t *testing.T, body []byte, name, description string) bool {
	t.Helper()
	for _, tl := range wireTools(t, body) {
		if tl.Name == name && tl.Description == description {
			return true
		}
	}
	return false
}

func pluginNamesIn(body []byte) []string {
	var req struct {
		Tools []struct {
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		panic("unmarshal the captured body: " + err.Error())
	}
	for _, tl := range req.Tools {
		if tl.Function.Name != "plugin" {
			continue
		}
		var schema struct {
			Properties struct {
				Name struct {
					Enum []string `json:"enum"`
				} `json:"name"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tl.Function.Parameters, &schema); err != nil {
			panic("the plugin door's schema is not JSON: " + err.Error())
		}
		return schema.Properties.Name.Enum
	}
	return nil
}

func hasPluginName(body []byte, name string) bool {
	for _, n := range pluginNamesIn(body) {
		if n == name {
			return true
		}
	}
	return false
}

func bodiesAll(s *pluginSrv) [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.bodies...)
}

func TestReloadTakesEffectNextTurnZeroLoopLines(t *testing.T) {
	home := t.TempDir()
	writePlugin(t, home, "forged.py", `DESCRIPTION = "the fixture forged plugin"
SCHEMA = {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}

def run(args: dict) -> str:
    return "forged: " + args["text"]
`)
	forgedFile := filepath.Join(home, "plugins", "forged.py")
	report := `[{"name":"forged","file":"` + forgedFile + `","ok":true,"description":"the fixture forged plugin","schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}}]`

	kernel := &kernelStub{replies: []pythontool.Reply{okReply(report), okReply("forged: hi\n")}}

	h := newReloadHarness(t, home, kernel, []string{
		pongReply,
		toolCallReply("c1", "plugins", `{"action":"reload"}`),
		pongReply,
		toolCallReply("c2", "forged", `{"text":"hi"}`),
		pongReply,
	})
	h.start()
	h.line("one")
	h.line("two")
	h.line("three")
	h.stop()

	bodies := bodiesAll(h.s)
	if len(bodies) != 5 {
		t.Fatalf("requests = %d, want 5 (the scripted schedule)", len(bodies))
	}

	if got := toolNames(bodies[0]); !sameOrder(got, nativeToolNames) {
		t.Fatalf("turn 1's wire = %v, want the native set in order", got)
	}

	if hasToolName(bodies[1], "forged") {
		t.Fatalf("the reload's own request carries forged (the stamp predates the swap)")
	}

	want := "plugins: reload: 1 loaded, 0 skipped\nloaded:\n  forged: the fixture forged plugin (" + forgedFile + ")\n"
	if got := toolMessageOf(t, bodies[2]); got != want {
		t.Fatalf("the reload's reply = %q, want the reload's (the listing with its action named):\n%q", got, want)
	}

	if !hasPluginName(bodies[3], "forged") {
		t.Fatalf("turn 3's wire must carry forged in the plugin door's enum, got %v", pluginNamesIn(bodies[3]))
	}

	if got := toolMessageOf(t, bodies[4]); got != "forged: hi" {
		t.Fatalf("the new tool's result = %q, want the round trip (\"forged: hi\")", got)
	}

	if len(h.r.k.Tools) != len(nativeToolNames) {
		t.Fatalf("the loop's snapshot changed by the swap: %d entries", len(h.r.k.Tools))
	}
	if got := len(h.r.live.List()); got != len(nativeToolNames)+1 {
		t.Fatalf("the table after the swap = %d tools, want the natives plus the swapped-in one", got)
	}

	if len(h.r.k.Middleware) != 6 {
		t.Fatalf("middleware = %d links, want the router, the provenance rule, the allow-list, the bound, the round cap, and the result bound", len(h.r.k.Middleware))
	}

	for _, tl := range h.r.k.Tools {
		if tl.Name() == "python" && (tl != h.r.tools["python"] || any(tl) != any(h.r.py)) {
			t.Fatal("the python tool must be the shared kernel (the reload's import, the tool's call)")
		}
	}
}

func TestPluginsReloadToolRebuildsTheList(t *testing.T) {
	home := t.TempDir()
	writePlugin(t, home, "alpha.py", `DESCRIPTION = "the fixture alpha plugin"
SCHEMA = {"type": "object"}

def run(args):
    return "alpha"
`)
	if err := os.WriteFile(filepath.Join(home, "plugins", "broken.py"), []byte("y = x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alphaFile := filepath.Join(home, "plugins", "alpha.py")
	brokenFile := filepath.Join(home, "plugins", "broken.py")
	kernel := &kernelStub{replies: []pythontool.Reply{
		okReply(`[{"name":"alpha","file":"` + alphaFile + `","ok":true,"description":"the fixture alpha plugin","schema":{"type":"object"}},{"name":"broken","file":"` + brokenFile + `","ok":false,"error":"NameError: name 'x' is not defined"}]`),
		okReply(`[]`),
	}}

	h := newReloadHarness(t, home, kernel, []string{
		toolCallReply("c1", "plugins", `{"action":"reload"}`),
		pongReply,
		toolCallReply("c2", "plugins", `{"action":"reload"}`),
		pongReply,
		pongReply,
	})
	h.start()
	h.line("one")

	if err := os.Remove(alphaFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(brokenFile); err != nil {
		t.Fatal(err)
	}
	h.line("two")
	h.line("three")
	h.stop()

	bodies := bodiesAll(h.s)
	if len(bodies) != 5 {
		t.Fatalf("requests = %d, want 5 (two reload turns, each call + answer, plus the wire's turn)", len(bodies))
	}

	if got := toolMessageOf(t, bodies[1]); !strings.Contains(got, "plugins: reload: 1 loaded, 1 skipped") || !strings.Contains(got, "broken.py: NameError: name 'x' is not defined") {
		t.Fatalf("the up reload's reply = %q, want the loud skips in it", got)
	}

	if !hasPluginName(bodies[2], "alpha") {
		t.Fatal("the next turn's wire must carry alpha in the plugin door's enum")
	}
	if hasPluginName(bodies[2], "broken") {
		t.Fatal("the skipped plugin must not be in the plugin door's enum")
	}

	if got := toolMessageOf(t, bodies[3]); got != "plugins: reload: 0 loaded, 0 skipped" {
		t.Fatalf("the down reload's reply = %q, want the empty list (removal free)", got)
	}
	if got := toolNames(bodies[4]); !sameOrder(got, nativeToolNames) {
		t.Fatalf("the final wire = %v, want the native set (the list rebuilt down)", got)
	}
}

func TestPluginsReloadCollisionRefusesAndKeepsTheList(t *testing.T) {
	home := t.TempDir()
	writePlugin(t, home, "bash.py", `DESCRIPTION = "shadowing bash"
SCHEMA = {"type": "object"}

def run(args):
    return "plugin bash"
`)
	bashFile := filepath.Join(home, "plugins", "bash.py")
	report := `[{"name":"bash","file":"` + bashFile + `","ok":true,"description":"shadowing bash","schema":{"type":"object"}}]`
	kernel := &kernelStub{replies: []pythontool.Reply{okReply(report)}}
	h := newReloadHarness(t, home, kernel, []string{
		toolCallReply("c1", "plugins", `{"action":"reload"}`),
		pongReply,
		pongReply,
	})
	h.start()
	h.line("one")
	h.line("two")
	h.stop()

	bodies := bodiesAll(h.s)
	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want 3", len(bodies))
	}
	if got := toolMessageOf(t, bodies[1]); got != `plugins: name collision: "bash" (bash.py) is already a native tool` {
		t.Fatalf("the tool error = %q, want the collision's voice", got)
	}

	if got := toolNames(bodies[2]); !sameOrder(got, nativeToolNames) {
		t.Fatalf("the wire = %v, want the pre-reload list, whole", got)
	}
	if got := len(h.r.live.List()); got != len(nativeToolNames) {
		t.Fatalf("the table = %d tools, want the pre-reload list (the swap refused)", got)
	}
}

func TestApproveReloadsPost8(t *testing.T) {
	py := pluginKernelPy(t)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	home := cfgDir(t, scratch)
	zone := filepath.Join(home, "plugins", "pending")
	if err := os.MkdirAll(zone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone, "forge.py"), []byte(`DESCRIPTION = "the fixture forge plugin"
SCHEMA = {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}

def run(args: dict) -> str:
    return "forged: " + args["text"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-base-url", "http://127.0.0.1:1/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = pluginEnv(scratch, py)
	cmd.Stdin = strings.NewReader("/plugins approve forge\n/plugins\n")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("the run must succeed: %v\nstdout: %s\nstderr: %s", err, out, ee.Stderr)
		}
		t.Fatalf("the run must succeed: %v\nstdout: %s", err, out)
	}

	for _, want := range []string{
		"plugins: approved forge",
		"plugins: reload: 1 loaded, 0 skipped",
		"loaded:",
		"forge: the fixture forge plugin (" + filepath.Join(home, "plugins", "forge.py") + ")",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("the approve's reply must carry %q:\n%s", want, out)
		}
	}

	tail := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if tail[len(tail)-1] != "  forge: the fixture forge plugin ("+filepath.Join(home, "plugins", "forge.py")+")" {
		t.Fatalf("the post-approve listing's row = %q, want the loaded plugin at its top-level home", tail[len(tail)-1])
	}
	if tail[len(tail)-2] != "loaded:" || tail[len(tail)-3] != "plugins: 1 loaded, 0 skipped" {
		t.Fatalf("the post-approve listing = %q, want the loaded listing (the listing follows the swap)", tail[len(tail)-3:])
	}

	if _, err := os.Stat(filepath.Join(home, "plugins", "forge.py")); err != nil {
		t.Fatalf("the approved plugin must be at the top level: %v", err)
	}
	if _, err := os.Stat(filepath.Join(zone, "forge.py")); !os.IsNotExist(err) {
		t.Fatalf("the pending zone must be empty after the approve: %v", err)
	}
}

func TestReloadE2ERegistersAForgedPluginNextTurn(t *testing.T) {
	py := pluginKernelPy(t)
	home := t.TempDir()
	forgedFile := filepath.Join(home, "plugins", "forged.py")
	forge := `DESCRIPTION = "the fixture forged plugin (the real kernel)"
SCHEMA = {"type": "object", "properties": {"x": {"type": "integer"}}, "required": ["x"]}

import numpy as _np

def run(args: dict) -> str:
    return str(int(_np.int64(args["x"])) * 2)
`
	kernel := pythontool.NewWith(py, pythontool.DefaultHost())
	t.Cleanup(kernel.Close)

	s := &pluginSrv{
		replies: []string{
			toolCallReply("c1", "plugins", `{"action":"reload"}`),
			pongReply,
			toolCallReply("c2", "forged", `{"x": 21}`),
			pongReply,
			toolCallReply("c3", "python", `{"code": "import forged; forged.run({'x': 5})"}`),
			pongReply,
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == "GET":
			if r.URL.Path == "/v1/models" {
				w.Write([]byte(`{"data":[]}`))
			} else {
				w.Write([]byte(`{"running":[]}`))
			}
			return
		default:
			s.mu.Lock()
			i := len(s.bodies)
			s.bodies = append(s.bodies, body)
			reply := s.replies[len(s.replies)-1]
			if i < len(s.replies) {
				reply = s.replies[i]
			}
			first := i == 0
			s.mu.Unlock()
			if first {
				if err := os.MkdirAll(filepath.Dir(forgedFile), 0o755); err != nil {
					t.Errorf("the scripted clock's directory: %v", err)
					return
				}
				if err := os.WriteFile(forgedFile, []byte(forge), 0o644); err != nil {
					t.Errorf("the scripted clock's write: %v", err)
					return
				}
			}
			w.Write([]byte(reply))
		}
	}))
	t.Cleanup(srv.Close)

	h := newReloadHarnessWith(t, home, kernel, srv, s)
	h.start()
	h.line("one")
	h.line("two")
	h.line("three")
	h.stop()

	bodies := bodiesAll(s)
	if len(bodies) != 6 {
		t.Fatalf("requests = %d, want 6 (three turns, each call + answer)", len(bodies))
	}

	if hasToolName(bodies[0], "forged") {
		t.Fatal("the first request carries forged (the file lands on this request, after the stamp)")
	}

	if got := toolMessageOf(t, bodies[1]); got != "plugins: reload: 1 loaded, 0 skipped\nloaded:\n  forged: the fixture forged plugin (the real kernel) ("+forgedFile+")\n" {
		t.Fatalf("the reload's reply = %q, want the loaded line (the real kernel's discovery)", got)
	}

	if !hasPluginName(bodies[2], "forged") {
		t.Fatal("the next turn's wire must carry forged in the plugin door's enum")
	}

	if got := toolMessageOf(t, bodies[3]); got != "42" {
		t.Fatalf("the forged call's result = %q, want the round trip (\"42\", the real kernel's)", got)
	}

	if got := toolMessageOf(t, bodies[5]); got != "Out[3]: '10'" {
		t.Fatalf("the python tool's call of the forged module = %q, want the namespace's proof (the kernel's result echo of \"10\")", got)
	}
}

func TestDoorSelfHealsAnOutOfBandInstall(t *testing.T) {
	home := t.TempDir()
	kernel := &kernelStub{replies: []pythontool.Reply{
		okReply(`[{"name":"dropped","file":"` + filepath.Join(home, "plugins", "dropped.py") + `","ok":true,"description":"the fixture dropped plugin","schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}}]`),
		okReply("dropped: hi\n"),
	}}
	h := newReloadHarness(t, home, kernel, []string{
		toolCallReply("c1", "plugin", `{"action":"run","name":"dropped","args":{"text":"hi"}}`),
		pongReply,
		pongReply,
	})
	h.start()

	writePlugin(t, home, "dropped.py", `DESCRIPTION = "the fixture dropped plugin"
SCHEMA = {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}

def run(args: dict) -> str:
    return "dropped: " + args["text"]
`)

	h.line("one")
	h.line("two")
	h.stop()

	bodies := bodiesAll(h.s)
	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want 3 (the scripted schedule)", len(bodies))
	}

	if hasPluginName(bodies[0], "dropped") {
		t.Fatalf("the call's own wire carries dropped (the stamp predates the swap)")
	}

	if got := toolMessageOf(t, bodies[1]); got != "dropped: hi" {
		t.Fatalf("the self-healed result = %q, want \"dropped: hi\" verbatim", got)
	}

	if !hasPluginName(bodies[2], "dropped") {
		t.Fatalf("the following wire must carry dropped in the door's enum, got %v", pluginNamesIn(bodies[2]))
	}

	if got := kernel.cellCount(); got != 2 {
		t.Fatalf("kernel cells = %d, want the discovery plus the call", got)
	}

	if got := len(h.r.live.List()); got != len(nativeToolNames)+1 {
		t.Fatalf("the table after the self-heal = %d tools, want the natives plus the swapped-in one", got)
	}
}
