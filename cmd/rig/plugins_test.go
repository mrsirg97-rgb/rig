package main

// The plugins' named cases (SPEC_PLUGINS, testing): the home and
// migration (SPEC_CONFIG 11) as in-package unit cases, and the
// discovery, collision, call, and kernel-alive cases as e2e over the
// built binary with a scratch home and a scripted provider. The kernel
// cases gate on a usable python (the tool/python suite's rule): a bare
// box skips cleanly.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/config"
)

// --- gates and helpers ---

// pluginKernelPy names an interpreter with the kernel's dependencies
// (IPython, numpy, pandas): the shared venv first, then python3 on
// PATH — or the case skips cleanly on a bare box (the same gate as
// tool/python's requireKernel).
func pluginKernelPy(t *testing.T) string {
	t.Helper()
	if h, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(h, ".pi", "agent", "kernel-venv", "bin", "python")
		if fi, err := os.Stat(p); err == nil && fi.Mode()&0o111 != 0 {
			if _, err := exec.Command(p, "-c", "import IPython, numpy, pandas").CombinedOutput(); err == nil {
				return p
			}
		}
	}
	p, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no shared kernel venv and no python3 on PATH; the plugin e2e needs one")
	}
	if _, err := exec.Command(p, "-c", "import IPython, numpy, pandas").CombinedOutput(); err != nil {
		t.Skipf("IPython, numpy or pandas not importable by %s (bare box)", p)
	}
	return p
}

// writePlugin drops a fixture plugin into the home's plugins/ directory.
func writePlugin(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pluginSrv is the scripted provider: one canned SSE reply per request
// index (the last repeats), every body captured.
type pluginSrv struct {
	mu      sync.Mutex
	bodies  [][]byte
	replies []string
}

func newPluginSrv(t *testing.T, s *pluginSrv) *httptest.Server {
	t.Helper()
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
			s.mu.Unlock()
			w.Write([]byte(reply))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *pluginSrv) body(i int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.bodies) {
		return nil
	}
	return s.bodies[i]
}

func (s *pluginSrv) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func (s *pluginSrv) last() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		return nil
	}
	return s.bodies[len(s.bodies)-1]
}

