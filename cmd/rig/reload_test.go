// The reload's named cases (SPEC_PLUGINS 8, testing): the seam's proof
// (the feature's gate) over the real loop with the fake kernel at the
// DI seam (no python), and the doors (plugins_reload, the list, the
// wire) the same way. The real-kernel e2e is in the plugin suite's
// gate (TestReloadE2ERegistersAForgedPluginNextTurn).
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

// okReply is a canned kernel reply (the printed value), the plugins
// suite's helper's shape (the two packages' fakes agree on the seam).
func okReply(s string) pythontool.Reply {
	return pythontool.Reply{Ok: true, Out: &s}
}

// kernelStub is the plugins.Kernel DI seam (SPEC_PLUGINS, testing):
// the canned replies in order, every cell recorded — no python.
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

// the core.Tool surface: the root's "python" entry is the kernel's Run
// (the real tool's Exec is the kernel's send — the root wires "python":
// py), so the stub's exec is its Run, rendered like the real tool's.
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

// reloadHarness is the reload's bench: the real loop and CLI, the
// scripted provider (the bodies captured), the fake kernel at the
// seam, and the scratch home's plugins/ directory.
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

// newReloadHarnessWith is the harness over a given scripted server (the
// reload's doors and the real-kernel's clock share one bench).
func newReloadHarnessWith(t *testing.T, home string, kernel plugins.Kernel, srv *httptest.Server, s *pluginSrv) *reloadHarness {
	t.Helper()

	dir := t.TempDir()
	db, _, err := store.Open(filepath.Join(dir, "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB.Close() })
	remDB, _, err := store.Open(filepath.Join(dir, "rem.sqlite"), remstore.Statements(), remstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { remDB.DB.Close() })

	allow := append(append([]string{}, nativeToolNames...), "forged", "alpha") // the model's world, as the settings' list would be
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
	r.tools["plugins_reload"] = plugins.NewReload(home, natives, kernel, r.swapPlugins)

	r.session = core.NewSession()
	in := make(chan string, 8)
	out := &lockedWriter{b: &bytes.Buffer{}}
	fe := cli.New(chanReader{ch: in}, out, cli.WithCommands(command.All(), commandEnv(r)))
	r.fe = fe
	r.rec = state.NewRecorder(fe, db, r.cwd, "local", Version, r.session.ID, r.session)
	wire(r)
	return &reloadHarness{t: t, r: r, s: s, allow: allow, in: in, out: out}
}

// start launches the loop (a goroutine, joined by stop).
func (h *reloadHarness) start() {
	h.t.Helper()
	done := make(chan error, 1)
	go func() { done <- loop.Run(context.Background(), h.r.k) }()
	h.stopDone = done
}

// line sends one input line and waits for its turn's pong (a schedule,
// not a sleep on the outcome).
func (h *reloadHarness) line(text string) {
	h.t.Helper()
	h.sent++
	h.in <- text + "\n" // the scanner's delimiter, as the CLI's lines carry
	h.waitPong(h.sent)
}

// stop closes stdin, joins the loop, and closes the session row as the
// root does at process end (a clean REPL exit).
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

// --- wire helpers (the bodies' tools arrays) ---

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

// pluginNamesIn is the plugin door's name enum (SPEC_GROWTH 9): the live
// plugins the wire carries in one `plugin` tool, not as per-plugin schemas.
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

// bodiesAll is the captured bodies, snapshotted (the loop has joined).
func bodiesAll(s *pluginSrv) [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.bodies...)
}

// TestReloadTakesEffectNextTurnZeroLoopLines (SPEC_PLUGINS 8, named —
// the seam's proof, the feature's gate): a turn over, the root's swap
// adds a tool, and the next turn's request carries it (name,
// description, schema) while the finished turn's request does not; the
// new tool executes on that next turn (the router's end) and the
// natives keep executing (the table's own); loop/ and core/ stay
// byte-frozen against the branch's base (the freeze gate, checked at
// the commit).
func TestReloadTakesEffectNextTurnZeroLoopLines(t *testing.T) {
	home := t.TempDir()
	writePlugin(t, home, "forged.py", `DESCRIPTION = "the fixture forged plugin"
SCHEMA = {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}

def run(args: dict) -> str:
    return "forged: " + args["text"]
`)
	forgedFile := filepath.Join(home, "plugins", "forged.py")
	report := `[{"name":"forged","file":"` + forgedFile + `","ok":true,"description":"the fixture forged plugin","schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}}]`
	// the fake kernel's canned replies: the discovery report, then the
	// call cell's printed value.
	kernel := &kernelStub{replies: []pythontool.Reply{okReply(report), okReply("forged: hi\n")}}

	h := newReloadHarness(t, home, kernel, []string{
		pongReply, // turn 1
		toolCallReply("c1", "plugins_reload", `{}`), // turn 2: the model's call
		pongReply, // turn 2 ends (the stamp predates the swap)
		toolCallReply("c2", "forged", `{"text":"hi"}`), // turn 3: the new tool
		pongReply, // turn 3 ends
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

	// turn 1's wire: the native set, in order — no forged (the stamp
	// predates the swap).
	if got := toolNames(bodies[0]); !sameOrder(got, nativeToolNames) {
		t.Fatalf("turn 1's wire = %v, want the native set in order", got)
	}
	// the request that called the reload (stamped before the swap): no
	// forged — the wire the model called against did not carry the tool.
	if hasToolName(bodies[1], "forged") {
		t.Fatalf("the reload's own request carries forged (the stamp predates the swap)")
	}
	// (the request after the reload's result — same turn, a new call — is
	// stamped after the swap and may carry it: the stamp is per call, and
	// the next turn's request must carry it, asserted below.)
	// the reload's reply rides back as the tool result.
	want := "plugins: reload: 1 loaded, 0 skipped\nloaded:\n  forged: the fixture forged plugin (" + forgedFile + ")\n"
	if got := toolMessageOf(t, bodies[2]); got != want {
		t.Fatalf("the reload's reply = %q, want the reload's (the listing with its action named):\n%q", got, want)
	}

	// turn 3's wire: the door's name enum carries forged (SPEC_GROWTH 9:
	// one `plugin` tool, no per-plugin schemas).
	if !hasPluginName(bodies[3], "forged") {
		t.Fatalf("turn 3's wire must carry forged in the plugin door's enum, got %v", pluginNamesIn(bodies[3]))
	}
	// the round trip: the new tool executed (the router's end).
	if got := toolMessageOf(t, bodies[4]); got != "forged: hi" {
		t.Fatalf("the new tool's result = %q, want the round trip (\"forged: hi\")", got)
	}

	// the loop's snapshot is the bootstrap: untouched by the swap; the
	// table is the truth (the next turn's wire came from it).
	if len(h.r.k.Tools) != len(nativeToolNames) {
		t.Fatalf("the loop's snapshot changed by the swap: %d entries", len(h.r.k.Tools))
	}
	if got := len(h.r.live.List()); got != len(nativeToolNames)+1 {
		t.Fatalf("the table after the swap = %d tools, want the natives plus the swapped-in one", got)
	}
	// the chain carries the router (the provenance rule, the allow-list,
	// and the bound, in the root's order).
	if len(h.r.k.Middleware) != 4 {
		t.Fatalf("middleware = %d links, want the router, the provenance rule, the allow-list, and the bound", len(h.r.k.Middleware))
	}
	// the python tool's instance survives (the shared kernel, the
	// per-process state): the loop's "python" is the kernel the reload
	// imports into — the same instance, as the root's "python": py.
	for _, tl := range h.r.k.Tools {
		if tl.Name() == "python" && (tl != h.r.tools["python"] || any(tl) != any(h.r.py)) {
			t.Fatal("the python tool must be the shared kernel (the reload's import, the tool's call)")
		}
	}
}

// TestPluginsReloadToolRebuildsTheList (SPEC_PLUGINS 8, named): the
// model calls plugins_reload — the reply is the reload's (the loud
// skips in it), the next turn's wire carries the new plugin (its
// DESCRIPTION and SCHEMA verbatim); a second reload over a removed
// file rebuilds the list down (removal free), and the wire follows.
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
		okReply(`[]`), // the disk's truth on the second pass: alpha gone
	}}
	// the disk: alpha is removed between the two reloads (the removal
	// free's named case) — the second listing is the empty directory.
	h := newReloadHarness(t, home, kernel, []string{
		toolCallReply("c1", "plugins_reload", `{}`), // turn 1: the up reload
		pongReply, // turn 1 ends
		toolCallReply("c2", "plugins_reload", `{}`), // turn 2: the down reload
		pongReply, // turn 2 ends
		pongReply, // turn 3: the wire's shape
	})
	h.start()
	h.line("one")
	// the removal (removal free): the disk is the truth the second
	// listing reads — both fixture files go away.
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
	// the up reload: the loud skips in the reply.
	if got := toolMessageOf(t, bodies[1]); !strings.Contains(got, "plugins: reload: 1 loaded, 1 skipped") || !strings.Contains(got, "broken.py: NameError: name 'x' is not defined") {
		t.Fatalf("the up reload's reply = %q, want the loud skips in it", got)
	}
	// the next turn's wire: alpha in the plugin door's enum; the skipped
	// one absent.
	if !hasPluginName(bodies[2], "alpha") {
		t.Fatal("the next turn's wire must carry alpha in the plugin door's enum")
	}
	if hasPluginName(bodies[2], "broken") {
		t.Fatal("the skipped plugin must not be in the plugin door's enum")
	}
	// the down reload: the list rebuilds to the disk (empty), and the
	// wire follows.
	if got := toolMessageOf(t, bodies[3]); got != "plugins: reload: 0 loaded, 0 skipped" {
		t.Fatalf("the down reload's reply = %q, want the empty list (removal free)", got)
	}
	if got := toolNames(bodies[4]); !sameOrder(got, nativeToolNames) {
		t.Fatalf("the final wire = %v, want the native set (the list rebuilt down)", got)
	}
}

