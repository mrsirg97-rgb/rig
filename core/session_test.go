package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/core"
)

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

func TestSessionLoadNormalizesMissingProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	s := core.NewSession()
	s.Append(core.Message{Role: core.RoleUser, Content: "hi"})
	if err := s.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

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

func TestSessionIDMintedUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := core.NewSession().ID
		if id == "" {
			t.Fatal("empty session id minted")
		}
		if seen[id] {
			t.Fatalf("duplicate session id: %q", id)
		}
		seen[id] = true
	}
}

func inodeOf(path string) (uint64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Sys().(*syscall.Stat_t).Ino, nil
}

func TestASaveReplacesTheTranscriptAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	v1 := core.NewSession()
	v1.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("one ", 2048)})
	if err := v1.Save(path); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	ino1, err := inodeOf(path)
	if err != nil {
		t.Fatal(err)
	}
	back1, err := core.Load(path)
	if err != nil {
		t.Fatalf("load v1: %v", err)
	}
	if !reflect.DeepEqual(back1.Messages, v1.Messages) {
		t.Fatal("v1 lost in round trip")
	}

	v2 := core.NewSession()
	v2.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("two ", 4096)})
	v2.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("more ", 4096)})
	if err := v2.Save(path); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	ino2, err := inodeOf(path)
	if err != nil {
		t.Fatal(err)
	}
	if ino1 == ino2 {
		t.Fatalf("save v2 rewrote the transcript in place (inode %d twice), want a fresh file", ino1)
	}
	back2, err := core.Load(path)
	if err != nil {
		t.Fatalf("load v2: %v", err)
	}
	if !reflect.DeepEqual(back2.Messages, v2.Messages) {
		t.Fatal("v2 lost in round trip")
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("transcript perms = %o, want 600", got)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("a finished save left a temp file behind: %v", err)
	}

	seed := core.NewSession()
	seed.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("seed ", 65536)})
	if err := seed.Save(path); err != nil {
		t.Fatalf("seed the reader's file: %v", err)
	}
	done := make(chan struct{})
	saw := make(chan error, 1)
	go func() {
		for {
			select {
			case <-done:
				saw <- nil
				return
			default:
			}
			if _, err := core.Load(path); err != nil {
				saw <- fmt.Errorf("a reader inside the save saw a broken transcript: %w", err)
				return
			}
		}
	}()
	var last *core.Session
	for i := 0; i < 300; i++ {
		last = core.NewSession()
		if i%2 == 0 {
			last.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("even ", 131072)})
		} else {
			last.Append(core.Message{Role: core.RoleAssistant, Content: strings.Repeat("odd ", 131072)})
		}
		if err := last.Save(path); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	close(done)
	if err := <-saw; err != nil {
		t.Fatal(err)
	}
	fin, err := core.Load(path)
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	if len(fin.Messages) != len(last.Messages) || fin.Messages[0].Content != last.Messages[0].Content {
		t.Fatal("the final save did not land whole")
	}
}
