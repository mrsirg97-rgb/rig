package config_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/models"
)

func TestModelsMalformedNamesFileRowAndField(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"not an array", `{"id": "x"}`, `expected a JSON array of model rows`},
		{"row not an object", `[1]`, `row 1: expected a JSON object`},
		{"missing id", `[{"window": 100, "maxTokens": 1, "reserve": 1, "keepRecent": 1}]`, `row 1: "id" is required`},
		{"duplicate id", `[{"id": "local", "window": 100, "maxTokens": 1, "reserve": 1, "keepRecent": 1}, {"id": "local", "window": 200, "maxTokens": 1, "reserve": 1, "keepRecent": 1}]`, `row 2: duplicate id "local"`},
		{"unknown role", `[{"id": "x", "window": 100, "maxTokens": 1, "reserve": 1, "keepRecent": 1, "role": "boss"}]`, `row 1: role: "boss" (allowed: interactive, worker)`},
		{"bad int", `[{"id": "x", "window": "big", "maxTokens": 1, "reserve": 1, "keepRecent": 1}]`, `row 1: window: expected an integer, got "big"`},
		{"unknown row key", `[{"id": "x", "window": 100, "maxTokens": 1, "reserve": 1, "keepRecent": 1, "winodw": 1}]`, `row 1: unknown key "winodw" (known: apiKey, baseUrl, cacheControl, cacheReadPrice, cacheWritePrice, concurrency, effort, efforts, id, inputPrice, keepRecent, maxTokens, outputPrice, provider, providerPin, reasoning, remote, reserve, retries, role, thinkingBudget, vision, window)`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := write(t, dir, "models.json", c.content)
			err := loadErr(t, dir, t.TempDir())
			if err.Error() != "config: "+p+": "+c.want {
				t.Fatalf("the voice = %q, want %q", err, "config: "+p+": "+c.want)
			}
		})
	}
}

func TestEmbeddedModelsTableIsEmpty(t *testing.T) {
	cfg := load(t, t.TempDir(), t.TempDir())
	if got := cfg.Models.Known(); len(got) != 0 {
		t.Fatalf("a fresh install lists %v: the table is the operator's file, nothing else", got)
	}
}

func TestModelsFileRowsAreTheTableVerbatim(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "models.json", `[{"id": "local", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384, "role": "interactive", "efforts": ["low", "medium", "xhigh"]}, {"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768}]`)
	cfg := load(t, dir, t.TempDir())
	if got := cfg.Models.Known(); !reflect.DeepEqual(got, []string{"brain", "local"}) {
		t.Fatalf("known = %v, want the two rows the file wrote, in id order", got)
	}
	m, ok := cfg.Models.Get("local")
	if !ok {
		t.Fatal("the file's row is gone")
	}
	want := models.Model{ID: "local", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleInteractive, Efforts: []string{"low", "medium", "xhigh"}}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("row = %+v, want the file's row verbatim (+%+v)", m, want)
	}
	brain, ok := cfg.Models.Get("brain")
	if !ok {
		t.Fatal("the file's second row is gone")
	}
	if brain.Role != models.RoleInteractive || brain.Effort != "" {
		t.Fatalf("row = %+v, want the defaults role interactive and effort empty (the policy's medium)", brain)
	}
}

func TestModelsFileRowMissingItsNumbersRefusesNamingThem(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "models.json", `[{"id": "brain", "window": 262144}]`)
	err := loadErr(t, dir, t.TempDir())
	if err.Error() != "config: "+p+": row 1: \"maxTokens\" is required" {
		t.Fatalf("the voice = %q, want the missing field named", err)
	}
}

func TestModelsMergeViolationRefuses(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "models.json", `[{"id": "local", "window": 65536, "maxTokens": 8192, "reserve": 81920, "keepRecent": 16384}]`)
	err := loadErr(t, dir, t.TempDir())
	want := "config: " + p + ": local: Reserve 81920 must be in [0, Window 65536): as large as the window, the trigger fires at every estimate (the pi shape)"
	if err.Error() != want {
		t.Fatalf("the voice = %q, want %q", err, want)
	}
}

func TestModelsVisionKeyDefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "models.json", `[{"id": "local", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384}]`)
	m, ok := load(t, dir, t.TempDir()).Models.Get("local")
	if !ok {
		t.Fatal("the file's row is gone")
	}
	if m.Vision {
		t.Fatal("the file sets vision for nobody unless it says so")
	}
}

