package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"testing"
)

const wireToolsPrefixGolden = "38cdd91e4c0f4e06f3b96f82e4bf5dce62aeeded2edea5d9fbbdb411c216e15f"

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

func TestWireHasNoNamedFilesystemTools(t *testing.T) {
	k := wire(testRoot(nullFrontend{}))
	for _, tool := range k.Tools {
		switch tool.Name() {
		case "ls", "find", "grep":
			t.Fatalf("the wire must not carry %q: bash ls/find/grep is the shell's, read is the observation path", tool.Name())
		case "diff":
			t.Fatalf("the wire must not carry %q: the diff folded into read's diff:true and edit's drift refusal", tool.Name())
		}
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
