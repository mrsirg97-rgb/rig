package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/config"
)

var noWorkerAllow = []string{"bash", "read", "write", "edit", "ls", "find", "grep", "todo", "rem", "python", "web_search", "web_fetch", "diff", "plugin", "plugins", "sessions"}
var fleetAllow = append(append(append([]string{}, noWorkerAllow...), "scheduler"), "delegate")

func TestWorkersAbsentIsNoWorkers(t *testing.T) {
	cfg := load(t, t.TempDir(), t.TempDir())
	if cfg.Workers != nil {
		t.Fatalf("Workers = %+v, want nil with no workers.json", cfg.Workers)
	}
	if !reflect.DeepEqual(cfg.Settings.Allow, noWorkerAllow) {
		t.Fatalf("allow = %v, want the 16 non-worker natives (no fleet, no worker tools)", cfg.Settings.Allow)
	}
}

func TestWorkersFileNamesTheFleet(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "workers.json", `{"model": "local"}`)
	cfg := load(t, dir, t.TempDir())
	want := &config.Workers{Model: "local", Slots: 1}
	if !reflect.DeepEqual(cfg.Workers, want) {
		t.Fatalf("Workers = %+v, want %+v (slots defaults to 1)", cfg.Workers, want)
	}
	if !reflect.DeepEqual(cfg.Settings.Allow, fleetAllow) {
		t.Fatalf("allow = %v, want the default grown by the two worker tools", cfg.Settings.Allow)
	}
}

func TestWorkersSlotsIsKept(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "workers.json", `{"model": "local", "slots": 2}`)
	cfg := load(t, dir, t.TempDir())
	if cfg.Workers == nil || cfg.Workers.Slots != 2 {
		t.Fatalf("Workers = %+v, want slots 2 (the file's)", cfg.Workers)
	}
}

func TestWorkersModelIsRequired(t *testing.T) {
	cases := []struct {
		content string
		want    string
	}{
		{`{}`, `"model" is required`},
		{`{"slots": 1}`, `"model" is required`},
		{`{"model": ""}`, `model: expected a non-empty string, got the empty string`},
	}
	for _, c := range cases {
		dir := t.TempDir()
		p := write(t, dir, "workers.json", c.content)
		err := loadErr(t, dir, t.TempDir())
		if err.Error() != "config: "+p+": "+c.want {
			t.Fatalf("content %s: the voice = %q, want %q", c.content, err.Error(), "config: "+p+": "+c.want)
		}
	}
}

func TestWorkersModelMustResolveInTheTable(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "workers.json", `{"model": "brain"}`)
	err := loadErr(t, dir, t.TempDir())
	want := "config: " + p + `: model "brain": no row in the models table (known: local)`
	if err.Error() != want {
		t.Fatalf("the voice = %q, want %q", err.Error(), want)
	}

	dir = t.TempDir()
	write(t, dir, "models.json", `[{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768, "role": "worker"}]`)
	write(t, dir, "workers.json", `{"model": "brain"}`)
	cfg := load(t, dir, t.TempDir())
	if cfg.Workers == nil || cfg.Workers.Model != "brain" {
		t.Fatalf("Workers = %+v, want the file's brain (the operator's row resolves)", cfg.Workers)
	}
}

func TestWorkersSlotsValidation(t *testing.T) {
	cases := []struct {
		slots string
		want  string
	}{
		{`0`, `slots: expected a positive number, got 0`},
		{`-1`, `slots: expected a positive number, got -1`},
		{`"two"`, `slots: expected an integer, got "two"`},
	}
	for _, c := range cases {
		dir := t.TempDir()
		p := write(t, dir, "workers.json", `{"model": "local", "slots": `+c.slots+`}`)
		err := loadErr(t, dir, t.TempDir())
		if err.Error() != "config: "+p+": "+c.want {
			t.Fatalf("slots %s: the voice = %q, want %q", c.slots, err.Error(), "config: "+p+": "+c.want)
		}
	}
}

func TestWorkersUnknownKeyRefuses(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "workers.json", `{"model": "local", "slot": 1}`)
	err := loadErr(t, dir, t.TempDir())
	if err.Error() != `config: `+p+`: unknown key "slot" (known: model, slots)` {
		t.Fatalf("the voice = %q", err.Error())
	}
}

