package state_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
	"github.com/mrsirg97-rgb/rig/store/state/domain"
)

func TestRecorderLandsCompactedSummary(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	sid := "rec-compact-sum"
	sess := core.NewSession()
	sess.Append(core.Message{Role: core.RoleUser, Content: "[compaction] the summary"})
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", sid, sess)
	if err := state.RecordSession(context.Background(), db, sid, "/tmp/wt", "model-x", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	rec.Notify(core.Compacted{
		Summary: "[compaction] the summary",
		Dropped: 100, Kept: 50,
		Usage: core.Usage{Prompt: 3, Completion: 1},
	})
	rec.Notify(core.TextDelta{Text: "post"})
	rec.Notify(core.Done{StopReason: "end_turn", Usage: core.Usage{Prompt: 9, Completion: 4}})

	s := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 1).Row()
	}).(*domain.Message)
	if s.Role != "user" || s.Content != "[compaction] the summary" {
		t.Fatalf("summary row not landed: %+v", s)
	}
	u := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewUsageDomain().GetUsage(c, 1).Row()
	}).(*domain.Usage)
	if u.Prompt != 3 || u.Completion != 1 {
		t.Fatalf("summary usage not landed: %+v", u)
	}
	a := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 2).Row()
	}).(*domain.Message)
	if a.Role != "assistant" || a.Content != "post" {
		t.Fatalf("the next Done must land the assistant after the summary: %+v", a)
	}
}

func TestRecorderRelandsTheKeptTail(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	sid := "rec-rel-tail"
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, sid, "/tmp/wt", "model-x", "0.1.0"); err != nil {
		t.Fatal(err)
	}

	if _, err := state.RecordMessage(ctx, db, sid, "user", "old", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, sid, "assistant", "old-call", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolCall(ctx, db, sid, 2, "c1", "bash", `{"old":1}`); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolResult(ctx, db, sid, 2, "c1", "old-result", nil); err != nil {
		t.Fatal(err)
	}

	sess := core.NewSession()
	sess.Append(core.Message{Role: core.RoleUser, Content: "[compaction] sum"})
	sess.Append(core.Message{Role: core.RoleUser, Content: "tail user"})
	sess.Append(core.Message{Role: core.RoleAssistant, Content: "tail asst",
		ToolCalls: []core.ToolCall{{ID: "c2", Name: "edit", Args: json.RawMessage(`{"p":1}`)}}})
	sess.Append(core.Message{Role: core.RoleTool, ToolID: "c2", Content: "tail result"})
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", sid, sess)
	rec.Notify(core.Compacted{Summary: "[compaction] sum", Usage: core.Usage{Prompt: 1, Completion: 1}})

	s := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 3).Row()
	}).(*domain.Message)
	if s.Role != "user" || s.Content != "[compaction] sum" {
		t.Fatalf("summary row not landed: %+v", s)
	}
	tu := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 4).Row()
	}).(*domain.Message)
	if tu.Role != "user" || tu.Content != "tail user" {
		t.Fatalf("the tail's user row not re-landed: %+v", tu)
	}
	ta := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 5).Row()
	}).(*domain.Message)
	if ta.Role != "assistant" || ta.Content != "tail asst" || ta.ToolId == nil || *ta.ToolId != "c2" {
		t.Fatalf("the tail's assistant row not re-landed: %+v", ta)
	}

	fresh := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, sid, 5, "c2").Row()
	}).(*domain.ToolCall)
	if fresh.Name != "edit" || fresh.Args != `{"p":1}` || fresh.Result == nil || *fresh.Result != "tail result" {
		t.Fatalf("the fresh call row: %+v", fresh)
	}

	orig := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, sid, 2, "c1").Row()
	}).(*domain.ToolCall)
	if orig == nil || orig.Result == nil || *orig.Result != "old-result" {
		t.Fatalf("the original call row must stay: %+v", orig)
	}
}

func TestResumeAfterCompactionRebuildsTheCompactedShape(t *testing.T) {
	db := openState(t)
	sid := "resume-compact"
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, sid, "/tmp/wt", "model-x", "0.1.0"); err != nil {
		t.Fatal(err)
	}

	if _, err := state.RecordMessage(ctx, db, sid, "user", "u1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, sid, "assistant", "a1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolCall(ctx, db, sid, 2, "c1", "bash", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolResult(ctx, db, sid, 2, "c1", "r1", nil); err != nil {
		t.Fatal(err)
	}

	sess := core.NewSession()
	sess.Append(core.Message{Role: core.RoleUser, Content: "[compaction] SUM"})
	sess.Append(core.Message{Role: core.RoleUser, Content: "u2"})
	sess.Append(core.Message{Role: core.RoleAssistant, Content: "a2",
		ToolCalls: []core.ToolCall{{ID: "c2", Name: "bash", Args: json.RawMessage(`{}`)}}})
	sess.Append(core.Message{Role: core.RoleTool, ToolID: "c2", Content: "r2"})
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", sid, sess)
	rec.Notify(core.Compacted{Summary: "[compaction] SUM", Usage: core.Usage{Prompt: 2, Completion: 1}})

	rec.Notify(core.TextDelta{Text: "post"})
	rec.Notify(core.Done{StopReason: "end_turn", Usage: core.Usage{Prompt: 5, Completion: 2}})

	res, err := state.Resume(ctx, db, sid)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	want := []core.Message{
		{Role: core.RoleUser, Content: "[compaction] SUM"},
		{Role: core.RoleUser, Content: "u2"},
		{Role: core.RoleAssistant, Content: "a2", ToolCalls: []core.ToolCall{{ID: "c2", Name: "bash", Args: json.RawMessage(`{}`)}}},
		{Role: core.RoleTool, ToolID: "c2", Content: "r2"},
		{Role: core.RoleAssistant, Content: "post"},
	}
	if len(res.Messages) != len(want) {
		t.Fatalf("resumed transcript = %d messages, want %d (%v)\n%+v", len(res.Messages), len(want), want, res.Messages)
	}
	for i, m := range res.Messages {
		if m.Role != want[i].Role || m.Content != want[i].Content {
			t.Fatalf("message %d = %+v, want %+v (the compacted shape, not the full history)", i, m, want[i])
		}
		if m.Role == core.RoleAssistant && len(m.ToolCalls) != len(want[i].ToolCalls) {
			t.Fatalf("message %d calls = %+v, want %+v", i, m.ToolCalls, want[i].ToolCalls)
		}
		if m.Role == core.RoleTool && m.ToolID != want[i].ToolID {
			t.Fatalf("message %d tool id = %q, want %q (fresh id, the pair consistent)", i, m.ToolID, want[i].ToolID)
		}
	}
}
