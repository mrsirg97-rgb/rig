package main

// The provenance rule's named cases (SPEC_SANDBOX 2, testing): the
// model's write into plugins/ refuses with the teaching voice, into
// plugins/pending/ lands, discovery never loads the pending zone, and
// the operator's /plugins pending and /plugins approve verbs do their
// work at the command door. The e2e runs the built binary with a
// scratch home and a scripted provider; the operator verbs ride the
// piped CLI (the command door is Frontend-side by construction).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/plugins"
)

// TestListPluginFilesIgnoresThePendingZone (SPEC_SANDBOX, named):
// discovery's listing is the top-level *.py rule — the pending zone is
// a directory, so it is not listed, whatever it holds. No loader
// change at all.
func TestListPluginFilesIgnoresThePendingZone(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	if err := os.MkdirAll(filepath.Join(pluginsDir, "pending"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "echo.py"), []byte("top level"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "pending", "other.py"), []byte("pending"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := plugins.List(home)
	if err != nil {
		t.Fatalf("plugins.List: %v", err)
	}
	if len(files) != 1 || files[0] != filepath.Join(pluginsDir, "echo.py") {
		t.Fatalf("the listing = %v, want only the top-level file", files)
	}
}

// writeCallArgs is a write tool call's args dict, marshalled for the
// scripted provider's tool_call.
func writeCallArgs(t *testing.T, path, content string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"path": path, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestModelWriteIntoPluginsRefusesLoud (SPEC_SANDBOX, named): the
// model's write into plugins/x.py refuses with the teaching voice —
// the refusal rides back as the tool result, and the file is not
// created.
func TestModelWriteIntoPluginsRefusesLoud(t *testing.T) {
	scratch := t.TempDir()
	// the refusal needs no python: the write call dies in the chain.
	s := &pluginSrv{replies: []string{
		toolCallReply("c1", "write", writeCallArgs(t, filepath.Join(scratch, ".rig", "plugins", "evil.py"), "evil = 1")),
		pongReply,
	}}
	srv := newPluginSrv(t, s)
	bin := buildBin(t, t.TempDir())
	target := filepath.Join(scratch, ".rig", "plugins", "evil.py")
	cmd := exec.Command(bin, "-p", "forge it", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(scratch, "")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the run must succeed (the refusal is a tool error, not a fault): %v\n%s", err, out)
	}
	if s.count() != 2 {
		t.Fatalf("requests = %d, want 2 (the refused call, then the answer)", s.count())
	}
	got := toolMessageOf(t, s.body(1))
	if !strings.Contains(got, target) {
		t.Fatalf("the refusal must name the target, got %q", got)
	}
	want := "plugins install by the operator's /plugins approve; write to plugins/pending/"
	if !strings.Contains(got, want) {
		t.Fatalf("the refusal must teach %q, got %q", want, got)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("the refused write must not create the file (stat: %v)", err)
	}
}

// TestModelWriteIntoThePendingZoneLands (SPEC_SANDBOX, named): into
// plugins/pending/ the same write lands — the zone is created at
// startup (the write tool makes no directories), and the file is the
// model's bytes.
func TestModelWriteIntoThePendingZoneLands(t *testing.T) {
	s := &pluginSrv{replies: []string{pongReply}}
	srv := newPluginSrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	target := filepath.Join(scratch, ".rig", "plugins", "pending", "forge.py")

	body := `DESCRIPTION = "the fixture forge plugin"
SCHEMA = {"type": "object"}

def run(args):
    return "forged"
`
	s.replies = []string{
		toolCallReply("c1", "write", writeCallArgs(t, target, body)),
		pongReply,
	}
	cmd := exec.Command(bin, "-p", "forge it", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(scratch, "")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	if s.count() != 2 {
		t.Fatalf("requests = %d, want 2 (the call, then the answer)", s.count())
	}
	if got := toolMessageOf(t, s.body(1)); !strings.Contains(got, "wrote") {
		t.Fatalf("the write must land, got the tool result %q", got)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the pending file must land in the zone: %v", err)
	}
	if string(got) != body {
		t.Fatalf("the file's bytes = %q, want the model's content", got)
	}
}

// TestDiscoveryNeverLoadsPending (SPEC_SANDBOX, named): a pending
// plugin, valid and named like a plugin, is invisible to the wire —
// the top-level rule is the loader's whole surface.
func TestDiscoveryNeverLoadsPending(t *testing.T) {
	py := pluginKernelPy(t)
	s := &pluginSrv{replies: []string{pongReply}}
	srv := newPluginSrv(t, s)
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	writePlugin(t, cfgDir(t, scratch), "echo.py", `DESCRIPTION = "the fixture echo plugin"
SCHEMA = {"type": "object"}

def run(args):
    return "echo"
`)
	home := cfgDir(t, scratch)
	zone := filepath.Join(home, "plugins", "pending")
	if err := os.MkdirAll(zone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone, "other.py"), []byte(`DESCRIPTION = "the pending fixture"
SCHEMA = {"type": "object"}

def run(args):
    return "other"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-p", "hello", "-base-url", srv.URL+"/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = pluginEnv(scratch, py)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}
	names := pluginNamesIn(s.body(0))
	foundEcho, foundOther := false, false
	for _, n := range names {
		if n == "echo" {
			foundEcho = true
		}
		if n == "other" {
			foundOther = true
		}
	}
	if !foundEcho {
		t.Fatalf("the top-level plugin must be in the plugin door's enum, got %v", names)
	}
	if foundOther {
		t.Fatalf("the pending plugin must never load, got %v", names)
	}
}

// pipedCommands runs the binary with command lines on stdin (the piped
// CLI's dispatch) and returns the stdout the operator sees. No model
// turn happens: the commands never reach the loop.
func pipedCommands(t *testing.T, bin string, scratch string, lines ...string) string {
	t.Helper()
	cmd := exec.Command(bin, "-base-url", "http://127.0.0.1:1/v1")
	cmd.Dir = t.TempDir()
	cmd.Env = rigEnv(scratch, "")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("the run must succeed: %v\nstdout: %s\nstderr: %s", err, out, ee.Stderr)
		}
		t.Fatalf("the run must succeed: %v\nstdout: %s", err, out)
	}
	return string(out)
}

// TestApproveMovesThePendingPlugin (SPEC_SANDBOX, named): the
// operator's /plugins approve <name> moves the pending file to the
// top level — the /plugins pending line before the approve lists it
// with its DESCRIPTION, and the zone is empty after.
func TestApproveMovesThePendingPlugin(t *testing.T) {
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	home := cfgDir(t, scratch)
	zone := filepath.Join(home, "plugins", "pending")
	if err := os.MkdirAll(zone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone, "forge.py"), []byte(`DESCRIPTION = "the fixture forge plugin"
SCHEMA = {"type": "object"}

def run(args):
    return "forged"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := pipedCommands(t, bin, scratch,
		"/plugins pending",
		"/plugins approve forge",
		"/plugins pending",
	)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 7 {
		t.Fatalf("the dispatch prints the commands' lines (post-8: the approve's tail is the reload's), got %q", out)
	}
	// the listing before: the zone's file with its DESCRIPTION.
	if !strings.Contains(lines[0], "pending") {
		t.Fatalf("the listing's header must name the pending count, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "forge: the fixture forge plugin") {
		t.Fatalf("the pending listing must carry the DESCRIPTION, got %q", lines[1])
	}
	// the approve: the move named.
	if !strings.Contains(lines[2], "approved forge") || !strings.Contains(lines[2], "pending") {
		t.Fatalf("the approve line must name the move, got %q", lines[2])
	}
	// the reload's tail (post-8): the list rebuilt, the forged plugin
	// loaded from its new top-level home.
	if !strings.Contains(lines[3], "reload: 1 loaded, 0 skipped") {
		t.Fatalf("the approve's tail must be the reload's (SPEC_SANDBOX, post-8), got %q", lines[3])
	}
	if lines[4] != "loaded:" || !strings.Contains(lines[5], "forge: the fixture forge plugin") || strings.Contains(lines[5], "pending") {
		t.Fatalf("the reload's listing must carry the loaded plugin at its top-level home, got %q", lines[5])
	}
	// the listing after: empty.
	if lines[6] != "plugins: no pending plugins" {
		t.Fatalf("the zone must be empty after the approve, got %q", lines[6])
	}
	// the filesystem: moved.
	if _, err := os.Stat(filepath.Join(home, "plugins", "forge.py")); err != nil {
		t.Fatalf("the approved plugin must be at the top level: %v", err)
	}
	if _, err := os.Stat(filepath.Join(zone, "forge.py")); !os.IsNotExist(err) {
		t.Fatalf("the pending file must be gone: %v", err)
	}
}

// TestApproveNativeCollisionRefusesAtTheNewDoor (SPEC_SANDBOX, named):
// approve of a name that collides with a native refuses with the
// existing rule's voice — the pending file stays, nothing lands at the
// top level.
func TestApproveNativeCollisionRefusesAtTheNewDoor(t *testing.T) {
	bin := buildBin(t, t.TempDir())
	scratch := t.TempDir()
	home := cfgDir(t, scratch)
	zone := filepath.Join(home, "plugins", "pending")
	if err := os.MkdirAll(zone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone, "bash.py"), []byte(`DESCRIPTION = "shadowing bash"
SCHEMA = {"type": "object"}

def run(args):
    return "plugin bash"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := pipedCommands(t, bin, scratch, "/plugins approve bash")
	if !strings.Contains(out, `name collision: "bash" (bash.py) is already a native tool`) {
		t.Fatalf("the refusal must be the existing rule's voice, got %q", out)
	}
	if _, err := os.Stat(filepath.Join(zone, "bash.py")); err != nil {
		t.Fatalf("the refused approve must leave the pending file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "plugins", "bash.py")); !os.IsNotExist(err) {
		t.Fatalf("the refused approve must not create the top-level file (stat: %v)", err)
	}
}
