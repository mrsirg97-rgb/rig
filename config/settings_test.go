package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/config"
	"github.com/mrsirg97-rgb/rig/models"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func load(t *testing.T, dir, cwd string) *config.Config {
	t.Helper()
	cfg, err := config.Load(dir, cwd)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func loadErr(t *testing.T, dir, cwd string) error {
	t.Helper()
	_, err := config.Load(dir, cwd)
	if err == nil {
		t.Fatalf("Load = ok, want a refusal")
	}
	return err
}

func TestLoadAbsentFilesIsSilent(t *testing.T) {
	cfg := load(t, t.TempDir(), t.TempDir())
	if cfg.Settings.BaseURL == "" || cfg.Settings.Model == "" || cfg.Settings.System == "" {
		t.Fatalf("absent files must resolve to the embedded values, got %+v", cfg.Settings)
	}
	if len(cfg.Settings.Allow) == 0 || cfg.Settings.Retries == 0 {
		t.Fatalf("absent files must resolve to the embedded values, got %+v", cfg.Settings)
	}
	if cfg.Theme != nil {
		t.Fatalf("Theme = %s, want nil when absent", cfg.Theme)
	}
	if cfg.Agents != "" {
		t.Fatalf("Agents = %q, want empty when neither file exists", cfg.Agents)
	}
	if _, ok := cfg.Models.Get("local"); !ok {
		t.Fatalf("the embedded table must resolve with no user file, got %v", cfg.Models.Known())
	}
}

func TestLoadEmptyDirIsSilent(t *testing.T) {
	empty := t.TempDir()
	absent := t.TempDir()
	a := load(t, empty, t.TempDir())
	b := load(t, absent, t.TempDir())
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("an empty config dir must be silent (the same result as absent):\nempty: %+v\nabsent: %+v", a, b)
	}
}

func TestEmbeddedDefaultsAreTheV020Values(t *testing.T) {
	cfg := load(t, t.TempDir(), t.TempDir())
	s := cfg.Settings
	if s.BaseURL != "http://127.0.0.1:8090/v1" {
		t.Fatalf("baseUrl = %q, want the 0.2.0 flag default", s.BaseURL)
	}
	if s.Model != "local" {
		t.Fatalf("model = %q, want the 0.2.0 flag default", s.Model)
	}
	if s.System != "You are rig, a minimal coding agent. Use the tools to inspect, change, and run things in the working directory; answer in plain text when done. The harness enforces its walls — an allowlist, a retry guard, an approval gate, a plugin landing zone — and names each refusal; a refusal is final for that call: change the call or ask, never reach the same effect through another tool. Memory is a tool: recall before re-deriving a project fact, learn deliberately what the next session should not re-derive, supersede by id when the code disagrees. Python is a persistent kernel: compute there, don't estimate; a capability you build twice belongs in a plugin." {
		t.Fatalf("system = %q, want the 0.2.0 default system prompt", s.System)
	}
	wantAllow := []string{"bash", "read", "write", "edit", "ls", "find", "grep", "todo", "rem", "python", "web_search", "web_fetch", "diff", "plugin", "plugins", "sessions"}
	if !reflect.DeepEqual(s.Allow, wantAllow) {
		t.Fatalf("allow = %v, want the non-worker default list %v (the two worker tools join it only when workers.json names a fleet)", s.Allow, wantAllow)
	}
	if s.Retries != 3 {
		t.Fatalf("retries = %d, want the 0.2.0 default 3", s.Retries)
	}
	if s.Rounds != 0 {
		t.Fatalf("rounds = %d, want the embedded default 0 (no cap)", s.Rounds)
	}
	if s.ResultCap != 65536 {
		t.Fatalf("resultCap = %d, want the embedded default 64 KiB (65536)", s.ResultCap)
	}
	if s.SearXNG != "http://127.0.0.1:8888" {
		t.Fatalf("searxngUrl = %q, want the 0.2.0 default", s.SearXNG)
	}
	if s.WebFetchProxy == nil || *s.WebFetchProxy != "http://127.0.0.1:8889" {
		t.Fatalf("webFetchProxy = %v, want the 0.2.0 default", s.WebFetchProxy)
	}
	if s.Trafilatura != nil {
		t.Fatalf("trafilatura = %v, want nil (0.2.0: no default, auto)", s.Trafilatura)
	}
	if s.Python != "" {
		t.Fatalf("python = %q, want empty (0.2.0: no default)", s.Python)
	}
	if s.SwapURL != "http://127.0.0.1:8090" {
		t.Fatalf("swapUrl = %q, want the 0.2.0 runner default", s.SwapURL)
	}
	m, ok := cfg.Models.Get("local")
	if !ok {
		t.Fatal("the embedded table has no row for local")
	}
	if m.Window != 65536 || m.MaxTokens != 8192 || m.Reserve != 8192 || m.KeepRecent != 16384 {
		t.Fatalf("row local = %+v, want the 0.2.0 numbers", m)
	}
	if m.Role != models.RoleInteractive {
		t.Fatalf("row local role = %q, want interactive (4's named roles)", m.Role)
	}
	if _, ok := cfg.Models.Get("qwen3.8-workers"); ok {
		t.Fatalf("the embedded table still carries the qwen3.8-workers row (12 cut it: the worker's model is the operator's)")
	}
	if got := len(cfg.Models.Known()); got != 1 {
		t.Fatalf("the embedded table = %d rows, want the one local row (%v)", got, cfg.Models.Known())
	}
	if cfg.Workers != nil {
		t.Fatalf("Workers = %+v, want nil with no workers.json (no fleet, no worker tools)", cfg.Workers)
	}
}

