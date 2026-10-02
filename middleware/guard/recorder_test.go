package guard_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/middleware/guard"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
)

type capture struct {
	mu    sync.Mutex
	final []decision.Final
}

func (c *capture) Record(ctx context.Context, f decision.Final) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.final = append(c.final, f)
}

func (c *capture) rows() []decision.Final {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]decision.Final(nil), c.final...)
}

func TestTheBoundRefusalRecordsAFinalRow(t *testing.T) {
	rec := &capture{}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "nope", errors.New("nope")
	}
	g := guard.Bound(2, rec).Wrap(exec)
	a := core.ToolCall{ID: "c1", Name: "edit", Args: json.RawMessage(`{"path":"a"}`)}
	for i := 0; i < 2; i++ {
		g(context.Background(), a)
	}
	if rows := rec.rows(); len(rows) != 0 {
		t.Fatalf("a failing call is not a refusal, got %+v", rows)
	}
	out, _ := g(context.Background(), a)
	if !strings.Contains(out, "bound exhausted") {
		t.Fatalf("the bound must refuse: %q", out)
	}
	rows := rec.rows()
	if len(rows) != 1 {
		t.Fatalf("one final row per bound refusal, got %d", len(rows))
	}
	if rows[0].Site != decision.SiteGuard || rows[0].Answer != "no" || rows[0].Decider != decision.SiteGuard {
		t.Fatalf("the row must name the site, the answer, and the decider: %+v", rows[0])
	}
}

func TestTheRoundCapRecordsAFinalRow(t *testing.T) {
	rec := &capture{}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	}
	g := guard.Rounds(2, rec).Wrap(exec)
	for i := 0; i < 2; i++ {
		if _, err := g(context.Background(), core.ToolCall{ID: "c1", Name: "bash"}); err != nil {
			t.Fatal(err)
		}
	}
	if rows := rec.rows(); len(rows) != 0 {
		t.Fatalf("calls inside the cap decide nothing, got %+v", rows)
	}
	out, _ := g(context.Background(), core.ToolCall{ID: "c1", Name: "bash"})
	if !strings.Contains(out, "round cap") {
		t.Fatalf("the round cap must refuse: %q", out)
	}
	rows := rec.rows()
	if len(rows) != 1 || rows[0].Site != decision.SiteGuard || rows[0].Answer != "no" {
		t.Fatalf("one final row per round-cap refusal: %+v", rows)
	}
}

func TestAStoreThatCannotBeWrittenLeavesTheRefusalUnchanged(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "decision.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "nope", errors.New("nope")
	}
	call := core.ToolCall{ID: "c1", Name: "edit", Args: json.RawMessage(`{"path":"a"}`)}
	plain := guard.Bound(1).Wrap(exec)
	want, wantErr := plain(context.Background(), call)

	g := guard.Bound(1, decisionstore.Recorder{DB: db}).Wrap(exec)
	got, err := g(context.Background(), call)
	if got != want || (err == nil) != (wantErr == nil) {
		t.Fatalf("the refusal must be identical with a dead store: (%q, %v) want (%q, %v)", got, err, want, wantErr)
	}
}
