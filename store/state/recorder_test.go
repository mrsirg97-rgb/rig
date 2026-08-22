package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
	"github.com/mrsirg97-rgb/rig/store/state/domain"
)

type scripted struct {
	mu      sync.Mutex
	inputs  []string
	named   []string
	inputed int
}

func (s *scripted) Input(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputed++
	if s.inputed > len(s.inputs) {
		return "", io.EOF
	}
	return s.inputs[s.inputed-1], nil
}

func (s *scripted) Notify(ev core.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.named = append(s.named, fmt.Sprintf("%T", ev))
}

func TestRecorderLandsTheTranscript(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sid := "rec-transcript"
	inner := &scripted{inputs: []string{"do it"}}
	rec := state.NewRecorder(inner, db, "/tmp/wt", "model-x", "0.1.0", sid, core.NewSession())

	ctx := context.Background()
	text, err := rec.Input(ctx)
	if err != nil || text != "do it" {
		t.Fatalf("input forwarding: %q %v", text, err)
	}
	rec.Notify(core.TextDelta{Text: "running "})
	rec.Notify(core.TextDelta{Text: "bash"})
	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)}})
	rec.Notify(core.Done{StopReason: "end_turn", Usage: core.Usage{Prompt: 5, Completion: 2}})

	rec.Notify(core.ToolResult{ID: "c1", Content: "out-1", Err: nil})
	if err := rec.Close("ok"); err != nil {
		t.Fatalf("close: %v", err)
	}

	user := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 1).Row()
	}).(*domain.Message)
	if user.Content != "do it" || user.Role != "user" {
		t.Fatalf("user message not landed: %+v", user)
	}
	a := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 2).Row()
	}).(*domain.Message)
	if a.Role != "assistant" || !strings.Contains(a.Content, "running bash") || a.ToolId == nil || *a.ToolId != "c1" {
		t.Fatalf("assistant message misattributed: %+v", a)
	}
	tc := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "c1").Row()
	}).(*domain.ToolCall)
	if tc.Result == nil || *tc.Result != "out-1" || tc.Err != nil {
		t.Fatalf("tool result not landed: %+v", tc)
	}
	u := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewUsageDomain().GetUsage(c, a.Seq).Row()
	}).(*domain.Usage)
	if u.Prompt != 5 || u.Completion != 2 {
		t.Fatalf("usage not landed: %+v", u)
	}
	s := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewSessionDomain().GetSession(c, sid).Row()
	}).(*domain.Session)
	if s.Exit != "ok" || s.EndedAt == nil {
		t.Fatalf("session closure not landed: %+v", s)
	}
}

func TestRecorderForwardsIntact(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), 1)
	if err != nil {
		t.Fatal(err)
	}
	inner := &scripted{}
	rec := state.NewRecorder(inner, db, "/tmp/wt", "model-x", "0.1.0", "rec-fwd", core.NewSession())
	rec.Notify(core.TextDelta{Text: "a"})
	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c9", Name: "bash", Args: json.RawMessage(`{}`)}})
	rec.Notify(core.Done{StopReason: "end_turn"})
	rec.Notify(core.Fault{Err: errors.New("late")})
	if inner.inputed != 0 || len(inner.named) != 4 {
		t.Fatalf("inner not forwarded intact: inputed=%d named=%v", inner.inputed, inner.named)
	}
	for i, want := range []string{"core.TextDelta", "core.ToolCallEvent", "core.Done", "core.Fault"} {
		if inner.named[i] != want {
			t.Fatalf("forwarding order broken at %d: %v", i, inner.named)
		}
	}
}