func TestWorkersAllowGrowsOnlyOverTheDefault(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "workers.json", `{"model": "local"}`)
	write(t, dir, "settings.json", `{"allow": ["bash", "read"]}`)
	cfg := load(t, dir, t.TempDir())
	want := []string{"bash", "read"}
	if !reflect.DeepEqual(cfg.Settings.Allow, want) {
		t.Fatalf("allow = %v, want the operator's list as written (no worker tools appended over an operator allow)", cfg.Settings.Allow)
	}
	if cfg.Workers == nil {
		t.Fatal("the fleet must still load beside the operator's allow")
	}
}

func TestDefaultJobModelMigratesOnceIntoWorkersJSON(t *testing.T) {
	dir := t.TempDir()
	sp := write(t, dir, "settings.json", `{"defaultJobModel": "local"}`)
	cfg := load(t, dir, t.TempDir())
	wp := filepath.Join(dir, "workers.json")
	if cfg.Workers == nil || cfg.Workers.Model != "local" || cfg.Workers.Slots != 1 {
		t.Fatalf("the legacy key must mint the fleet: %+v", cfg.Workers)
	}
	b, err := os.ReadFile(wp)
	if err != nil || string(b) != "{\"model\": \"local\"}\n" {
		t.Fatalf("workers.json minted = %q (%v)", b, err)
	}
	want := "config: " + sp + ": defaultJobModel moved to workers.json — minted " + wp + " with model \"local\"; delete the key"
	if cfg.Notice != want {
		t.Fatalf("notice = %q, want %q", cfg.Notice, want)
	}
	again := load(t, dir, t.TempDir())
	if again.Notice != "config: "+sp+": defaultJobModel is ignored (workers.json names the fleet); delete the key" {
		t.Fatalf("the second start ignores the key with a notice, got %q", again.Notice)
	}
	if b2, _ := os.ReadFile(wp); string(b2) != string(b) {
		t.Fatal("the mint must happen once")
	}
}

func TestDefaultJobModelDisagreeingWithTheFleetRefuses(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "models.json", `[{"id": "other", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384}]`)
	write(t, dir, "workers.json", `{"model": "other"}`)
	sp := write(t, dir, "settings.json", `{"defaultJobModel": "local"}`)
	err := loadErr(t, dir, t.TempDir())
	want := "config: " + sp + ": defaultJobModel \"local\" disagrees with workers.json's model \"other\"; delete the key"
	if err.Error() != want {
		t.Fatalf("two truths must refuse: %q, want %q", err.Error(), want)
	}
}

func TestDefaultJobModelUnknownToTheTableRefuses(t *testing.T) {
	dir := t.TempDir()
	sp := write(t, dir, "settings.json", `{"defaultJobModel": "ghost"}`)
	err := loadErr(t, dir, t.TempDir())
	if !strings.HasPrefix(err.Error(), "config: "+sp+": defaultJobModel \"ghost\": no row in the models table") {
		t.Fatalf("a legacy model the table lacks must refuse naming it: %q", err.Error())
	}
	if _, statErr := os.Stat(filepath.Join(dir, "workers.json")); statErr == nil {
		t.Fatal("nothing minted on a refusal")
	}
}

func TestEmptyDefaultJobModelIsANotice(t *testing.T) {
	dir := t.TempDir()
	sp := write(t, dir, "settings.json", `{"defaultJobModel": ""}`)
	cfg := load(t, dir, t.TempDir())
	if cfg.Workers != nil {
		t.Fatal("an empty legacy key mints nothing")
	}
	if cfg.Notice != "config: "+sp+": defaultJobModel is cut (the fleet is workers.json); delete the key" {
		t.Fatalf("notice = %q", cfg.Notice)
	}
}

func TestDefaultJobModelStaysInTheKnownList(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "settings.json", `{"allowd": ["bash"]}`)
	err := loadErr(t, dir, t.TempDir())
	want := `config: ` + p + `: unknown key "allowd" (known: allow, approve, baseUrl, defaultJobModel, model, plugins, python, resultCap, retries, rounds, sandbox, sandboxBinds, searxngUrl, swapUrl, system, theme, trafilatura, webFetchProxy)`
	if err.Error() != want {
		t.Fatalf("the cut key must stay in the known list so its cut's voice, not the unknown-key voice, fires: %q", err.Error())
	}
}
