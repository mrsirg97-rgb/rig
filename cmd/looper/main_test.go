package main

import (
	"regexp"
	"testing"
)

// The initial version is fixed: anything else is a release decision, not a
// code change.
func TestVersionIsTheInitialRelease(t *testing.T) {
	if Version != "0.1.0" {
		t.Fatalf("Version = %q, want the initial release 0.1.0", Version)
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(Version) {
		t.Fatalf("Version %q must be dotted numeric", Version)
	}
}

// The composition root must wire every seam explicitly: swapping a seam is a
// registration change here and nowhere else.
func TestWireRegistersEverySeam(t *testing.T) {
	k := wire(
		"http://127.0.0.1:8080/v1",
		"local",
		"be terse",
		[]string{"bash", "read", "write", "edit"},
		3,
	)
	if k == nil {
		t.Fatal("wire returned nil")
	}
	if k.Provider == nil || k.Frontend == nil || k.Policy == nil {
		t.Fatal("every required seam must be registered")
	}
	if got := k.SortedToolNames(); len(got) != 7 || got[0] != "bash" || got[6] != "write" {
		t.Fatalf("registered tools = %v, want bash,edit,find,grep,ls,read,write", got)
	}
	if len(k.Middleware) != 2 {
		t.Fatalf("middleware = %d links, want the allow-list and the retry guard", len(k.Middleware))
	}
}
