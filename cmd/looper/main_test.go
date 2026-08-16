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

type fakeRem struct{}

func (fakeRem) Name() string { return "rem" }
func (fakeRem) Description() string {
	return "fake rem surface"
}
func (fakeRem) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeRem) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

type fakeSched struct{}

func (fakeSched) Name() string { return "scheduler" }
func (fakeSched) Description() string {
	return "fake scheduler surface"
}
func (fakeSched) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeSched) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

type fakePython struct{}

func (fakePython) Name() string { return "python" }
func (fakePython) Description() string {
	return "fake python kernel surface"
}
func (fakePython) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakePython) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

type fakeWebSearch struct{}

func (fakeWebSearch) Name() string { return "web_search" }
func (fakeWebSearch) Description() string {
	return "fake web_search surface"
}
func (fakeWebSearch) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeWebSearch) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

type fakeWebFetch struct{}

func (fakeWebFetch) Name() string { return "web_fetch" }
func (fakeWebFetch) Description() string {
	return "fake web_fetch surface"
}
func (fakeWebFetch) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeWebFetch) Exec(ctx context.Context, args json.RawMessage) (string, error) {
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
		core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec { return next }),
		fakeTodo{},
		fakeRem{},
		fakeSched{},
		fakePython{},
		fakeWebSearch{},
		fakeWebFetch{},
	)
	if k == nil {
		t.Fatal("wire returned nil")
	}
	if k.Provider == nil || k.Frontend == nil || k.Policy == nil {
		t.Fatal("every required seam must be registered")
	}
	if got := k.SortedToolNames(); len(got) != 13 || got[0] != "bash" || got[5] != "python" || got[7] != "rem" || got[8] != "scheduler" || got[9] != "todo" || got[10] != "web_fetch" || got[11] != "web_search" || got[12] != "write" {
		t.Fatalf("registered tools = %v, want bash,edit,find,grep,ls,python,read,rem,scheduler,todo,web_fetch,web_search,write", got)
	}
	if len(k.Middleware) != 3 {
		t.Fatalf("middleware = %d links, want the allow-list, the bound, and the observation tap", len(k.Middleware))
	}
}