func TestModelsVisionKeySetsTheRow(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "models.json", `[{"id": "local", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384, "vision": true}]`)
	m, ok := load(t, dir, t.TempDir()).Models.Get("local")
	if !ok || !m.Vision {
		t.Fatalf("vision = %+v, want the row to carry it", m)
	}
}

func TestModelsVisionKeyIsPresenceAware(t *testing.T) {
	home := t.TempDir()
	write(t, home, "models.json", `[{"id": "visionary", "window": 32768, "maxTokens": 4096, "reserve": 4096, "keepRecent": 8192, "vision": true}, {"id": "plain", "window": 32768, "maxTokens": 4096, "reserve": 4096, "keepRecent": 8192, "vision": false}]`)
	cfg := load(t, home, t.TempDir())
	if m, _ := cfg.Models.Get("visionary"); !m.Vision {
		t.Fatal("a row carries the flag")
	}
	if m, _ := cfg.Models.Get("plain"); m.Vision {
		t.Fatal("an explicit false stands")
	}
}

func TestModelsVisionKeyRefusesANonBoolean(t *testing.T) {
	cases := map[string]string{
		"a string":  `[{"id": "x", "window": 100, "maxTokens": 1, "reserve": 1, "keepRecent": 1, "vision": "yes"}]`,
		"a number":  `[{"id": "x", "window": 100, "maxTokens": 1, "reserve": 1, "keepRecent": 1, "vision": 1}]`,
		"null":      `[{"id": "x", "window": 100, "maxTokens": 1, "reserve": 1, "keepRecent": 1, "vision": null}]`,
		"an object": `[{"id": "x", "window": 100, "maxTokens": 1, "reserve": 1, "keepRecent": 1, "vision": {}}]`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := write(t, dir, "models.json", content)
			err := loadErr(t, dir, t.TempDir())
			if !strings.Contains(err.Error(), "vision") {
				t.Fatalf("the refusal names the field: %v", err)
			}
			if !strings.Contains(err.Error(), p) {
				t.Fatalf("the refusal names the file: %v", err)
			}
		})
	}
}

func TestModelsHostedRowKeys(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "models.json", `[{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "provider": "openrouter", "baseUrl": "https://openrouter.ai/api/v1", "apiKey": "sk-test", "concurrency": 4, "reasoning": "reasoning", "providerPin": "Together", "cacheControl": true}]`)
	cfg := load(t, dir, t.TempDir())
	m, ok := cfg.Models.Get("brain")
	if !ok {
		t.Fatalf("the hosted row must be added")
	}
	if !m.Remote || m.Provider != "openrouter" || m.BaseURL != "https://openrouter.ai/api/v1" || m.APIKey != "sk-test" {
		t.Fatalf("row run site = %+v", m)
	}
	if m.Reasoning != "reasoning" || len(m.ProviderPin) != 1 || m.ProviderPin[0] != "Together" || !m.CacheControl || m.Retries != 3 {
		t.Fatalf("hosted fields = %+v, want reasoning reasoning pin [Together] cacheControl retries 3 (the remote default)", m)
	}
	want := "config: " + filepath.Join(dir, "models.json") + ": concurrency retired: the fleet is the resident model"
	if len(cfg.Notices) != 1 || cfg.Notices[0] != want {
		t.Fatalf("notices = %v, want [%q] (the concurrency key is read, ignored, named)", cfg.Notices, want)
	}
}

func TestModelsHostedRowInvariantsRefuse(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "models.json", `[{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "remote": true}]`)
	err := loadErr(t, dir, t.TempDir())
	if err.Error() != "config: "+p+": brain: a remote row needs a baseUrl (the endpoint it runs against)" {
		t.Fatalf("the voice = %q, want the missing baseUrl named", err)
	}

	dir2 := t.TempDir()
	p2 := write(t, dir2, "models.json", `[{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "provider": "deepseek", "baseUrl": "https://api.deepseek.com", "cacheControl": true}]`)
	err = loadErr(t, dir2, t.TempDir())
	if err.Error() != "config: "+p2+": brain: cacheControl needs the openrouter or anthropic provider (provider: \"deepseek\")" {
		t.Fatalf("the voice = %q, want the cacheControl refusal", err)
	}

	dir3 := t.TempDir()
	p3 := write(t, dir3, "models.json", `[{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "provider": "deepseek", "baseUrl": "https://api.deepseek.com", "providerPin": ["X"]}]`)
	err = loadErr(t, dir3, t.TempDir())
	if err.Error() != "config: "+p3+": brain: providerPin is openrouter-only (provider: \"deepseek\")" {
		t.Fatalf("the voice = %q, want the providerPin refusal", err)
	}
}