// toolCallReply is one SSE tool_call (the golden e2e's shape): the
// arguments ride the data line's JSON string, so their quotes are
// escaped.
func toolCallReply(id, name, argumentsJSON string) string {
	esc := strings.ReplaceAll(argumentsJSON, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	return `data: {"choices":[{"delta":{"tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":"` + esc + `"}}]},"finish_reason":"tool_calls"}]}` + "\n"
}

const pongReply = `data: {"choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n"

// wireTools pulls the request's tools array (name, description, parameters).
func wireTools(t *testing.T, body []byte) []struct {
	Name        string
	Description string
	Parameters  json.RawMessage
} {
	t.Helper()
	var req struct {
		Tools []struct {
			Function struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal the captured body: %v", err)
	}
	out := make([]struct {
		Name        string
		Description string
		Parameters  json.RawMessage
	}, 0, len(req.Tools))
	for _, tl := range req.Tools {
		out = append(out, struct {
			Name        string
			Description string
			Parameters  json.RawMessage
		}{tl.Function.Name, tl.Function.Description, tl.Function.Parameters})
	}
	return out
}

// toolMessageOf pulls the last tool-role message's content out of a
// request (the transcript replays its earlier tool results — the
// latest is the call under assertion).
func toolMessageOf(t *testing.T, body []byte) string {
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
	found := false
	for _, m := range req.Messages {
		if m.Role == "tool" {
			last = m.Content
			found = true
		}
	}
	if !found {
		t.Fatal("the request carries no tool message")
	}
	return last
}

// pluginEnv is rigEnv plus the kernel's interpreter (RIG_PYTHON, the
// chain's seam) when py is named.
func pluginEnv(scratch, py string) []string {
	env := rigEnv(scratch, "")
	if py != "" {
		env = append(env, "RIG_PYTHON="+py)
	}
	return env
}

// --- the home and the migration (SPEC_CONFIG 11, SPEC_PLUGINS 6) ---

// TestRigHomeResolvesEnvOverDefault (SPEC_PLUGINS, named): RIG_HOME set
// (non-empty) is the home, whatever ~/.rig holds; unset, the home is
// ~/.rig; the resolution never reads XDG_CONFIG_HOME.
func TestRigHomeResolvesEnvOverDefault(t *testing.T) {
	scratch := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", scratch)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	t.Run("RIG_HOME set wins", func(t *testing.T) {
		override := filepath.Join(t.TempDir(), "the-home")
		t.Setenv("RIG_HOME", override)
		got, err := rigHome()
		if err != nil {
			t.Fatalf("rigHome: %v", err)
		}
		if got != override {
			t.Fatalf("RIG_HOME set (non-empty): the home = %q, want %q", got, override)
		}
	})
	t.Run("unset is ~/.rig", func(t *testing.T) {
		t.Setenv("RIG_HOME", "")
		got, err := rigHome()
		if err != nil {
			t.Fatalf("rigHome: %v", err)
		}
		if want := filepath.Join(scratch, ".rig"); got != want {
			t.Fatalf("the home = %q, want %q (XDG_CONFIG_HOME is not consulted)", got, want)
		}
	})
	t.Run("no HOME and no RIG_HOME refuses", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("RIG_HOME", "")
		if _, err := rigHome(); err == nil || !strings.Contains(err.Error(), "RIG_HOME") {
			t.Fatalf("a resolvable home is load-bearing: the refusal must name the override, got %v", err)
		}
	})
}

func captureStderr(t *testing.T, f func() (string, error)) (string, string, error) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	var out string
	var fErr error
	out, fErr = f()
	w.Close()
	os.Stderr = old
	buf, _ := io.ReadAll(r)
	return out, string(buf), fErr
}

// TestMigrationRenamesTheOldHomeOnce (SPEC_PLUGINS, named): the old
// ~/.config/rig with a marker, ~/.rig absent — the rename happens, the
// marker is read from the new home (the config load sees it), the old
// directory is gone, exactly one migration line, and the second start
// prints none (once, by construction).
func TestMigrationRenamesTheOldHomeOnce(t *testing.T) {
	scratch := t.TempDir()
	oldHome := filepath.Join(scratch, ".config", "rig")
	if err := os.MkdirAll(oldHome, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := `{"system": "MIGRATED-MARKER"}`
	if err := os.WriteFile(filepath.Join(oldHome, "settings.json"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", scratch)
	t.Setenv("RIG_HOME", "")

	home, stderr, err := captureStderr(t, rigHome)
	if err != nil {
		t.Fatalf("rigHome: %v\n%s", err, stderr)
	}
	want := filepath.Join(scratch, ".rig")
	if home != want {
		t.Fatalf("the home = %q, want %q", home, want)
	}
	// the migration's one line.
	wantLine := "rig: migrated the config home: " + oldHome + " -> " + want
	if strings.Count(stderr, wantLine) != 1 {
		t.Fatalf("the migration line must print exactly once, got %q", stderr)
	}
	// the old directory is gone; the marker rides the new home.
	if _, err := os.Stat(oldHome); !os.IsNotExist(err) {
		t.Fatalf("the old home must be gone after the rename (stat: %v)", err)
	}
	cfg, err := config.Load(home, t.TempDir())
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Settings.System != "MIGRATED-MARKER" {
		t.Fatalf("the config load must see the migrated marker, got %q", cfg.Settings.System)
	}
	// the second start: silent, no rename.
	_, stderr2, err := captureStderr(t, rigHome)
	if err != nil {
		t.Fatalf("rigHome (second): %v", err)
	}
	if strings.Contains(stderr2, "migrated") {
		t.Fatalf("the second start must be silent, got %q", stderr2)
	}
}

// TestMigrationNoOps (SPEC_PLUGINS, named): the old home absent (silent
// no-op); the home already present (the old directory left intact,
// silent); a failed rename (a read-only $HOME) refuses loud.
func TestMigrationNoOps(t *testing.T) {
	t.Run("the old home absent", func(t *testing.T) {
		scratch := t.TempDir()
		t.Setenv("HOME", scratch)
		t.Setenv("RIG_HOME", "")
		home, stderr, err := captureStderr(t, rigHome)
		if err != nil || strings.Contains(stderr, "migrated") {
			t.Fatalf("(err, stderr) = (%v, %q); nothing to migrate is a silent no-op", err, stderr)
		}
		if want := filepath.Join(scratch, ".rig"); home != want {
			t.Fatalf("the home = %q, want %q", home, want)
		}
	})
	t.Run("the home already present", func(t *testing.T) {
		scratch := t.TempDir()
		newHome := filepath.Join(scratch, ".rig")
		oldHome := filepath.Join(scratch, ".config", "rig")
		for _, d := range []string{newHome, oldHome} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(oldHome, "settings.json"), []byte(`{"system":"OLD"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", scratch)
		t.Setenv("RIG_HOME", "")
		home, stderr, err := captureStderr(t, rigHome)
		if err != nil || strings.Contains(stderr, "migrated") {
			t.Fatalf("(err, stderr) = (%v, %q); a present home wins, silently", err, stderr)
		}
		if home != newHome {
			t.Fatalf("the home = %q, want %q", home, newHome)
		}
		if got, err := os.ReadFile(filepath.Join(oldHome, "settings.json")); err != nil || string(got) != `{"system":"OLD"}` {
			t.Fatalf("the old directory must be left intact (err=%v, contents=%q)", err, got)
		}
	})
	t.Run("a failed rename refuses loud", func(t *testing.T) {
		scratch := t.TempDir()
		oldHome := filepath.Join(scratch, ".config", "rig")
		if err := os.MkdirAll(oldHome, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", scratch)
		t.Setenv("RIG_HOME", "")
		// a read-only $HOME: the rename's target parent is not writable.
		if err := os.Chmod(scratch, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(scratch, 0o755) })
		_, stderr, err := captureStderr(t, rigHome)
		if err == nil {
			t.Fatalf("the failed rename must refuse the start; stderr=%q", stderr)
		}
		if !strings.Contains(err.Error(), "migrate the config home") || !strings.Contains(err.Error(), oldHome) {
			t.Fatalf("the refusal must name the migration and the old home, got %v", err)
		}
	})
}

