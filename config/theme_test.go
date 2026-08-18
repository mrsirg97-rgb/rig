package config_test

import (
	"bytes"
	"testing"
)

// --- the named cases (SPEC_CONFIG, testing) ---

// TestThemeAbsentNil (SPEC_CONFIG 7): absent is silent — Theme nil.
func TestThemeAbsentNil(t *testing.T) {
	cfg := load(t, t.TempDir(), t.TempDir())
	if cfg.Theme != nil {
		t.Fatalf("Theme = %s, want nil when absent", cfg.Theme)
	}
}

// TestThemeRawIsTheFileBytes (SPEC_CONFIG 7): the loader reads the
// document verbatim as written — 10 (SPEC_TUI) owns the schema and
// decodes this raw value.
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

// TestThemeMalformedRefuses (SPEC_CONFIG 3, 7): present but not one
// well-formed JSON value — the decoder's reason, the path named.
func TestThemeMalformedRefuses(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "theme.json", `{"a":1 x}`)
	err := loadErr(t, dir, t.TempDir())
	want := "config: " + p + ": invalid character 'x' after object key:value pair"
	if err.Error() != want {
		t.Fatalf("the voice = %q, want %q", err, want)
	}
}
