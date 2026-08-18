package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mrsirg97-rgb/rig/config"
)

// --- helpers (shared by the settings and models-file cases) ---

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

// --- the named cases (SPEC_CONFIG, testing) ---

// TestLoadAbsentFilesIsSilent (SPEC_CONFIG 3): no dir, no files, no
// AGENTS.md — the embedded values, Theme nil, Agents "".
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

// TestLoadEmptyDirIsSilent (SPEC_CONFIG 9): the directory exists, no
// files — the same result. The directory's presence is not an event.
func TestLoadEmptyDirIsSilent(t *testing.T) {
	empty := t.TempDir()
	absent := t.TempDir()
	a := load(t, empty, t.TempDir())
	b := load(t, absent, t.TempDir())
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("an empty config dir must be silent (the same result as absent):\nempty: %+v\nabsent: %+v", a, b)
	}
}

// TestEmbeddedDefaultsAreTheV020Values (SPEC_CONFIG 4, 5): the move is
// exact — the embedded settings equal the 0.2.0 flag defaults key by
// key, and the embedded table equals the 0.2.0 rows row by row.
func TestEmbeddedDefaultsAreTheV020Values(t *testing.T) {
	cfg := load(t, t.TempDir(), t.TempDir())
	s := cfg.Settings
	if s.BaseURL != "http://127.0.0.1:8090/v1" {
		t.Fatalf("baseUrl = %q, want the 0.2.0 flag default", s.BaseURL)
	}
	if s.Model != "local" {
		t.Fatalf("model = %q, want the 0.2.0 flag default", s.Model)
	}
	if s.System != "You are rig, a minimal coding agent. Use the provided tools to inspect, change, and run things in the working directory. Answer in plain text when done." {
		t.Fatalf("system = %q, want the 0.2.0 default system prompt", s.System)
	}
	wantAllow := []string{"bash", "read", "write", "edit", "ls", "find", "grep", "todo", "rem", "scheduler", "python", "web_search", "web_fetch"}
	if !reflect.DeepEqual(s.Allow, wantAllow) {
		t.Fatalf("allow = %v, want the 0.2.0 default list %v", s.Allow, wantAllow)
	}
	if s.Retries != 3 {
		t.Fatalf("retries = %d, want the 0.2.0 default 3", s.Retries)
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
	if s.DefaultJobModel != "qwen3.8-workers" {
		t.Fatalf("defaultJobModel = %q, want the 0.2.0 scheduler default", s.DefaultJobModel)
	}

	// the table: the 0.2.0 rows, exactly — the numbers verbatim, and the
	// roles named (4's example): local is interactive, the scheduler's
	// worker id is worker.
	want := map[string]string{"local": "interactive", "qwen3.8-workers": "worker"}
	for _, id := range []string{"local", "qwen3.8-workers"} {
		m, ok := cfg.Models.Get(id)
		if !ok {
			t.Fatalf("the embedded table has no row for %q", id)
		}
		if m.Window != 65536 || m.MaxTokens != 8192 || m.Reserve != 8192 || m.KeepRecent != 16384 {
			t.Fatalf("row %q = %+v, want the 0.2.0 numbers", id, m)
		}
		if m.Role != want[id] {
			t.Fatalf("row %q role = %q, want %q (4's named roles)", id, m.Role, want[id])
		}
	}
	if got := len(cfg.Models.Known()); got != 2 {
		t.Fatalf("the embedded table = %d rows, want the two 0.2.0 rows (%v)", got, cfg.Models.Known())
	}
}

// TestSettingsMalformedNamesFileAndField (SPEC_CONFIG 3): the voices,
// exactly — the file and the field, the operator's JSON spelling.
func TestSettingsMalformedNamesFileAndField(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"retries type", `{"retries": "three"}`, `retries: expected an integer, got "three"`},
		{"unknown key", `{"allowd": ["bash"]}`, `unknown key "allowd" (known: allow, baseUrl, defaultJobModel, model, python, retries, searxngUrl, swapUrl, system, trafilatura, webFetchProxy)`},
		{"not an object", `[1]`, `expected a JSON object`},
		{"allow element", `{"allow": ["bash", "read", 5]}`, `allow[2]: expected a string, got 5`},
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

// TestSettingsZeroDescendsToEmbedded (SPEC_CONFIG 2): zero = unset at
// the file layer — the value descends to the embedded.
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

// TestSettingsPresenceKeysInFileAreExplicit (SPEC_CONFIG 2, 5): for the
// two presence-aware keys, present at the file layer is set — even
// empty. 0.2.0's documented "set empty = the choice" env semantics,
// extended to the file.
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
