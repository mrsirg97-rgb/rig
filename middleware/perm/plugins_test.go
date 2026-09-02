package perm_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/perm"
)

func pluginCall(t *testing.T, mw core.ToolMiddleware, name, args string) (calls int, content string, err error) {
	t.Helper()
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		calls++
		return "executed", nil
	}
	exec = mw.Wrap(exec)
	content, err = exec(context.Background(), core.ToolCall{
		ID:   "c1",
		Name: name,
		Args: json.RawMessage(args),
	})
	return calls, content, err
}

func TestPluginsRuleRefusesOutsidePending(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	target := filepath.Join(pluginsDir, "x.py")
	for _, name := range []string{"write", "edit"} {
		calls, content, err := pluginCall(t, perm.Plugins(pluginsDir), name,
			`{"path": `+mustJSON(t, target)+`, "content": "x"}`)
		if err == nil {
			t.Fatalf("%s into %s must be refused", name, target)
		}
		if calls != 0 {
			t.Fatalf("%s into %s reached the exec %d times", name, target, calls)
		}
		if !strings.Contains(content, target) {
			t.Fatalf("the refusal must name the target, got %q", content)
		}
		want := "plugins install by the operator's /plugins approve; write to plugins/pending/"
		if !strings.Contains(content, want) {
			t.Fatalf("the refusal must teach %q, got %q", want, content)
		}
	}
}

func TestPluginsRuleAllowsThePendingZone(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	for _, name := range []string{"write", "edit"} {
		for _, sub := range []string{"x.py", filepath.Join("sub", "x.py")} {
			target := filepath.Join(pluginsDir, "pending", sub)
			calls, content, err := pluginCall(t, perm.Plugins(pluginsDir), name,
				`{"path": `+mustJSON(t, target)+`, "old": "a", "new": "b"}`)
			if err != nil || calls != 1 {
				t.Fatalf("%s into %s must pass through, got %d calls / %q (err=%v)", name, target, calls, content, err)
			}
		}
	}
}

