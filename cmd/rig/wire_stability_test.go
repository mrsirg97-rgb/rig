package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"testing"
)

const wireToolsPrefixGolden = "884a4fc579c47c4ad49c4a14f3f5977db4466b8c6a30cd31557a02adc2f17b78"

func TestWireToolsPrefixGolden(t *testing.T) {
	k := wire(testRoot(nullFrontend{}))
	type spec struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Schema      json.RawMessage `json:"schema"`
	}
	specs := make([]spec, 0, len(k.Tools))
	for _, tool := range k.Tools {
		specs = append(specs, spec{tool.Name(), tool.Description(), tool.Schema()})
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	b, err := json.Marshal(specs)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != wireToolsPrefixGolden {
		t.Fatalf("the wire tools prefix changed: sha256 %s (want %s) — a schema or description change moves the prefix cache; update the golden deliberately", got, wireToolsPrefixGolden)
	}
}

func TestSystemPromptIsByteStableAcrossBuilds(t *testing.T) {
	r := testRoot(nullFrontend{})
	wire(r)
	a := r.buildSystem()
	b := r.buildSystem()
	if a != b {
		t.Fatalf("the system assembly must be byte-stable across builds (the prefix cache depends on it):\n a: %q\n b: %q", a, b)
	}
}
