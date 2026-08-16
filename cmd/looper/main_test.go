package main

import (
	"context"
	"encoding/json"
	"io"
	"regexp"
	"testing"

	"github.com/mrsirg97-rgb/looper/core"
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
type nullFrontend struct{}

// fakeTodo stands in for the todo surface so the seam's registration is
// testable without a store file.
type fakeTodo struct{}

func (fakeTodo) Name() string { return "todo" }
func (fakeTodo) Description() string {
	return "fake todo surface"
}
func (fakeTodo) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeTodo) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

func (nullFrontend) Input(ctx context.Context) (string, error) { return "", io.EOF }
func (nullFrontend) Notify(ev core.Event)                      {}

func TestWireRegistersEverySeam(t *testing.T) {
	k := wire(
		"http://127.0.0.1:8080/v1",
		"local",
		"be terse",
		[]string{"bash", "read", "write", "edit"},
		3,
		nullFrontend{},
		func(next core.ToolExec) core.ToolExec { return next },
		fakeTodo{},
	)
	if k == nil {
		t.Fatal("wire returned nil")
	}
	if k.Provider == nil || k.Frontend == nil || k.Policy == nil {
		t.Fatal("every required seam must be registered")
	}
	if got := k.SortedToolNames(); len(got) != 8 || got[0] != "bash" || got[6] != "todo" || got[7] != "write" {
		t.Fatalf("registered tools = %v, want bash,edit,find,grep,ls,read,todo,write", got)
	}
	if len(k.Middleware) != 3 {
		t.Fatalf("middleware = %d links, want the allow-list, the bound, and the observation tap", len(k.Middleware))
	}
}