func TestPluginsRuleResolvesSymlinksBeforeApplyingTheZone(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	pending := filepath.Join(pluginsDir, "pending")
	if err := os.MkdirAll(pending, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(pending, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(link, "x.py")
	calls, _, err := pluginCall(t, perm.Plugins(pluginsDir), "write", `{"path": `+mustJSON(t, target)+`, "content": "x"}`)
	if err != nil || calls != 1 {
		t.Fatalf("a resolved target outside the plugin root is foreign and must pass through, got %d calls / %v", calls, err)
	}
	liveTarget := filepath.Join(pluginsDir, "live.py")
	if err := os.WriteFile(liveTarget, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	liveLink := filepath.Join(outside, "live.py")
	if err := os.Symlink(liveTarget, liveLink); err != nil {
		t.Fatal(err)
	}
	calls, _, err = pluginCall(t, perm.Plugins(pluginsDir), "write", `{"path": `+mustJSON(t, liveLink)+`, "content": "x"}`)
	if err == nil || calls != 0 {
		t.Fatalf("a foreign spelling that resolves into live plugins must refuse, got %d calls / %v", calls, err)
	}
}

func TestPluginsRuleIgnoresOtherTools(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	target := filepath.Join(pluginsDir, "x.py")
	for _, name := range []string{"read", "bash", "ls"} {
		calls, _, err := pluginCall(t, perm.Plugins(pluginsDir), name,
			`{"path": `+mustJSON(t, target)+`}`)
		if err != nil || calls != 1 {
			t.Fatalf("%s into %s must pass through, got %d calls (err=%v)", name, target, calls, err)
		}
	}
}

func TestPluginsRuleIgnoresForeignPluginsDirs(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	foreign := filepath.Join(home, "other", "plugins", "x.py")
	calls, _, err := pluginCall(t, perm.Plugins(pluginsDir), "write",
		`{"path": `+mustJSON(t, foreign)+`, "content": "x"}`)
	if err != nil || calls != 1 {
		t.Fatalf("a foreign plugins/ dir must pass through, got %d calls (err=%v)", calls, err)
	}
}

func TestPluginsRuleSiblingPrefixIsOutside(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	sibling := filepath.Join(home, "pluginspending", "x.py")
	calls, _, err := pluginCall(t, perm.Plugins(pluginsDir), "write",
		`{"path": `+mustJSON(t, sibling)+`, "content": "x"}`)
	if err != nil || calls != 1 {
		t.Fatalf("a sibling dir must pass through, got %d calls (err=%v)", calls, err)
	}
}

func TestPluginsRuleTargetIsThePluginsDir(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	calls, content, err := pluginCall(t, perm.Plugins(pluginsDir), "write",
		`{"path": `+mustJSON(t, pluginsDir)+`, "content": "x"}`)
	if err == nil || calls != 0 {
		t.Fatalf("the plugins/ dir itself must be refused, got %d calls (err=%v)", calls, err)
	}
	if !strings.Contains(content, "plugins/pending") {
		t.Fatalf("the refusal must teach the pending zone, got %q", content)
	}
}

func TestPluginsRulePassesThroughWithoutAPath(t *testing.T) {
	home := t.TempDir()
	calls, _, err := pluginCall(t, perm.Plugins(filepath.Join(home, "plugins")), "write", `{}`)
	if err != nil || calls != 1 {
		t.Fatalf("a path-less write must pass through to the tool's own voice, got %d calls (err=%v)", calls, err)
	}
}

func TestPluginsRuleRelativePaths(t *testing.T) {
	home := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })

	pluginsDir := filepath.Join(home, "plugins")
	outside, _, err := pluginCall(t, perm.Plugins(pluginsDir), "write",
		`{"path": "plugins/x.py", "content": "x"}`)
	if err == nil || outside != 0 {
		t.Fatalf("a relative target inside plugins/ must be refused, got %d calls (err=%v)", outside, err)
	}
	inside, _, err := pluginCall(t, perm.Plugins(pluginsDir), "write",
		`{"path": "plugins/pending/x.py", "content": "x"}`)
	if err != nil || inside != 1 {
		t.Fatalf("a relative target in plugins/pending/ must pass through, got %d calls (err=%v)", inside, err)
	}
}

func TestDenialOrderTheAllowlistSpeaksFirst(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	target := filepath.Join(pluginsDir, "x.py")

	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		t.Fatal("a denied call must not reach the exec")
		return "", nil
	}
	exec = perm.Plugins(pluginsDir).Wrap(exec)
	exec = perm.Allowlist("bash").Wrap(exec)

	content, err := exec(context.Background(), core.ToolCall{
		ID:   "c1",
		Name: "write",
		Args: json.RawMessage(`{"path": ` + mustJSON(t, target) + `, "content": "x"}`),
	})
	if err == nil {
		t.Fatal("an unlisted write must be denied")
	}
	if !strings.Contains(content, "allow") || !strings.Contains(content, "write") {
		t.Fatalf("the allow-list's voice must win over the provenance voice, got %q", content)
	}
}

func mustJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPluginsRuleExpandsATildeBeforeTheZone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pluginsDir := filepath.Join(home, ".rig", "plugins")
	if err := os.MkdirAll(filepath.Join(pluginsDir, "pending"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls, content, err := pluginCall(t, perm.Plugins(pluginsDir), "write",
		`{"path": "~/.rig/plugins/evil.py", "content": "x"}`)
	if err == nil || calls != 0 {
		t.Fatalf("a tilde path into plugins/ must be refused before the exec (calls %d, err %v)", calls, err)
	}
	if !strings.Contains(content, filepath.Join(pluginsDir, "evil.py")) {
		t.Fatalf("the refusal must name the expanded target, got %q", content)
	}
	calls, _, err = pluginCall(t, perm.Plugins(pluginsDir), "write",
		`{"path": "~/.rig/plugins/pending/ok.py", "content": "x"}`)
	if err != nil || calls != 1 {
		t.Fatalf("a tilde path into pending/ must pass (calls %d, err %v)", calls, err)
	}
}
