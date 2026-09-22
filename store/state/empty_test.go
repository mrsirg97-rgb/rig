package state_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
)

func openRecorder(t *testing.T, sid string, inputs []string) (*state.Recorder, store.DB) {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rec := state.NewRecorder(&scripted{inputs: inputs}, db, "/tmp/wt", "model-x", "1.3.4", sid, core.NewSession())
	return rec, db
}

func TestRecorderCountsDiscardedUsage(t *testing.T) {
	rec, db := openRecorder(t, "rec-empty", []string{"do it"})
	ctx := context.Background()
	if _, err := rec.Input(ctx); err != nil {
		t.Fatalf("input: %v", err)
	}
	rec.Notify(core.ReasoningDelta{Text: "discarded thinking"})
	rec.Notify(core.EmptyTurn{Resample: 1, Limit: 2, Usage: core.Usage{Prompt: 100, Completion: 268, CacheRead: 99}})
	rec.Notify(core.ReasoningDelta{Text: "kept"})
	rec.Notify(core.TextDelta{Text: "answer"})
	rec.Notify(core.Done{StopReason: "stop", Usage: core.Usage{Prompt: 100, Completion: 10, CacheRead: 99}})

	rows, err := state.SessionUsage(ctx, db, "rec-empty")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("usage rows = %d, want the discarded row plus the good row: %+v", len(rows), rows)
	}
	if rows[0].Prompt != 100 || rows[0].Completion != 268 || rows[0].CacheRead != 99 {
		t.Fatalf("discarded usage = %+v, want it counted", rows[0])
	}
	if rows[1].Prompt != 100 || rows[1].Completion != 10 || rows[1].CacheRead != 99 {
		t.Fatalf("good usage = %+v, want the resample's own", rows[1])
	}

	sess, err := state.Resume(ctx, db, "rec-empty")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("the empty turn must leave no message row: %+v", sess.Messages)
	}
	if sess.Messages[1].Content != "answer" || sess.Messages[1].Reasoning != "kept" {
		t.Fatalf("the good turn must carry only its own reasoning: %+v", sess.Messages[1])
	}
}

func TestRecorderMergesDiscardedUsageRows(t *testing.T) {
	rec, db := openRecorder(t, "rec-empty-2", []string{"do it"})
	ctx := context.Background()
	if _, err := rec.Input(ctx); err != nil {
		t.Fatalf("input: %v", err)
	}
	rec.Notify(core.EmptyTurn{Resample: 1, Limit: 2, Usage: core.Usage{Prompt: 5, Completion: 1}})
	rec.Notify(core.EmptyTurn{Resample: 2, Limit: 2, Usage: core.Usage{Prompt: 7, Completion: 3}})
	rec.Notify(core.Fault{Err: errors.New("empty")})

	rows, err := state.SessionUsage(ctx, db, "rec-empty-2")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("usage rows = %d, want one merged row: %+v", len(rows), rows)
	}
	if rows[0].Prompt != 12 || rows[0].Completion != 4 {
		t.Fatalf("the two discarded usages must merge into the one row: %+v", rows[0])
	}
	sess, err := state.Resume(ctx, db, "rec-empty-2")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(sess.Messages) != 1 {
		t.Fatalf("the transcript must hold only the user prompt: %+v", sess.Messages)
	}
}