// TestMigrationNeverRunsUnderAnOverride (SPEC_CONFIG 11, named):
// RIG_HOME set to an **absent** directory, the old ~/.config/rig
// present: no rename, no migration line, and the old home untouched
// (the marker file still there) — the override is isolation, not a
// move order (the RIG_HOME=$(mktemp -d) shape), whatever the override
// holds.
func TestMigrationNeverRunsUnderAnOverride(t *testing.T) {
	scratch := t.TempDir()
	oldHome := filepath.Join(scratch, ".config", "rig")
	if err := os.MkdirAll(oldHome, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := `{"system": "OLD-DATA"}`
	if err := os.WriteFile(filepath.Join(oldHome, "settings.json"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(t.TempDir(), "absent-override") // not present
	t.Setenv("HOME", scratch)
	t.Setenv("RIG_HOME", override)

	home, stderr, err := captureStderr(t, rigHome)
	if err != nil {
		t.Fatalf("rigHome: %v\n%s", err, stderr)
	}
	if home != override {
		t.Fatalf("the override is the home, as-is: got %q, want %q", home, override)
	}
	if strings.Contains(stderr, "migrated") {
		t.Fatalf("the migration must never run under an override, got %q", stderr)
	}
	if got, rerr := os.ReadFile(filepath.Join(oldHome, "settings.json")); rerr != nil || string(got) != marker {
		t.Fatalf("the old home must be untouched (err=%v, contents=%q)", rerr, got)
	}
	if _, statErr := os.Stat(override); statErr == nil {
		t.Fatalf("the override must not have been created by a migration rename")
	}
}

// TestRigHomeOverrideBeatsTheOldHome (SPEC_PLUGINS, named): RIG_HOME
// pointing at a home with settings.json while the old ~/.config/rig
// holds a different one — the run takes the override's settings, and
// the old directory is left intact (the present-home edge).
func TestRigHomeOverrideBeatsTheOldHome(t *testing.T) {
	s := &bodySrv{}
	srv := newBodySrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	override := filepath.Join(t.TempDir(), "the-home")
	if err := os.MkdirAll(override, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(override, "settings.json"), []byte(`{"system": "FROM-RIG-HOME"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	oldHome := filepath.Join(scratch, ".config", "rig")
	if err := os.MkdirAll(oldHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldHome, "settings.json"), []byte(`{"system": "FROM-OLD"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	env := rigEnv(scratch, "")
	env = append(env, "RIG_HOME="+override)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	if got := systemOf(t, s.last()); got != "FROM-RIG-HOME" {
		t.Fatalf("the override's settings must win, got %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(oldHome, "settings.json")); err != nil || string(got) != `{"system": "FROM-OLD"}` {
		t.Fatalf("the old home must be left intact when the override's home is present (err=%v, contents=%q)", err, got)
	}
}

// --- the discovery, the collision, the call (SPEC_PLUGINS, e2e) ---

// TestPluginsDiscoveryRegistersAndSkips (SPEC_PLUGINS, named): a
// fixture directory with a good, a broken-import, and a missing-SCHEMA
// plugin — the startup prints exactly the two skip lines (file and
// field named), the request's tools array is the native set (15,
// post-8) plus the good plugin (its DESCRIPTION and SCHEMA verbatim),
// and the broken and missing ones are absent from the wire.
func TestPluginsDiscoveryRegistersAndSkips(t *testing.T) {
	py := pluginKernelPy(t)
	s := &pluginSrv{replies: []string{pongReply}}
	srv := newPluginSrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	writePlugin(t, cfgDir(t, scratch), "echo.py", `DESCRIPTION = "the fixture echo plugin"
SCHEMA = {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}

def run(args: dict) -> str:
    return "echo: " + args["text"]
`)
	writePlugin(t, cfgDir(t, scratch), "broken.py", `DESCRIPTION = "the broken fixture"
SCHEMA = {"type": "object"}
y = x  # NameError at import time

def run(args):
    return "never"
`)
	writePlugin(t, cfgDir(t, scratch), "missing.py", `DESCRIPTION = "missing its schema"

def run(args):
    return "never"
`)
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = pluginEnv(scratch, py)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	// the skip lines: exactly two, the file and the field named, in
	// file order (broken.py before missing.py).
	skipLines := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "rig: plugins:") {
			skipLines++
		}
	}
	if skipLines != 2 {
		t.Fatalf("skip lines = %d, want exactly 2:\n%s", skipLines, out)
	}
	if !strings.Contains(string(out), "rig: plugins: broken.py: NameError: name 'x' is not defined") {
		t.Fatalf("the broken plugin's line must name the file and the exception:\n%s", out)
	}
	if !strings.Contains(string(out), "rig: plugins: missing.py: TypeError: missing SCHEMA") {
		t.Fatalf("the missing-schema plugin's line must name the file and the field:\n%s", out)
	}
	// the wire: the native set plus echo, in order; the broken and
	// missing ones absent.
	tools := wireTools(t, s.body(0))
	if len(tools) != len(nativeToolNames)+1 {
		t.Fatalf("tools = %d, want %d (the native set plus the loaded plugin)", len(tools), len(nativeToolNames)+1)
	}
	for i, name := range nativeToolNames {
		if tools[i].Name != name {
			t.Fatalf("the wire's head must be the native set in order; position %d = %q, want %q", i, tools[i].Name, name)
		}
	}
	last := tools[len(tools)-1]
	if last.Name != "echo" {
		t.Fatalf("the wire's tail = %q, want the loaded plugin", last.Name)
	}
	if last.Description != "the fixture echo plugin" {
		t.Fatalf("the plugin's description must ride verbatim, got %q", last.Description)
	}
	var gotSchema, wantSchema any
	if err := json.Unmarshal(last.Parameters, &gotSchema); err != nil {
		t.Fatalf("the plugin's schema is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}`), &wantSchema); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotSchema, wantSchema) {
		t.Fatalf("the plugin's schema must ride through as the same JSON value, got %s", last.Parameters)
	}
}

// TestPluginCollisionRefusesLoud (SPEC_PLUGINS, named): a fixture
// plugin named like a native tool — exit non-zero, the refusal names
// the plugin's file and the native tool, and no state store is created
// (the refusal is before the stores).
func TestPluginCollisionRefusesLoud(t *testing.T) {
	py := pluginKernelPy(t)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	writePlugin(t, cfgDir(t, scratch), "bash.py", `DESCRIPTION = "shadowing bash"
SCHEMA = {"type": "object"}

def run(args):
    return "plugin bash"
`)
	cmd := exec.Command(bin, "-p", "hello", "-base-url", "http://127.0.0.1:1/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = pluginEnv(scratch, py)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a collision must refuse the start, got exit 0:\n%s", out)
	}
	if !strings.Contains(string(out), `rig: plugins: name collision: "bash" (bash.py) is already a native tool`) {
		t.Fatalf("the refusal must name the plugin's file and the native tool:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(scratch, ".rig", "sessions")); !os.IsNotExist(statErr) {
		t.Fatalf("no state store may be created before the refusal (sessions stat: %v)", statErr)
	}
}

// TestPluginCallRoundTripsArgsResult (SPEC_PLUGINS, named): the model
// calls the good plugin with an args dict — the tool result on the
// wire is run's return, verbatim (args in, result out).
func TestPluginCallRoundTripsArgsResult(t *testing.T) {
	py := pluginKernelPy(t)
	s := &pluginSrv{replies: []string{
		toolCallReply("c1", "echo", `{"text":"hello rig"}`),
		pongReply,
	}}
	srv := newPluginSrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	writePlugin(t, cfgDir(t, scratch), "echo.py", `DESCRIPTION = "the fixture echo plugin"
SCHEMA = {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}

def run(args: dict) -> str:
    return "echo: " + args["text"]
`)
	// the allow-list: the file's list is the list (the embedded default
	// is the native set — the plugin must be allow-listed, SPEC_PLUGINS 7).
	if err := os.WriteFile(filepath.Join(cfgDir(t, scratch), "settings.json"), []byte(`{"allow": ["echo"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-p", "echo it", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = pluginEnv(scratch, py)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	if s.count() != 2 {
		t.Fatalf("requests = %d, want 2 (the call, then the answer)", s.count())
	}
	if got := toolMessageOf(t, s.body(1)); got != "echo: hello rig" {
		t.Fatalf("the tool result = %q, want run's return verbatim (\"echo: hello rig\")", got)
	}
}

// TestPluginExceptionIsAToolErrorKernelAlive (SPEC_PLUGINS, named): a
// fixture plugin whose run raises — the tool result on the wire is a
// tool error carrying the traceback tail; the model's next call — the
// python tool — runs on the same kernel and returns its result (the
// kernel is alive after, the shared namespace intact).
func TestPluginExceptionIsAToolErrorKernelAlive(t *testing.T) {
	py := pluginKernelPy(t)
	s := &pluginSrv{replies: []string{
		toolCallReply("c1", "boom", `{"when":"nope"}`),
		toolCallReply("c2", "python", `{"code":"print(1+1)"}`),
		pongReply,
	}}
	srv := newPluginSrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	writePlugin(t, cfgDir(t, scratch), "boom.py", `DESCRIPTION = "the fixture boom plugin"
SCHEMA = {"type": "object", "properties": {"when": {"type": "string"}}}

def run(args: dict) -> str:
    print("partial output")
    raise ValueError("boom args: " + args["when"])
`)
	// the allow-list: the file's list is the list (the embedded default
	// is the native set — the plugin must be allow-listed, SPEC_PLUGINS 7).
	if err := os.WriteFile(filepath.Join(cfgDir(t, scratch), "settings.json"), []byte(`{"allow": ["boom", "python"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-p", "boom it", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = pluginEnv(scratch, py)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the run must succeed (the exception is a tool error, not a crash): %v\n%s", err, out)
	}
	if s.count() != 3 {
		t.Fatalf("requests = %d, want 3 (boom, python, the answer)", s.count())
	}
	boom := toolMessageOf(t, s.body(1))
	if !strings.Contains(boom, "ValueError: boom args: nope") {
		t.Fatalf("the tool error must carry the traceback tail, got %q", boom)
	}
	if !strings.Contains(boom, "partial output") {
		t.Fatalf("the partial output must ride along, got %q", boom)
	}
	pyout := toolMessageOf(t, s.body(2))
	if !strings.Contains(pyout, "2") {
		t.Fatalf("the python tool's result on the same kernel = %q, want the kernel alive after the exception", pyout)
	}
}

// TestRigHomeOverrideWins (SPEC_PLUGINS, named): RIG_HOME set (a home
// with a plugins/ directory and a settings.json) over the scratch
// ~/.rig and the old ~/.config/rig — the run takes the override's
// settings, discovers the override's plugins, and leaves both the
// default home and the old one untouched.
func TestRigHomeOverrideWins(t *testing.T) {
	py := pluginKernelPy(t)
	s := &pluginSrv{replies: []string{pongReply}}
	srv := newPluginSrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	override := filepath.Join(t.TempDir(), "the-home")
	if err := os.MkdirAll(override, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(override, "settings.json"), []byte(`{"system": "FROM-OVERRIDE"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writePlugin(t, override, "echo.py", `DESCRIPTION = "the override's echo plugin"
SCHEMA = {"type": "object"}

def run(args):
    return "override echo"
`)
	// the competing homes: the default and the old, both with markers.
	for _, d := range []string{filepath.Join(scratch, ".rig"), filepath.Join(scratch, ".config", "rig")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "settings.json"), []byte(`{"system": "FROM-LOSER"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	env := rigEnv(scratch, "")
	env = append(env, "RIG_HOME="+override, "RIG_PYTHON="+py)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	if got := systemOf(t, s.last()); got != "FROM-OVERRIDE" {
		t.Fatalf("the override's settings must win, got %q", got)
	}
	tools := wireTools(t, s.body(0))
	found := false
	for _, tl := range tools {
		if tl.Name == "echo" && tl.Description == "the override's echo plugin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the override's plugin must be discovered (tools=%d)", len(tools))
	}
	for _, d := range []string{filepath.Join(scratch, ".rig"), filepath.Join(scratch, ".config", "rig")} {
		if got, rerr := os.ReadFile(filepath.Join(d, "settings.json")); rerr != nil || string(got) != `{"system": "FROM-LOSER"}` {
			t.Fatalf("the competing home %s must be left untouched (err=%v, contents=%q)", d, rerr, got)
		}
	}
}

// TestNoPluginsDirectoryIsTheV020Wire (SPEC_PLUGINS, named): a fixture
// run (no plugins directory) carries exactly the native tool names in
// the tools array — the 0.2.0 wire, byte-exact (the golden pin's
// companion assertion).
func TestNoPluginsDirectoryIsTheV020Wire(t *testing.T) {
	s := &bodySrv{}
	srv := newBodySrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(scratch, "")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	tools := wireTools(t, s.last())
	if len(tools) != len(nativeToolNames) {
		t.Fatalf("tools = %d, want the native set (no plugins directory, no plugins)", len(tools))
	}
	for i, name := range nativeToolNames {
		if tools[i].Name != name {
			t.Fatalf("position %d = %q, want %q (the 0.2.0 order)", i, tools[i].Name, name)
		}
	}
}

// TestBothHomesPresentIsNamed (SPEC_CONFIG 11, amended): with the old
// ~/.config/rig present and ~/.rig also present, the migration never
// fires (a present home wins) — and the leftover is named on stderr,
// so a machine where a dev build half-birthed ~/.rig does not lose its
// real data to silence. Nothing moves; both directories are untouched.
func TestBothHomesPresentIsNamed(t *testing.T) {
	scratch := t.TempDir()
	old := filepath.Join(scratch, ".config", "rig")
	newH := filepath.Join(scratch, ".rig")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newH, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(old, "marker.txt")
	if err := os.WriteFile(marker, []byte("real data"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildBin(t, t.TempDir())
	cmd := exec.Command(bin, "-p", "x", "-base-url", "http://127.0.0.1:1/v1", "-retries", "0")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(scratch, "")
	out, _ := cmd.CombinedOutput() // the model call fails; the startup ran
	if !strings.Contains(string(out), "the old config home still exists: "+old) {
		t.Fatalf("the leftover old home must be named on stderr:\n%s", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the old home must be untouched: %v", err)
	}
	if strings.Contains(string(out), "migrated the config home") {
		t.Fatalf("the migration must not fire with the home present:\n%s", out)
	}
}
