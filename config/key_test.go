package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/config"
)

func writeSettings(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSetKeyWritesTheKeyAndPreservesTheRest(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"model":"tester","rounds":4}`)
	if err := config.SetKey(dir, "webFetchProxy", "http://rig:pw@127.0.0.1:8889"); err != nil {
		t.Fatal(err)
	}
	raw := read(t, filepath.Join(dir, "settings.json"))
	for _, want := range []string{`"model": "tester"`, `"rounds": 4`, `"webFetchProxy": "http://rig:pw@127.0.0.1:8889"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("settings.json = %s, want %s", raw, want)
		}
	}
}

func TestSetKeyRefusesAKeyConfigDoesNotKnow(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"model":"tester"}`)
	err := config.SetKey(dir, "webFetchProxxy", "x")
	if err == nil {
		t.Fatal("want the unknown key refused")
	}
	for _, want := range []string{"webFetchProxxy", "webFetchProxy", "searxngUrl"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %q, want it to name %s and the known key it resembles", err, want)
		}
	}
	if strings.Contains(read(t, filepath.Join(dir, "settings.json")), "webFetchProxxy") {
		t.Fatal("the refused key still reached the file")
	}
}

func TestSetKeyCreatesTheAbsentFileAtZeroSixZero(t *testing.T) {
	dir := t.TempDir()
	if err := config.SetKey(dir, "theme", "cool"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings.json is %o, want 0600 (it can carry credentials)", info.Mode().Perm())
	}
}

func TestSetKeyOnAMalformedFileRefuses(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"model":`)
	if err := config.SetKey(dir, "theme", "cool"); err == nil {
		t.Fatal("want the malformed file refused")
	}
	if !strings.Contains(read(t, filepath.Join(dir, "settings.json")), `{"model":`) {
		t.Fatal("a refused write must leave the operator's file alone")
	}
}

func TestSetKeyOnANullDocumentWritesTheKey(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, "null\n")
	if err := config.SetKey(dir, "theme", "dark"); err != nil {
		t.Fatal(err)
	}
	raw := read(t, filepath.Join(dir, "settings.json"))
	if !strings.Contains(raw, `"theme": "dark"`) {
		t.Fatalf("settings.json = %s, want the key written over the null", raw)
	}
}

func TestSetKeyRefusesAnEmptyDir(t *testing.T) {
	if err := config.SetKey("", "theme", "cool"); err == nil || !strings.Contains(err.Error(), "no config dir") {
		t.Fatalf("err = %v, want the missing dir named", err)
	}
}

func TestReadKeyFindsAKnownKeyAndMissesQuietly(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"webFetchProxy":"http://rig:pw@127.0.0.1:8889","theme":""}`)
	v, ok, err := config.ReadKey(dir, "webFetchProxy")
	if err != nil || !ok || v != "http://rig:pw@127.0.0.1:8889" {
		t.Fatalf("ReadKey = %q %v %v, want the stored URL", v, ok, err)
	}
	_, ok, err = config.ReadKey(dir, "theme")
	if err != nil || !ok {
		t.Fatalf("an empty string is still a set key: %v %v", ok, err)
	}
	_, ok, err = config.ReadKey(dir, "allow")
	if err != nil || ok {
		t.Fatalf("ReadKey(allow) = %v %v, want not-set with no error", ok, err)
	}
	if _, _, err := config.ReadKey(dir, "nonsense"); err == nil || !strings.Contains(err.Error(), "nonsense") {
		t.Fatalf("err = %v, want the unknown key named", err)
	}
	if _, _, err := config.ReadKey("", "theme"); err == nil || !strings.Contains(err.Error(), "no config dir") {
		t.Fatalf("err = %v, want the missing dir named", err)
	}
}

func TestReadKeyOnAMalformedFileRefuses(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"theme":`)
	if _, _, err := config.ReadKey(dir, "theme"); err == nil {
		t.Fatal("want the malformed file refused")
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestSetThemeGoesThroughSetKey(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"model":"tester"}`)
	if err := config.SetTheme(dir, "cool"); err != nil {
		t.Fatal(err)
	}
	raw := read(t, filepath.Join(dir, "settings.json"))
	if !strings.Contains(raw, `"theme": "cool"`) || !strings.Contains(raw, `"model": "tester"`) {
		t.Fatalf("settings.json = %s, want both keys", raw)
	}
	v, ok, err := config.ReadKey(dir, "theme")
	if err != nil || !ok || v != "cool" {
		t.Fatalf("ReadKey(theme) = %q %v %v", v, ok, err)
	}
}