// TestPluginsReloadCollisionRefusesAndKeepsTheList (SPEC_PLUGINS 8,
// named): a loaded report named like a native — the tool error is the
// collision's voice, and the wire is the pre-reload list, whole.
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
		toolCallReply("c1", "plugins_reload", `{}`), // the colliding reload
		pongReply, // the turn ends
		pongReply, // the wire's shape
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
	// the wire is the pre-reload list, whole (the swap never ran).
	if got := toolNames(bodies[2]); !sameOrder(got, nativeToolNames) {
		t.Fatalf("the wire = %v, want the pre-reload list, whole", got)
	}
	if got := len(h.r.live.List()); got != len(nativeToolNames) {
		t.Fatalf("the table = %d tools, want the pre-reload list (the swap refused)", got)
	}
}

// TestApproveReloadsPost8 (SPEC_PLUGINS 8, named): the pending zone's
// file: /plugins approve moves it, the reply carries the move plus the
// reload's line, and the next /plugins listing shows it loaded (the
// root's state swapped, the command's listing follows). Gated on a
// usable python as the plugin suite's: the approve's tail is the
// reload's, and the reload is the discovery's.
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
	// the approve: the move plus the reload's reply (the tail is the
	// reload's, post-8) — the loaded plugin at its new top-level home.
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
	// the next listing: the root's state swapped, the command's listing
	// follows — the loaded row at its top-level home, the zone empty.
	tail := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if tail[len(tail)-1] != "  forge: the fixture forge plugin ("+filepath.Join(home, "plugins", "forge.py")+")" {
		t.Fatalf("the post-approve listing's row = %q, want the loaded plugin at its top-level home", tail[len(tail)-1])
	}
	if tail[len(tail)-2] != "loaded:" || tail[len(tail)-3] != "plugins: 1 loaded, 0 skipped" {
		t.Fatalf("the post-approve listing = %q, want the loaded listing (the listing follows the swap)", tail[len(tail)-3:])
	}
	// the filesystem: moved.
	if _, err := os.Stat(filepath.Join(home, "plugins", "forge.py")); err != nil {
		t.Fatalf("the approved plugin must be at the top level: %v", err)
	}
	if _, err := os.Stat(filepath.Join(zone, "forge.py")); !os.IsNotExist(err) {
		t.Fatalf("the pending zone must be empty after the approve: %v", err)
	}
}

