package tool_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/tool"
)

func TestEveryEntryHasTheShapeAndAParsingSchema(t *testing.T) {
	names := tool.AllNames()
	if len(names) == 0 {
		t.Fatal("the registry is empty")
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Fatalf("%q appears twice", n)
		}
		seen[n] = true
		d := tool.Def(n)
		if d.Name() != n {
			t.Fatalf("Def(%q).Name() = %q", n, d.Name())
		}
		desc := d.Description()
		i, j := strings.Index(desc, " guidelines: "), strings.LastIndex(desc, " reply: ")
		if i <= 0 || j <= i {
			t.Fatalf("%q: the description is not what, guidelines, reply: %q", n, desc)
		}
		var schema map[string]any
		if err := json.Unmarshal(d.Schema(), &schema); err != nil {
			t.Fatalf("%q: schema: %v", n, err)
		}
		if schema["type"] != "object" {
			t.Fatalf("%q: the schema is not an object schema", n)
		}
	}
}

func TestRegistryWordsCarryNoOtherHarnessVoice(t *testing.T) {
	for _, n := range tool.AllNames() {
		d := tool.Def(n)
		text := d.Description() + string(d.Schema())
		for _, bad := range []string{"pi", "pane"} {
			if regexp.MustCompile(`\b` + bad + `\b`).MatchString(text) {
				t.Errorf("%s carries another harness's voice: %q", n, bad)
			}
		}
	}
}

func TestNamesListsOnlyTheEnabledInFileOrder(t *testing.T) {
	all, enabled := tool.AllNames(), tool.Names()
	if len(enabled) > len(all) {
		t.Fatal("more enabled names than entries")
	}
	j := 0
	for _, n := range all {
		if j < len(enabled) && enabled[j] == n {
			if !tool.Def(n).Enabled() {
				t.Fatalf("%q is listed as enabled but is not", n)
			}
			j++
			continue
		}
		if tool.Def(n).Enabled() {
			t.Fatalf("%q is enabled but missing from Names()", n)
		}
	}
	if j != len(enabled) {
		t.Fatalf("Names() carries names outside the file order: %v", enabled)
	}
}

func TestFillReplacesTheSlotEverywhereAndLeavesTheOriginal(t *testing.T) {
	d := tool.Def("scheduler")
	if !strings.Contains(string(d.Schema()), "{default_model}") {
		t.Fatal("scheduler's schema must carry the {default_model} slot; the description speaks of the resident default in words")
	}
	f := tool.Fill(d, "{default_model}", "dsv4")
	if strings.Contains(f.Description(), "{default_model}") || strings.Contains(string(f.Schema()), "{default_model}") {
		t.Fatal("Fill must replace the slot in the description and the schema")
	}
	if !strings.Contains(string(f.Schema()), "(default dsv4 when nothing is)") {
		t.Fatalf("filled schema = %s", string(f.Schema()))
	}
	if !strings.Contains(string(tool.Def("scheduler").Schema()), "{default_model}") {
		t.Fatal("Fill must not change the registry's own copy")
	}
}

func TestSchemaIsACopy(t *testing.T) {
	a := tool.Def("bash").Schema()
	a[0] = 'x'
	if b := tool.Def("bash").Schema(); b[0] != '{' {
		t.Fatal("a caller's write reached the registry")
	}
}

func TestDefPanicsOnAnUnknownName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Def of an unknown name must panic: a missing entry is a programmer error")
		}
	}()
	tool.Def("no-such-tool")
}