func TestRecorderFaultLands(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), 1)
	if err != nil {
		t.Fatal(err)
	}
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", "rec-fault", core.NewSession())
	if err := state.RecordSession(context.Background(), db, "rec-fault", "/tmp/wt", "model-x", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	rec.Notify(core.Fault{Err: errors.New("provider stream torn")})
	if err := rec.Close("fault"); err != nil {
		t.Fatalf("close: %v", err)
	}

	f := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewFaultDomain().GetFault(c, 1).Row()
	}).(*domain.Fault)
	if f.Message != "provider stream torn" || f.SessionId != "rec-fault" {
		t.Fatalf("fault row not landed: %+v", f)
	}
}

func TestRecorderObservationErrorsStayLoud(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), 1)
	if err != nil {
		t.Fatal(err)
	}
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", "rec-loud", core.NewSession())
	_ = state.RecordSession(context.Background(), db, "rec-loud", "/tmp/wt", "model-x", "0.1.0")
	if err := db.DB.Close(); err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)}})
	rec.Notify(core.Done{StopReason: "end_turn"})
	rec.Notify(core.ToolResult{ID: "cx", Content: "out"})
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "rec-loud") || !strings.Contains(string(out), "cx") {
		t.Errorf("observation failure not surfaced loudly: %q", out)
	}
}

func TestRecorderLandsToolOnlyTurns(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rec := state.NewRecorder(&scripted{inputs: []string{"do it"}}, db, "/tmp/wt", "model-x", "0.1.0", "rec-toolonly", core.NewSession())
	ctx := context.Background()
	if _, err := rec.Input(ctx); err != nil {
		t.Fatal(err)
	}

	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)}})
	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c2", Name: "ls", Args: json.RawMessage(`{}`)}})
	rec.Notify(core.Done{StopReason: "end_turn"})

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	rec.Notify(core.ToolResult{ID: "c1", Content: "out-1"})
	rec.Notify(core.ToolResult{ID: "c2", Content: "out-2"})
	w.Close()
	os.Stderr = old
	stderrOut, _ := io.ReadAll(r)
	if stderrOut != nil && len(stderrOut) > 0 {
		t.Errorf("stderr spammed on landed results: %q", stderrOut)
	}
	if err := rec.Close("ok"); err != nil {
		t.Fatalf("close: %v", err)
	}

	c1 := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "c1").Row()
	}).(*domain.ToolCall)
	if c1 == nil {
		t.Fatal("call c1 has no row")
	}
	c2 := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "c2").Row()
	}).(*domain.ToolCall)
	if c2 == nil {
		t.Fatal("call c2 has no row")
	}
	if c1.MessageSeq != c2.MessageSeq {
		t.Fatalf("calls attributed to different rows: %d vs %d", c1.MessageSeq, c2.MessageSeq)
	}
	if c1.Result == nil || *c1.Result != "out-1" || c2.Result == nil || *c2.Result != "out-2" {
		t.Fatalf("results not landed: %+v %+v", c1, c2)
	}
	asst := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, c1.MessageSeq).Row()
	}).(*domain.Message)
	if asst == nil || asst.Role != "assistant" || asst.Content != "" {
		t.Fatalf("assistant row not written for the tool-only turn: %+v", asst)
	}
}

func TestRecorderFaultDiscardsPartialText(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), 1)
	if err != nil {
		t.Fatal(err)
	}
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", "rec-discard", core.NewSession())
	rec.Notify(core.TextDelta{Text: "PARTIAL "})
	rec.Notify(core.Fault{Err: errors.New("stream torn")})
	rec.Notify(core.TextDelta{Text: "fresh"})
	rec.Notify(core.Done{StopReason: "end_turn"})
	if err := rec.Close("fault"); err != nil {
		t.Fatalf("close: %v", err)
	}

	first := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 1).Row()
	}).(*domain.Message)
	if first == nil || first.Content != "fresh" || first.Role != "assistant" {
		t.Fatalf("first landed row is not the clean one: %+v", first)
	}
	second := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 2).Row()
	}).(*domain.Message)
	if second != nil {
		t.Fatalf("a concatenated or partial row landed: %+v", second)
	}
}
