package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
	"github.com/mrsirg97-rgb/rig/store/state/domain"
)

func openState(t *testing.T) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db
}

func TestRecorderLandsResultsFromTheEvent(t *testing.T) {
	db := openState(t)
	sid := "rec-event"
	session := core.NewSession()
	rec := state.NewRecorder(&scripted{inputs: []string{"do it"}}, db, "/tmp/wt", "model-x", "0.1.0", sid, session)

	ctx := context.Background()
	if _, err := rec.Input(ctx); err != nil {
		t.Fatal(err)
	}
	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)}})
	rec.Notify(core.Done{StopReason: "end_turn"})

	rec.Notify(core.ToolResult{ID: "c1", Content: "out-1", Err: nil})
	if err := rec.Close("ok"); err != nil {
		t.Fatal(err)
	}

	tc := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, sid, 2, "c1").Row()
	}).(*domain.ToolCall)
	if tc.Result == nil || *tc.Result != "out-1" || tc.Err != nil {
		t.Fatalf("the event-sourced result not landed: %+v", tc)
	}
}

func TestRecorderLandsGuardedFailuresFromTheEvent(t *testing.T) {
	db := openState(t)
	sid := "rec-fail"
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", sid, core.NewSession())
	_ = state.RecordSession(context.Background(), db, sid, "/tmp/wt", "model-x", "0.1.0")
	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c9", Name: "bash", Args: json.RawMessage(`{}`)}})
	rec.Notify(core.Done{StopReason: "end_turn"})
	rec.Notify(core.ToolResult{ID: "c9", Content: "bound exhausted: bash has failed 3 times; stop reissuing this call", Err: errors.New("bound exhausted")})
	if err := rec.Close("ok"); err != nil {
		t.Fatal(err)
	}
	tc := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, sid, 1, "c9").Row()
	}).(*domain.ToolCall)
	if tc.Err == nil || !strings.Contains(*tc.Err, "bound exhausted") {
		t.Fatalf("the guarded failure must land as a failure row: %+v", tc)
	}
}

func TestRecorderTurnEndDiscardsTheUnlandedPartial(t *testing.T) {
	db := openState(t)
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", "rec-turnend", core.NewSession())
	rec.Notify(core.TextDelta{Text: "PARTIAL "})
	rec.Notify(core.ReasoningDelta{Text: "PARTIAL thinking "})
	rec.Notify(core.TurnEnd{Reason: core.TurnInterrupt})
	rec.Notify(core.TextDelta{Text: "fresh"})
	rec.Notify(core.Done{StopReason: "end_turn"})
	if err := rec.Close("ok"); err != nil {
		t.Fatal(err)
	}
	first := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 1).Row()
	}).(*domain.Message)
	if first == nil || first.Content != "fresh" {
		t.Fatalf("the first landed row must be the clean one, got %+v", first)
	}
	if first.Reasoning != nil {
		t.Fatalf("the partial reasoning must not persist: %+v", first.Reasoning)
	}
	second := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 2).Row()
	}).(*domain.Message)
	if second != nil {
		t.Fatalf("a concatenated or partial row landed: %+v", second)
	}
}

func TestRecorderLandsReasoning(t *testing.T) {
	db := openState(t)
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", "rec-reason", core.NewSession())
	rec.Notify(core.ReasoningDelta{Text: "thinking "})
	rec.Notify(core.ReasoningDelta{Text: "done"})
	rec.Notify(core.TextDelta{Text: "answer"})
	rec.Notify(core.Done{StopReason: "end_turn"})
	if err := rec.Close("ok"); err != nil {
		t.Fatal(err)
	}
	m := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 1).Row()
	}).(*domain.Message)
	if m == nil || m.Reasoning == nil || *m.Reasoning != "thinking done" {
		t.Fatalf("the reasoning column must carry the accumulated thinking: %+v", m)
	}
	if m.Content != "answer" {
		t.Fatalf("the content must stay the answer: %+v", m)
	}
}

