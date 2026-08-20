package models_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/models"
)

// legalRow is the worker profile from the spec (decision 2).
var legal = models.Model{ID: "local", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleInteractive}

// table is the 0.2.0 rows (SPEC_CONFIG 4: the table leaves code for the
// embedded config/models.json; the test harnesses construct the same rows).
func table() models.Table {
	t, err := models.New(
		models.Model{ID: "local", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleInteractive},
		models.Model{ID: "qwen3.8-workers", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker},
	)
	if err != nil {
		panic("models: table: " + err.Error())
	}
	return t
}

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

// The role vocabulary (SPEC_CONFIG 4): the two values pass, anything
// else refuses naming the allowed set. The default is the caller's (the
// merge and the synthesis path set it); Check refuses the unset field.
func TestCheckRoleVocabulary(t *testing.T) {
	for _, role := range []string{models.RoleInteractive, models.RoleWorker} {
		m := legal
		m.Role = role
		if err := m.Check(); err != nil {
			t.Fatalf("role %q must pass: %v", role, err)
		}
	}
	for _, role := range []string{"", "boss"} {
		m := legal
		m.Role = role
		err := m.Check()
		if err == nil {
			t.Fatalf("role %q must refuse", role)
		}
		if !strings.Contains(err.Error(), "interactive, worker") {
			t.Fatalf("the refusal must name the allowed set: %v", err)
		}
		if role != "" && !strings.Contains(err.Error(), "boss") {
			t.Fatalf("the refusal must name the given value: %v", err)
		}
	}
}

// Resolve: a known row comes back as-is when the env sets nothing
// (SPEC_COMPACT 2).
func TestResolveReturnsTheKnownRow(t *testing.T) {
	m, err := models.Resolve(table(), "local", func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(m, legal) {
		t.Fatalf("Resolve = %+v, want the table row %+v", m, legal)
	}
}

// Resolve: for the active id, the RIG_MODEL_* env overlays the row's
// fields — set beats the row, per field (SPEC_CONFIG 4): the precedence
// chain applied to the row's fields. 0.2.0 consulted the env only for
// ids the table did not know; this makes the env win for a known id too.
func TestResolveEnvOverlaysTheActiveRow(t *testing.T) {
	env := map[string]string{"RIG_MODEL_WINDOW": "32768"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	m, err := models.Resolve(table(), "local", lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := legal
	want.Window = 32768
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("Resolve = %+v, want the row with the env's window only (+%d)", m, want.Window-legal.Window)
	}

	// all four fields: each set env beats its field
	env = map[string]string{
		"RIG_MODEL_WINDOW": "40000", "RIG_MODEL_MAX_TOKENS": "10000",
		"RIG_MODEL_RESERVE": "5000", "RIG_MODEL_KEEP_RECENT": "10000",
	}
	lookup = func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	m, err = models.Resolve(table(), "local", lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if m.Window != 40000 || m.MaxTokens != 10000 || m.Reserve != 5000 || m.KeepRecent != 10000 {
		t.Fatalf("Resolve = %+v, want every field the env's", m)
	}

	// an overlay that breaks the invariants is refused, loud
	env = map[string]string{"RIG_MODEL_RESERVE": "999999"}
	lookup = func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if _, err := models.Resolve(table(), "local", lookup); err == nil {
		t.Fatal("an overlay that breaks Reserve < Window must refuse")
	}

	// a malformed number is refused too
	env = map[string]string{"RIG_MODEL_WINDOW": "big"}
	lookup = func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if _, err := models.Resolve(table(), "local", lookup); err == nil {
		t.Fatal("a malformed env number must refuse")
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
	m, err := models.Resolve(table(), "brain", lookup)
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
	m, err = models.Resolve(table(), "brain", lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// the synthesized row carries its role (SPEC_CONFIG 4): the env path
	// has no file to borrow one from, and the default is interactive.
	want := models.Model{ID: "brain", Window: 262144, MaxTokens: 16384, Reserve: 16384, KeepRecent: 32768, Role: models.RoleInteractive}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("row = %+v, want %+v", m, want)
	}
}

// Resolve: the unknown-id synthesis path names the role (SPEC_CONFIG 4,
// named): the env path has no file to borrow one from, and the default
// is interactive; effort is empty — the policy's medium default.
func TestResolveSynthesizedRowCarriesInteractive(t *testing.T) {
	env := map[string]string{"RIG_MODEL_WINDOW": "65536"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	m, err := models.Resolve(table(), "synth", lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if m.Role != models.RoleInteractive || m.Effort != "" {
		t.Fatalf("synthesized row = Role %q Effort %q, want interactive / \"\" (the policy's medium default)", m.Role, m.Effort)
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
			_, err := models.Resolve(table(), "brain", lookup)
			if err == nil {
				t.Fatalf("Resolve = ok, want a loud refusal")
			}
		})
	}
}

// Resolve: an unknown id with no env — the refusal names the id, the
// table's known ids, and the env (decision 2's voice, quoted).
func TestResolveRefusalNamesTheKnownIdsAndEnv(t *testing.T) {
	_, err := models.Resolve(table(), "ghost", func(string) (string, bool) { return "", false })
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
		models.Model{ID: "zeta", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleInteractive},
		models.Model{ID: "alpha", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleInteractive},
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
