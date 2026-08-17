package models_test

import (
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/models"
)

// legalRow is the worker profile from the spec (decision 2).
var legal = models.Model{ID: "local", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384}

// The row invariants by name (SPEC_COMPACT 2): each refused, the id and
// the field named in the error.
func TestCheckNamesTheRowInvariants(t *testing.T) {
	if err := legal.Check(); err != nil {
		t.Fatalf("a legal row must pass: %v", err)
	}
	cases := []struct {
		name  string
		mut   func(*models.Model)
		field string
	}{
		{"empty id", func(m *models.Model) { m.ID = "" }, "id"},
		{"window zero", func(m *models.Model) { m.Window = 0 }, "Window"},
		{"window negative", func(m *models.Model) { m.Window = -1 }, "Window"},
		{"max tokens zero", func(m *models.Model) { m.MaxTokens = 0 }, "MaxTokens"},
		{"max tokens negative", func(m *models.Model) { m.MaxTokens = -1 }, "MaxTokens"},
		{"pi shape: reserve equals window", func(m *models.Model) { m.Reserve = m.Window }, "Reserve"},
		{"reserve over window", func(m *models.Model) { m.Reserve = m.Window + 1 }, "Reserve"},
		{"reserve negative", func(m *models.Model) { m.Reserve = -1 }, "Reserve"},
		{"keep recent equals usable", func(m *models.Model) { m.KeepRecent = m.Window - m.Reserve }, "KeepRecent"},
		{"keep recent over usable", func(m *models.Model) { m.KeepRecent = m.Window - m.Reserve + 1 }, "KeepRecent"},
		{"keep recent negative", func(m *models.Model) { m.KeepRecent = -1 }, "KeepRecent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := legal
			c.mut(&m)
			err := m.Check()
			if err == nil {
				t.Fatalf("Check() = nil, want a refusal")
			}
			if m.ID != "" && !strings.Contains(err.Error(), m.ID) {
				t.Fatalf("the refusal must name the id %q: %v", m.ID, err)
			}
			if !strings.Contains(err.Error(), c.field) {
				t.Fatalf("the refusal must name the field %s: %v", c.field, err)
			}
		})
	}
}

// Defaults carries the worker profile under rig's default id and the
// scheduler's worker id, with the spec's numbers (decision 2).
func TestDefaultsCarriesTheWorkerRows(t *testing.T) {
	for _, id := range []string{"local", "qwen3.8-workers"} {
		m, ok := models.Defaults.Get(id)
		if !ok {
			t.Fatalf("Defaults has no row for %q", id)
		}
		if m.Window != 65536 || m.MaxTokens != 8192 || m.Reserve != 8192 || m.KeepRecent != 16384 {
			t.Fatalf("row %q = %+v, want Window 65536 MaxTokens 8192 Reserve 8192 KeepRecent 16384", id, m)
		}
	}
}

// Resolve: a known row comes back as-is (SPEC_COMPACT 2).
func TestResolveReturnsTheKnownRow(t *testing.T) {
	m, err := models.Resolve(models.Defaults, "local", func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if m != legal {
		t.Fatalf("Resolve = %+v, want the table row %+v", m, legal)
	}
}

// Resolve: env synthesis — absent fields take the named defaults
// (MaxTokens 8192, Reserve Window/8, KeepRecent Window/4), then validate
// (decision 2). This is how a new model on the swap gets a row without a
// code change.
func TestResolveSynthesizesFromEnv(t *testing.T) {
	env := map[string]string{"RIG_MODEL_WINDOW": "262144"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	// absent fields: the named defaults
	m, err := models.Resolve(models.Defaults, "brain", lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if m.ID != "brain" || m.Window != 262144 {
		t.Fatalf("row = %+v, want the synthesized row for brain at 262144", m)
	}
	if m.MaxTokens != 8192 || m.Reserve != 262144/8 || m.KeepRecent != 262144/4 {
		t.Fatalf("absent fields = MaxTokens %d Reserve %d KeepRecent %d, want the named defaults 8192 %d %d",
			m.MaxTokens, m.Reserve, m.KeepRecent, 262144/8, 262144/4)
	}

	// the spec's brain row, spelled out in full
	env = map[string]string{
		"RIG_MODEL_WINDOW": "262144", "RIG_MODEL_MAX_TOKENS": "16384",
		"RIG_MODEL_RESERVE": "16384", "RIG_MODEL_KEEP_RECENT": "32768",
	}
	lookup = func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	m, err = models.Resolve(models.Defaults, "brain", lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := models.Model{ID: "brain", Window: 262144, MaxTokens: 16384, Reserve: 16384, KeepRecent: 32768}
	if m != want {
		t.Fatalf("row = %+v, want %+v", m, want)
	}
}

// Resolve: an env mistake is loud at start, not a slow death on the first
// turn (decision 2): a synthesized row that violates the invariants is
// refused with the invariants' voice; a malformed number is refused too.
func TestResolveRefusesBadSynthesis(t *testing.T) {
	cases := map[string]map[string]string{
		"reserve over window":   {"RIG_MODEL_WINDOW": "100", "RIG_MODEL_RESERVE": "100"},
		"window zero":           {"RIG_MODEL_WINDOW": "0"},
		"malformed window":      {"RIG_MODEL_WINDOW": "sixty-four"},
		"malformed keep recent": {"RIG_MODEL_WINDOW": "100000", "RIG_MODEL_KEEP_RECENT": "abc"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
			_, err := models.Resolve(models.Defaults, "brain", lookup)
			if err == nil {
				t.Fatalf("Resolve = ok, want a loud refusal")
			}
		})
	}
}

// Resolve: an unknown id with no env — the refusal names the id, the
// table's known ids, and the env (decision 2's voice, quoted).
func TestResolveRefusalNamesTheKnownIdsAndEnv(t *testing.T) {
	_, err := models.Resolve(models.Defaults, "ghost", func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("Resolve = ok, want a refusal")
	}
	msg := err.Error()
	for _, want := range []string{"ghost", "local", "qwen3.8-workers", "RIG_MODEL_WINDOW"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal must name %q: %v", want, err)
		}
	}
}

// Known: stable order for the refusal voice.
func TestKnownIsStable(t *testing.T) {
	tbl, err := models.New(
		models.Model{ID: "zeta", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384},
		models.Model{ID: "alpha", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := tbl.Known(); len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("Known() = %v, want [alpha zeta]", got)
	}
	if _, ok := tbl.Get("ghost"); ok {
		t.Fatal("a missing row must not resolve")
	}
}
