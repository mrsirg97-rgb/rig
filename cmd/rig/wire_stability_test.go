package main

import (
	"testing"
)

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
