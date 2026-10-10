package config_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/config"
)

func TestThemeRawIsTheFileBytes(t *testing.T) {
	dir := t.TempDir()
	doc := []byte(`{"palette":{"bg":"#0a0a0a","fg":"#e5e5e5"},"font":"system"}
`)
	write(t, dir, "theme.json", string(doc))
	cfg := load(t, dir, t.TempDir())
	if !bytes.Equal(cfg.Theme, doc) {
		t.Fatalf("Theme = %s, want the file's bytes as written %s", cfg.Theme, doc)
	}
}

func TestThemeMalformedRefuses(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "theme.json", `{"a":1 x}`)
	err := loadErr(t, dir, t.TempDir())
	want := "config: " + p + ": invalid character 'x' after object key:value pair"
	if err.Error() != want {
		t.Fatalf("the voice = %q, want %q", err, want)
	}
}

func TestSetThemeWritesTheKeyAndPreservesTheRest(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "settings.json", `{"model": "local", "retries": 2}`)
	if err := config.SetTheme(dir, "cool"); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}
	cfg := load(t, dir, t.TempDir())
	if cfg.Settings.Theme != "cool" {
		t.Fatalf("Theme = %q, want cool", cfg.Settings.Theme)
	}
	if cfg.Settings.Model != "local" || cfg.Settings.Retries != 2 {
		t.Fatalf("the file's other keys must survive the write, got %+v", cfg.Settings)
	}
}

func TestSetThemeCreatesTheAbsentFile(t *testing.T) {
	dir := t.TempDir()
	if err := config.SetTheme(dir, "warm"); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}
	cfg := load(t, dir, t.TempDir())
	if cfg.Settings.Theme != "warm" {
		t.Fatalf("Theme = %q, want warm", cfg.Settings.Theme)
	}
}

func TestSetThemeOnAMalformedFileRefuses(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "settings.json", `{ model }`)
	err := config.SetTheme(dir, "cool")
	if err == nil || err.Error() != "config: "+p+": invalid character 'm' looking for beginning of object key string" {
		t.Fatalf("the malformed file = %v, want the loud config voice", err)
	}
}

func TestSetThemeLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	if err := config.SetTheme(dir, "cool"); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("the temp write leaked %s", e.Name())
		}
	}
}