// TestReloadE2ERegistersAForgedPluginNextTurn (SPEC_PLUGINS 8, named;
// the real kernel, the plugin suite's gate): the provider's first
// request lands a new file in plugins/ (the scripted clock), the model
// calls plugins_reload — the reply carries the loaded line — and the
// next turn's wire carries the plugin, whose call round-trips through
// the shared namespace (the import is the reload's; the python tool's
// call of the same module is the namespace's proof).
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

	// the scripted clock: the file lands when the provider's first
	// request arrives (before the reload's exec, after its wire's stamp).
	s := &pluginSrv{
		replies: []string{
			toolCallReply("c1", "plugins_reload", `{}`),
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

	// the harness, over the scripted server: the real kernel as the
	// shared seam (the python tool and the reload's import, one
	// namespace), the allow-list widened to the forged plugin.
	h := newReloadHarnessWith(t, home, kernel, srv, s)
	h.start()
	h.line("one")   // turn 1: the file lands, the reload's exec imports it
	h.line("two")   // turn 2: the wire's forged, the round trip
	h.line("three") // turn 3: the python tool's call of the same module
	h.stop()

	bodies := bodiesAll(s)
	if len(bodies) != 6 {
		t.Fatalf("requests = %d, want 6 (three turns, each call + answer)", len(bodies))
	}

	// the request the file landed in does not carry the plugin (the
	// stamp predates the clock's write).
	if hasToolName(bodies[0], "forged") {
		t.Fatal("the first request carries forged (the file lands on this request, after the stamp)")
	}
	// the reload's reply: the loaded line, the real kernel's discovery.
	if got := toolMessageOf(t, bodies[1]); got != "plugins: reload: 1 loaded, 0 skipped\nloaded:\n  forged: the fixture forged plugin (the real kernel) ("+forgedFile+")\n" {
		t.Fatalf("the reload's reply = %q, want the loaded line (the real kernel's discovery)", got)
	}
	// the next turn's wire: forged in the plugin door's enum (SPEC_GROWTH
	// 9: one `plugin` tool, the schema behind plugin_schema).
	if !hasPluginName(bodies[2], "forged") {
		t.Fatal("the next turn's wire must carry forged in the plugin door's enum")
	}
	// the round trip: the plugin's run, the real kernel's numpy.
	if got := toolMessageOf(t, bodies[3]); got != "42" {
		t.Fatalf("the forged call's result = %q, want the round trip (\"42\", the real kernel's)", got)
	}
	// the shared namespace: the python tool's import of the module the
	// reload registered (the import is the reload's, the call is this
	// turn's) — the same kernel, the same namespace. The cell is the
	// third (the discovery, the forged call, this one), and the
	// expression's value is the kernel's result echo.
	if got := toolMessageOf(t, bodies[5]); got != "Out[3]: '10'" {
		t.Fatalf("the python tool's call of the forged module = %q, want the namespace's proof (the kernel's result echo of \"10\")", got)
	}
}

// TestDoorSelfHealsAnOutOfBandInstall (SPEC_STREAMLINE 4, the door's
// proof): a plugin file lands in the home after the wire, no
// plugins_reload is called, and the model calls the door by name; the
// door re-discovers once, executes, and the result rides back verbatim;
// the following request carries the name in the door's enum.
func TestDoorSelfHealsAnOutOfBandInstall(t *testing.T) {
	home := t.TempDir()
	kernel := &kernelStub{replies: []pythontool.Reply{
		okReply(`[{"name":"dropped","file":"` + filepath.Join(home, "plugins", "dropped.py") + `","ok":true,"description":"the fixture dropped plugin","schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}}]`),
		okReply("dropped: hi\n"),
	}}
	h := newReloadHarness(t, home, kernel, []string{
		toolCallReply("c1", "plugin", `{"name":"dropped","args":{"text":"hi"}}`), // turn 1: the door's call
		pongReply, // turn 1 ends
		pongReply, // turn 2: the wire's shape
	})
	h.start()

	// the out-of-band install: the operator's file, no reload called
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
	// the call's wire: the stamp predates the swap, the enum does not
	// carry dropped.
	if hasPluginName(bodies[0], "dropped") {
		t.Fatalf("the call's own wire carries dropped (the stamp predates the swap)")
	}
	// the self-healed run: the result rides back verbatim.
	if got := toolMessageOf(t, bodies[1]); got != "dropped: hi" {
		t.Fatalf("the self-healed result = %q, want \"dropped: hi\" verbatim", got)
	}
	// the following request: the door's enum carries dropped (the swap
	// has landed).
	if !hasPluginName(bodies[2], "dropped") {
		t.Fatalf("the following wire must carry dropped in the door's enum, got %v", pluginNamesIn(bodies[2]))
	}
	// the redo ran exactly once (the discovery), and the call once.
	if got := kernel.cellCount(); got != 2 {
		t.Fatalf("kernel cells = %d, want the discovery plus the call", got)
	}
	// the swap left the plugin as a real tool in the table (the router's
	// end, the python import's end).
	if got := len(h.r.live.List()); got != len(nativeToolNames)+1 {
		t.Fatalf("the table after the self-heal = %d tools, want the natives plus the swapped-in one", got)
	}
}
