package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/config"
)

var fleetAllow = []string{"bash", "read", "write", "edit", "view", "todo", "rem", "python", "web", "decide", "plugin", "sessions", "scheduler", "delegate"}

func TestWorkersFileIsReadIgnoredAndNamedOnce(t *testing.T) {
	dir := t.TempDir()
	wp := write(t, dir, "workers.json", `{"model": "local", "slots": 2, "reviewer": "review"}`)
	cfg := load(t, dir, t.TempDir())
	want := "config: " + wp + ": workers.json retired: the fleet is the resident model"
	if len(cfg.Notices) != 1 || cfg.Notices[0] != want {
		t.Fatalf("notices = %v, want [%q]", cfg.Notices, want)
	}
	if !hasAllow(cfg.Settings.Allow, fleetAllow) {
		t.Fatalf("allow = %v, want the default grown by the two worker tools", cfg.Settings.Allow)
	}
}

func TestWorkersContentIsNeverInterpreted(t *testing.T) {
	for _, content := range []string{
		`{"model": "no-such-row"}`,
		`{"slots": 0}`,
		`{"unknown": true}`,
		`not json at all`,
		`{}`,
	} {
		dir := t.TempDir()
		write(t, dir, "workers.json", content)
		cfg := load(t, dir, t.TempDir())
		if len(cfg.Notices) != 1 || !strings.Contains(cfg.Notices[0], "workers.json retired") {
			t.Fatalf("content %q: notices = %v, want the one retirement line", content, cfg.Notices)
		}
	}
}

func TestWorkersAbsentIsSilent(t *testing.T) {
	cfg := load(t, t.TempDir(), t.TempDir())
	if len(cfg.Notices) != 0 {
		t.Fatalf("notices = %v, want none", cfg.Notices)
	}
	if !hasAllow(cfg.Settings.Allow, fleetAllow) {
		t.Fatalf("allow = %v, want the default grown by the two worker tools", cfg.Settings.Allow)
	}
}

func TestModelsConcurrencyKeyIsReadIgnoredAndNamedOnce(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "models.json", `[{"id": "local", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "role": "worker", "concurrency": 4}]`)
	cfg := load(t, dir, t.TempDir())
	want := "config: " + filepath.Join(dir, "models.json") + ": concurrency retired: the fleet is the resident model"
	if len(cfg.Notices) != 1 || cfg.Notices[0] != want {
		t.Fatalf("notices = %v, want [%q]", cfg.Notices, want)
	}
	if _, ok := cfg.Models.Get("local"); !ok {
		t.Fatal("the row must still load")
	}
}

func TestModelsConcurrencyOverlayEnvIsGone(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "models.json", `[{"id": "local", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "role": "worker"}]`)
	t.Setenv("RIG_MODEL_CONCURRENCY", "4")
	cfg := load(t, dir, t.TempDir())
	if len(cfg.Notices) != 0 {
		t.Fatalf("notices = %v, want none (the env key is gone, not retired)", cfg.Notices)
	}
}

func TestDefaultJobModelLegacyKeyIsNamedAndIgnored(t *testing.T) {
	dir := t.TempDir()
	sp := write(t, dir, "settings.json", `{"defaultJobModel": "brain"}`)
	write(t, dir, "models.json", `[{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "role": "worker"}]`)
	cfg := load(t, dir, t.TempDir())
	want := "config: " + sp + ": defaultJobModel moved to model — the fleet is the resident model; delete the key"
	if len(cfg.Notices) != 1 || cfg.Notices[0] != want {
		t.Fatalf("notices = %v, want [%q]", cfg.Notices, want)
	}
}

func TestDefaultJobModelUnknownToTheTableIsStillOnlyNamed(t *testing.T) {
	dir := t.TempDir()
	sp := write(t, dir, "settings.json", `{"defaultJobModel": "brain"}`)
	cfg := load(t, dir, t.TempDir())
	if len(cfg.Notices) != 1 || !strings.Contains(cfg.Notices[0], sp) {
		t.Fatalf("notices = %v, want the one retirement line", cfg.Notices)
	}
}

func TestDefaultJobModelStaysInTheKnownList(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "settings.json", `{"allowd": ["bash"]}`)
	err := loadErr(t, dir, t.TempDir())
	want := `config: ` + p + `: unknown key "allowd" (known: allow, approve, baseUrl, decisionUrl, defaultJobModel, model, plugins, python, resultCap, retries, rounds, sandbox, sandboxBinds, searxngUrl, swapUrl, system, theme, trafilatura, updateKey, webFetchProxy, workers)`
	if err.Error() != want {
		t.Fatalf("the cut key must stay in the known list so its cut's voice, not the unknown-key voice, fires: %q", err.Error())
	}
}

func TestWorkersRetirementJoinsTheOtherNotices(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "models.json", `[{"id": "local", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "role": "worker", "concurrency": 2}]`)
	write(t, dir, "workers.json", `{"model": "local"}`)
	cfg := load(t, dir, t.TempDir())
	if len(cfg.Notices) != 2 {
		t.Fatalf("notices = %v, want the concurrency and the workers lines", cfg.Notices)
	}
	if !strings.Contains(cfg.Notices[0], "concurrency retired") || !strings.Contains(cfg.Notices[1], "workers.json retired") {
		t.Fatalf("notices = %v, want the models line first, the workers line second", cfg.Notices)
	}
}

func hasAllow(got, want []string) bool {
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

var _ = config.Load

func TestAllowNamingPluginsIsDroppedAndNamedOnce(t *testing.T) {
	dir := t.TempDir()
	sp := write(t, dir, "settings.json", `{"allow": ["bash", "plugins", "plugin"]}`)
	cfg := load(t, dir, t.TempDir())
	want := "config: " + sp + ": allow names plugins, which folded into plugin in 2.8.2; the name is dropped"
	if len(cfg.Notices) != 1 || cfg.Notices[0] != want {
		t.Fatalf("notices = %v, want [%q]", cfg.Notices, want)
	}
	if strings.Join(cfg.Settings.Allow, ",") != "bash,plugin" {
		t.Fatalf("allow = %v, want plugins dropped and plugin kept", cfg.Settings.Allow)
	}
}
