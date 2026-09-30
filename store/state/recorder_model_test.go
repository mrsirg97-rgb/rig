package state_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	"github.com/mrsirg97-rgb/rig/v2/store/state/domain"
)

func TestRecorderStampsUsageWithTheServedModel(t *testing.T) {
	rec, db := openRecorder(t, "rec-model", []string{"do it"})
	ctx := context.Background()
	if _, err := rec.Input(ctx); err != nil {
		t.Fatalf("input: %v", err)
	}
	rec.Notify(core.TextDelta{Text: "answer"})
	rec.Notify(core.Done{StopReason: "stop", Model: "ox-alpha", Usage: core.Usage{Prompt: 5, Completion: 2}})

	rows, err := state.SessionUsage(ctx, db, "rec-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Model != "ox-alpha" {
		t.Fatalf("usage rows = %+v, want the served id ox-alpha", rows)
	}
}

func TestRecorderStampsADiscardedTurnWithItsModel(t *testing.T) {
	rec, db := openRecorder(t, "rec-empty-model", []string{"do it"})
	ctx := context.Background()
	if _, err := rec.Input(ctx); err != nil {
		t.Fatalf("input: %v", err)
	}
	rec.Notify(core.EmptyTurn{Resample: 1, Limit: 2, Model: "ox-alpha", Usage: core.Usage{Prompt: 100, Completion: 268}})
	rec.Notify(core.Fault{Err: errors.New("empty")})

	rows, err := state.SessionUsage(ctx, db, "rec-empty-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Model != "ox-alpha" {
		t.Fatalf("usage rows = %+v, want the discarded attempt's id ox-alpha", rows)
	}
}

func TestRecorderStampsTheSummaryCallWithItsModel(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	sid := "rec-compact-model"
	sess := core.NewSession()
	sess.Append(core.Message{Role: core.RoleUser, Content: "[compaction] the summary"})
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", sid, sess)
	rec.Notify(core.Compacted{
		Summary: "[compaction] the summary",
		Model:   "ox-alpha",
		Usage:   core.Usage{Prompt: 3, Completion: 1},
	})

	u := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewUsageDomain().GetUsage(c, 1).Row()
	}).(*domain.Usage)
	if u.Model == nil || *u.Model != "ox-alpha" {
		t.Fatalf("summary usage model = %v, want ox-alpha", u.Model)
	}
}
