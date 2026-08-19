package perm_test

// The plugin provenance rule (SPEC_SANDBOX 2): the model's write and
// edit refuse a target inside the rig home's plugins/ that is not
// inside plugins/pending/; the model's authoring lands in the pending
// zone, and the operator's /plugins approve installs. The rule is the
// workflow guard for the honest path — bash can still move a file
// there — so the tests pin the path decision, not a boundary.

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

// pluginCall runs one named call with raw args through the given
// middleware and reports whether the exec saw it.
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

// TestPluginsRuleRefusesOutsidePending (SPEC_SANDBOX, named): the
// model's write and edit refuse a target inside plugins/ that is not
// inside plugins/pending/ — the refusal teaches the operator's verb
// and the pending zone, and the exec never runs.
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

// TestPluginsRuleAllowsThePendingZone (SPEC_SANDBOX, named): into
// plugins/pending/ the same calls land — the forge's landing zone is
// the model's authoring path.
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

// TestPluginsRuleIgnoresOtherTools (SPEC_SANDBOX 2): the rule names
// write and edit — read can still look at the installed set, and the
// other tools are not path rules.
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

// TestPluginsRuleIgnoresForeignPluginsDirs: a directory named plugins/
// elsewhere is not the rig home's — the rule is the home's rule, by
// path.
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

// TestPluginsRuleSiblingPrefixIsOutside: pluginspending/ shares the
// plugins/ prefix but is a sibling — the zone test is a path test, not
// a string prefix.
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

// TestPluginsRuleTargetIsThePluginsDir: the plugins/ directory itself
// is inside plugins/ and not inside plugins/pending/ — refused.
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

// TestPluginsRulePassesThroughWithoutAPath: the args are the tool's to
// parse — a missing path is the tool's own loud voice, not this
// rule's.
func TestPluginsRulePassesThroughWithoutAPath(t *testing.T) {
	home := t.TempDir()
	calls, _, err := pluginCall(t, perm.Plugins(filepath.Join(home, "plugins")), "write", `{}`)
	if err != nil || calls != 1 {
		t.Fatalf("a path-less write must pass through to the tool's own voice, got %d calls (err=%v)", calls, err)
	}
}

// TestPluginsRuleRelativePaths (SPEC_SANDBOX, named companion): the
// model spells the target relative to the cwd; the rule decides on the
// absolute path, whatever the spelling.
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

// TestDenialOrderTheAllowlistSpeaksFirst: the chain lists the
// provenance rule before the allow-list (first-listed is innermost, so
// the allow-list is consulted first) — a tool that is not allowed
// speaks with the allow-list's voice, the more basic refusal, whatever
// the path is.
func TestDenialOrderTheAllowlistSpeaksFirst(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	target := filepath.Join(pluginsDir, "x.py")

	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		t.Fatal("a denied call must not reach the exec")
		return "", nil
	}
	exec = perm.Plugins(pluginsDir).Wrap(exec) // listed first = innermost: consulted after the allow-list
	exec = perm.Allowlist("bash").Wrap(exec)

	content, err := exec(context.Background(), core.ToolCall{
		ID:   "c1",
		Name: "write", // not in the allow-list
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