func TestSettingsMalformedNamesFileAndField(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"retries type", `{"retries": "three"}`, `retries: expected an integer, got "three"`},
		{"rounds type", `{"rounds": "many"}`, `rounds: expected an integer, got "many"`},
		{"resultCap type", `{"resultCap": "big"}`, `resultCap: expected an integer, got "big"`},
		{"rounds negative", `{"rounds": -3}`, `rounds: expected a positive number (0 = no cap), got -3`},
		{"resultCap negative", `{"resultCap": -1}`, `resultCap: expected a positive number (0 = the default), got -1`},
		{"unknown key", `{"allowd": ["bash"]}`, `unknown key "allowd" (known: allow, approve, baseUrl, defaultJobModel, model, plugins, python, resultCap, retries, rounds, sandbox, sandboxBinds, searxngUrl, swapUrl, system, theme, trafilatura, webFetchProxy)`},
		{"not an object", `[1]`, `expected a JSON object`},
		{"allow element", `{"allow": ["bash", "read", 5]}`, `allow[2]: expected a string, got 5`},
		{"sandbox value", `{"sandbox": "maybe"}`, `sandbox: expected "jailed" or "off", got "maybe"`},
		{"sandboxBinds element", `{"sandboxBinds": ["/dev/nvidia0", 5]}`, `sandboxBinds[1]: expected a string, got 5`},
		{"approve value", `{"approve": "yolo"}`, `approve: expected "auto" or "manual", got "yolo"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := write(t, dir, "settings.json", c.content)
			err := loadErr(t, dir, t.TempDir())
			if err.Error() != "config: "+p+": "+c.want {
				t.Fatalf("the voice = %q, want %q", err, "config: "+p+": "+c.want)
			}
		})
	}
}

func TestSettingsZeroDescendsToEmbedded(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "settings.json", `{"retries": 0, "model": ""}`)
	cfg := load(t, dir, t.TempDir())
	if cfg.Settings.Retries != 3 {
		t.Fatalf("retries = %d, want the embedded 3 (zero descends)", cfg.Settings.Retries)
	}
	if cfg.Settings.Model != "local" {
		t.Fatalf("model = %q, want the embedded local (empty descends)", cfg.Settings.Model)
	}
}

func TestApproveDefaultsToAuto(t *testing.T) {
	cfg := load(t, t.TempDir(), t.TempDir())
	if cfg.Settings.Approve != "auto" {
		t.Fatalf("approve = %q, want the embedded default auto", cfg.Settings.Approve)
	}
	dir := t.TempDir()
	write(t, dir, "settings.json", `{"approve": "manual"}`)
	cfg = load(t, dir, t.TempDir())
	if cfg.Settings.Approve != "manual" {
		t.Fatalf("approve = %q, want the file's manual", cfg.Settings.Approve)
	}
}

func TestSandboxDefaultsToJailed(t *testing.T) {
	cfg := load(t, t.TempDir(), t.TempDir())
	if cfg.Settings.Sandbox != "jailed" {
		t.Fatalf("sandbox = %q, want the embedded default jailed (fail closed is the default)", cfg.Settings.Sandbox)
	}
	if len(cfg.Settings.SandboxBinds) != 0 {
		t.Fatalf("sandboxBinds = %v, want empty (no extra binds by default)", cfg.Settings.SandboxBinds)
	}

	dir := t.TempDir()
	write(t, dir, "settings.json", `{"sandbox": "off", "sandboxBinds": ["/dev/nvidia0", "/data:rw"]}`)
	cfg = load(t, dir, t.TempDir())
	if cfg.Settings.Sandbox != "off" {
		t.Fatalf("sandbox = %q, want the file's off (the operator's explicit act)", cfg.Settings.Sandbox)
	}
	want := []string{"/dev/nvidia0", "/data:rw"}
	if !reflect.DeepEqual(cfg.Settings.SandboxBinds, want) {
		t.Fatalf("sandboxBinds = %v, want %v", cfg.Settings.SandboxBinds, want)
	}
}

func TestSandboxBindsEmptyDescends(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "settings.json", `{"sandbox": "off", "sandboxBinds": []}`)
	cfg := load(t, dir, t.TempDir())
	if cfg.Settings.Sandbox != "off" {
		t.Fatalf("sandbox = %q, want the file's off", cfg.Settings.Sandbox)
	}
	if len(cfg.Settings.SandboxBinds) != 0 {
		t.Fatalf("sandboxBinds = %v, want the embedded empty (an empty file list is no binds)", cfg.Settings.SandboxBinds)
	}
}

func TestSettingsThemeIsAKnownKey(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "settings.json", `{"theme": "paper"}`)
	cfg := load(t, dir, t.TempDir())
	if cfg.Settings.Theme != "paper" {
		t.Fatalf("theme = %q, want paper", cfg.Settings.Theme)
	}
}

func TestSettingsPresenceKeysInFileAreExplicit(t *testing.T) {
	t.Run("webFetchProxy empty is the choice", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "settings.json", `{"webFetchProxy": ""}`)
		cfg := load(t, dir, t.TempDir())
		if cfg.Settings.WebFetchProxy == nil {
			t.Fatalf("webFetchProxy = nil, want set-empty (direct egress is the choice)")
		}
		if *cfg.Settings.WebFetchProxy != "" {
			t.Fatalf("webFetchProxy = %q, want empty", *cfg.Settings.WebFetchProxy)
		}
	})
	t.Run("trafilatura empty is the choice", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "settings.json", `{"trafilatura": ""}`)
		cfg := load(t, dir, t.TempDir())
		if cfg.Settings.Trafilatura == nil {
			t.Fatalf("trafilatura = nil, want set-empty (the stdlib pass is the choice)")
		}
		if *cfg.Settings.Trafilatura != "" {
			t.Fatalf("trafilatura = %q, want empty", *cfg.Settings.Trafilatura)
		}
	})
	t.Run("absent keys take the embedded", func(t *testing.T) {
		cfg := load(t, t.TempDir(), t.TempDir())
		if cfg.Settings.WebFetchProxy == nil || *cfg.Settings.WebFetchProxy != "http://127.0.0.1:8889" {
			t.Fatalf("absent webFetchProxy = %v, want the embedded proxy", cfg.Settings.WebFetchProxy)
		}
		if cfg.Settings.Trafilatura != nil {
			t.Fatalf("absent trafilatura = %v, want nil (auto)", cfg.Settings.Trafilatura)
		}
	})
}

func TestPluginsEnabledKeyIsRetired(t *testing.T) {
	home := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"plugins": {"enabled": ["x"]}}`)
	if _, err := config.Load(home, t.TempDir()); err == nil || !strings.Contains(err.Error(), "retired") || !strings.Contains(err.Error(), "plugins/disabled/") {
		t.Fatalf("a non-empty enabled list must refuse naming the move, got %v", err)
	}
	write(`{"plugins": {"enabled": [], "max": 3}}`)
	cfg, err := config.Load(home, t.TempDir())
	if err != nil {
		t.Fatalf("an empty enabled list is dropped, got %v", err)
	}
	if cfg.Settings.Plugins.Max != 3 {
		t.Fatalf("max survives beside it: %d", cfg.Settings.Plugins.Max)
	}
}