func TestRecorderLandsCacheUsage(t *testing.T) {
	db := openState(t)
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", "rec-cache", core.NewSession())
	rec.Notify(core.TextDelta{Text: "x"})
	rec.Notify(core.Done{StopReason: "end_turn", Usage: core.Usage{Prompt: 922, Completion: 10, CacheRead: 918, CacheWrite: 4}})
	if err := rec.Close("ok"); err != nil {
		t.Fatal(err)
	}
	u := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewUsageDomain().GetUsage(c, 1).Row()
	}).(*domain.Usage)
	if u.Prompt != 922 || u.Completion != 10 {
		t.Fatalf("usage not landed: %+v", u)
	}
	if u.CacheRead != 918 || u.CacheWrite != 4 {
		t.Fatalf("the cache columns must land from Done: %+v", u)
	}
}

func TestRecorderUpsertsFilesAtTheBoundary(t *testing.T) {
	db := openState(t)
	sid := "rec-files"
	session := core.NewSession()
	session.Files["/tmp/a.txt"] = core.FileState{Hash: "h1", Mtime: 100}
	rec := state.NewRecorder(&scripted{inputs: []string{"work"}}, db, "/tmp/wt", "model-x", "0.1.0", sid, session).Snapshot(func(s *core.Session) map[string]core.FileState {
		out := make(map[string]core.FileState, len(s.Files))
		for p, st := range s.Files {
			out[p] = st
		}
		return out
	})

	ctx := context.Background()
	if _, err := rec.Input(ctx); err != nil {
		t.Fatal(err)
	}
	f := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewFileDomain().GetFile(c, sid, "/tmp/a.txt").Row()
	}).(*domain.File)
	if f == nil || f.Hash != "h1" || f.Mtime != 100 {
		t.Fatalf("the files snapshot must land at the Input boundary: %+v", f)
	}

	session.Files["/tmp/a.txt"] = core.FileState{Hash: "h2", Mtime: 200}
	session.Files["/tmp/b.txt"] = core.FileState{Hash: "hb", Mtime: 300}
	rec.Notify(core.TextDelta{Text: "done"})
	rec.Notify(core.Done{StopReason: "end_turn"})
	if err := rec.Close("ok"); err != nil {
		t.Fatal(err)
	}
	again := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewFileDomain().GetFile(c, sid, "/tmp/a.txt").Row()
	}).(*domain.File)
	if again == nil || again.Hash != "h2" || again.Mtime != 200 {
		t.Fatalf("the upsert must replace the drifted row, not duplicate it: %+v", again)
	}
	b := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewFileDomain().GetFile(c, sid, "/tmp/b.txt").Row()
	}).(*domain.File)
	if b == nil || b.Hash != "hb" {
		t.Fatalf("the new path must insert: %+v", b)
	}
}

func TestRecorderEventErrorsStayLoud(t *testing.T) {
	db := openState(t)
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", "rec-loud2", core.NewSession())
	_ = state.RecordSession(context.Background(), db, "rec-loud2", "/tmp/wt", "model-x", "0.1.0")
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
	if !strings.Contains(string(out), "rec-loud2") || !strings.Contains(string(out), "cx") {
		t.Fatalf("the event-sourced observation failure not surfaced loudly: %q", out)
	}
}

func TestRecorderForwardsTheHardeningEventsIntact(t *testing.T) {
	db := openState(t)
	inner := &scripted{}
	rec := state.NewRecorder(inner, db, "/tmp/wt", "model-x", "0.1.0", "rec-fwd2", core.NewSession())
	rec.Notify(core.ToolStart{Call: core.ToolCall{ID: "c1", Name: "bash"}})
	rec.Notify(core.ToolResult{ID: "c1", Content: "out"})
	rec.Notify(core.TurnEnd{Reason: core.TurnOver})
	rec.Notify(core.TestEvent{Name: "x"})
	if len(inner.named) != 4 {
		t.Fatalf("inner not forwarded intact: %v", inner.named)
	}
	for i, want := range []string{"core.ToolStart", "core.ToolResult", "core.TurnEnd", "core.TestEvent"} {
		if inner.named[i] != want {
			t.Fatalf("forwarding order broken at %d: %v", i, inner.named)
		}
	}
}

