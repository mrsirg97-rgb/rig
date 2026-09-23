package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/models"
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
		{"unknown row key", `[{"id": "x", "window": 100, "maxTokens": 1, "reserve": 1, "keepRecent": 1, "winodw": 1}]`, `row 1: unknown key "winodw" (known: apiKey, baseUrl, cacheControl, concurrency, effort, efforts, id, keepRecent, maxTokens, provider, providerPin, reasoning, remote, reserve, retries, role, vision, window)`},
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

func TestModelsMergesOverEmbeddedRowByRow(t *testing.T) {
	t.Run("overlay keeps the unset fields", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "models.json", `[{"id": "local", "window": 32768}]`)
		cfg := load(t, dir, t.TempDir())
		m, ok := cfg.Models.Get("local")
		if !ok {
			t.Fatalf("the merged table lost the embedded row local")
		}
		want := models.Model{ID: "local", Window: 32768, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleInteractive, Efforts: []string{"low", "medium", "xhigh"}}
		if !reflect.DeepEqual(m, want) {
			t.Fatalf("overlay = %+v, want the user's window over the embedded fields (+%+v)", m, want)
		}
	})
	t.Run("new row added with its defaults", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "models.json", `[{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768}]`)
		cfg := load(t, dir, t.TempDir())
		m, ok := cfg.Models.Get("brain")
		if !ok {
			t.Fatalf("the new row must be added")
		}
		if m.Role != models.RoleInteractive {
			t.Fatalf("a new row's role = %q, want the default interactive", m.Role)
		}
		if m.Effort != "" {
			t.Fatalf("a new row's effort = %q, want the default (the policy's medium)", m.Effort)
		}
		if got := len(cfg.Models.Known()); got != 2 {
			t.Fatalf("merged table = %d rows, want the embedded local plus the new one (%v)", got, cfg.Models.Known())
		}
	})
	t.Run("role and effort are the file's when set", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "models.json", `[{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "role": "worker", "effort": "low"}]`)
		cfg := load(t, dir, t.TempDir())
		m, ok := cfg.Models.Get("brain")
		if !ok {
			t.Fatalf("the new row must be added")
		}
		if m.Role != models.RoleWorker || m.Effort != "low" {
			t.Fatalf("row = %+v, want the file's role worker and effort low", m)
		}
	})
	t.Run("unlisted embedded row kept", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "models.json", `[{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768}]`)
		cfg := load(t, dir, t.TempDir())
		m, ok := cfg.Models.Get("local")
		if !ok {
			t.Fatalf("the unlisted embedded row must be kept (the file is an overlay, not a replacement)")
		}
		if m.Window != 65536 || m.MaxTokens != 8192 || m.Reserve != 8192 || m.KeepRecent != 16384 {
			t.Fatalf("unlisted row = %+v, want the embedded values untouched", m)
		}
	})
	t.Run("a new row's missing number refuses naming it", func(t *testing.T) {
		dir := t.TempDir()
		p := write(t, dir, "models.json", `[{"id": "brain", "window": 262144}]`)
		err := loadErr(t, dir, t.TempDir())
		if err.Error() != "config: "+p+": row 1: \"maxTokens\" is required" {
			t.Fatalf("the voice = %q, want the missing field named", err)
		}
	})
}

func TestModelsMergeViolationRefuses(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "models.json", `[{"id": "local", "reserve": 81920}]`)
	err := loadErr(t, dir, t.TempDir())
	want := "config: " + p + ": local: Reserve 81920 must be in [0, Window 65536): as large as the window, the trigger fires at every estimate (the pi shape)"
	if err.Error() != want {
		t.Fatalf("the voice = %q, want %q", err, want)
	}
}

func TestModelsVisionKeyDefaultsFalse(t *testing.T) {
	cfg := load(t, t.TempDir(), t.TempDir())
	m, ok := cfg.Models.Get("local")
	if !ok {
		t.Fatal("the embedded row is gone")
	}
	if m.Vision {
		t.Fatal("the embedded table sets vision for nobody: the operator's file decides")
	}
}

func TestModelsVisionKeySetsAndOverlays(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "models.json", `[{"id": "local", "vision": true}]`)
	m, ok := load(t, dir, t.TempDir()).Models.Get("local")
	if !ok || !m.Vision {
		t.Fatalf("vision = %+v, want the row to carry it", m)
	}
	if m.Window != 65536 {
		t.Fatalf("the overlay keeps the unset fields: %+v", m)
	}
}

func TestModelsVisionKeyIsPresenceAware(t *testing.T) {
	home := t.TempDir()
	write(t, home, "models.json", `[{"id": "visionary", "window": 32768, "maxTokens": 4096, "reserve": 4096, "keepRecent": 8192, "vision": true}, {"id": "plain", "window": 32768, "maxTokens": 4096, "reserve": 4096, "keepRecent": 8192, "vision": false}]`)
	cfg := load(t, home, t.TempDir())
	if m, _ := cfg.Models.Get("visionary"); !m.Vision {
		t.Fatal("a new row carries the flag")
	}
	if m, _ := cfg.Models.Get("plain"); m.Vision {
		t.Fatal("an explicit false stands")
	}
	if m, _ := cfg.Models.Get("local"); m.Vision {
		t.Fatal("an unlisted row keeps the embedded default")
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
	if m.Concurrency != 4 || m.Reasoning != "reasoning" || len(m.ProviderPin) != 1 || m.ProviderPin[0] != "Together" || !m.CacheControl || m.Retries != 3 {
		t.Fatalf("hosted fields = %+v, want concurrency 4 reasoning reasoning pin [Together] cacheControl retries 3 (the remote default)", m)
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
	if err.Error() != "config: "+p2+": brain: providerPin and cacheControl are openrouter-only (provider: \"deepseek\")" {
		t.Fatalf("the voice = %q, want the openrouter-only refusal", err)
	}
}
