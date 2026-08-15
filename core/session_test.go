package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mrsirg97-rgb/looper/core"
)

// The session must survive a JSON round trip byte-for-byte in meaning:
// transcript intact, file provenance intact, order intact.
func TestSessionRoundTripsThroughJSON(t *testing.T) {
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: "hello"})
	s.Append(core.Message{
		Role:      core.RoleAssistant,
		ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{"command":"ls"}`)}},
	})
	s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: "clean"})
	s.Files["/tmp/x"] = core.FileState{Hash: "ab", Mtime: 42}

	path := filepath.Join(t.TempDir(), "session.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(back.Messages) != len(s.Messages) {
		t.Fatalf("transcript lost in round trip: %d messages, want %d", len(back.Messages), len(s.Messages))
	}
	for i, want := range s.Messages {
		if got := back.Messages[i]; !reflect.DeepEqual(got, want) {
			t.Fatalf("message %d mutated in round trip:\n got: %+v\nwant: %+v", i, got, want)
		}
	}
	if got := back.Files["/tmp/x"]; got != s.Files["/tmp/x"] {
		t.Fatalf("file provenance mutated in round trip: %+v", got)
	}
}

// A saved session without provenance must load with an empty, usable Files
// map, not nil.
func TestSessionLoadNormalizesMissingProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: "hi"})
	if err := s.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	// hand-edit the JSON away to drop Files entirely
	if err := rewriteNoFiles(path); err != nil {
		t.Fatal(err)
	}
	back, err := core.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if back.Files == nil {
		t.Fatal("Files must load as an empty map, not nil")
	}
}

// rewriteNoFiles drops the Files key from a saved session's JSON, to
// exercise the missing-provenance load path.
func rewriteNoFiles(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	delete(doc, "Files")
	out, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// Day-one context threading: the session rides ctx into the exec chain and
// back, keyed to identity.
func TestSessionThreadingRoundTrips(t *testing.T) {
	ctx := context.Background()
	if _, ok := core.SessionFrom(ctx); ok {
		t.Fatal("bare ctx must carry no session")
	}
	s := core.NewSession()
	ctx = core.WithSession(ctx, s)
	back, ok := core.SessionFrom(ctx)
	if !ok || back != s {
		t.Fatal("session must thread into ctx and back, by identity")
	}
}