func seedSession(t *testing.T, db store.DB, sid string) {
	t.Helper()
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, sid, "/tmp/wt", "model-x", "0.1.0"); err != nil {
		t.Fatal(err)
	}

	if _, err := state.RecordMessage(ctx, db, sid, "user", "do it", nil, nil); err != nil {
		t.Fatal(err)
	}

	ra := "the thinking behind the calls"
	if _, err := state.RecordMessage(ctx, db, sid, "assistant", "", &ra, nil); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolCall(ctx, db, sid, 2, "c1", "bash", `{"cmd":"ls"}`); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolCall(ctx, db, sid, 2, "c2", "edit", `{"path":"/tmp/a"}`); err != nil {
		t.Fatal(err)
	}
	out := "total 1"
	if err := state.RecordToolResult(ctx, db, sid, 2, "c1", out, nil); err != nil {
		t.Fatal(err)
	}

	if err := state.RecordFile(ctx, db, sid, "/tmp/a.txt", "h1", 100); err != nil {
		t.Fatal(err)
	}

	if _, err := state.RecordMessage(ctx, db, sid, "user", "again", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, sid, "assistant", "final answer", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestResumeRebuildsTheTranscript(t *testing.T) {
	db := openState(t)
	seedSession(t, db, "resume-full")

	sess, err := state.Resume(context.Background(), db, "resume-full")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if sess.ID != "resume-full" {
		t.Fatalf("the resumed session must keep its identity, got %q", sess.ID)
	}

	var want []core.Message
	for _, m := range sess.Messages {
		want = append(want, m)
	}
	if len(want) != 5 {
		t.Fatalf("transcript = %d messages, want 5 (user, assistant+calls, tool, user, assistant)\n%+v", len(want), want)
	}
	if want[0].Role != core.RoleUser || want[0].Content != "do it" {
		t.Fatalf("message 0 = %+v", want[0])
	}
	asst := want[1]
	if asst.Role != core.RoleAssistant || asst.Content != "" {
		t.Fatalf("message 1 = %+v", asst)
	}
	if asst.Reasoning != "the thinking behind the calls" {
		t.Fatalf("the reasoning must rebuild: %q", asst.Reasoning)
	}
	if len(asst.ToolCalls) != 2 || asst.ToolCalls[0].ID != "c1" || asst.ToolCalls[1].ID != "c2" {
		t.Fatalf("the calls must rebuild in row order: %+v", asst.ToolCalls)
	}
	if string(asst.ToolCalls[0].Args) != `{"cmd":"ls"}` {
		t.Fatalf("the args must reparse to raw JSON: %q", asst.ToolCalls[0].Args)
	}
	tool := want[2]
	if tool.Role != core.RoleTool || tool.ToolID != "c1" || tool.Content != "total 1" {
		t.Fatalf("message 2 = %+v, want the landed result as a tool message", tool)
	}

	if want[3].Role != core.RoleUser || want[3].Content != "again" {
		t.Fatalf("message 3 = %+v", want[3])
	}
	if want[4].Role != core.RoleAssistant || want[4].Content != "final answer" {
		t.Fatalf("message 4 = %+v", want[4])
	}

	fs, ok := sess.Files["/tmp/a.txt"]
	if !ok || fs.Hash != "h1" || fs.Mtime != 100 {
		t.Fatalf("the files must rebuild: %+v", sess.Files)
	}
}

func TestResumeDanglingCallSurvives(t *testing.T) {
	db := openState(t)
	seedSession(t, db, "resume-dangle")

	sess, err := state.Resume(context.Background(), db, "resume-dangle")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	var asst *core.Message
	var toolForC2 bool
	for i := range sess.Messages {
		if len(sess.Messages[i].ToolCalls) > 0 {
			m := sess.Messages[i]
			asst = &m
		}
		if sess.Messages[i].Role == core.RoleTool && sess.Messages[i].ToolID == "c2" {
			toolForC2 = true
		}
	}
	if asst == nil {
		t.Fatal("the assistant message with calls must survive")
	}
	ids := []string{}
	for _, c := range asst.ToolCalls {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "c1,c2" {
		t.Fatalf("both calls must survive the projection (dangling kept), got %v", ids)
	}
	if toolForC2 {
		t.Fatal("the dangling call must not gain a synthesized result")
	}
}

func TestResumeUnknownIdFailsLoud(t *testing.T) {
	db := openState(t)
	seedSession(t, db, "resume-known")

	_, err := state.Resume(context.Background(), db, "nope")
	if err == nil {
		t.Fatal("an unknown session id must fail")
	}
	if !strings.Contains(err.Error(), "no such session") || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("the failure must name the gap and the id, got %v", err)
	}
}
