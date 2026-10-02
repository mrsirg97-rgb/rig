package paths_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/middleware/paths"
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

func TestAnExpansionRecordsAFinalRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rec := &capture{}
	exec := paths.Middleware(rec).Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ok", nil
	})
	if _, err := exec(context.Background(), core.ToolCall{Name: "read", Args: json.RawMessage(`{"path":"~/a.txt"}`)}); err != nil {
		t.Fatal(err)
	}
	rows := rec.rows()
	if len(rows) != 1 {
		t.Fatalf("one final row per expansion, got %d", len(rows))
	}
	if rows[0].Site != decision.SitePaths || rows[0].Decider != decision.SitePaths {
		t.Fatalf("the row must name the site and the decider: %+v", rows[0])
	}
	if rows[0].Answer != filepath.Join(home, "a.txt") {
		t.Fatalf("the answer is the expanded value: %+v", rows[0])
	}
	if !strings.Contains(rows[0].State, "~/a.txt") {
		t.Fatalf("the state carries the raw value: %+v", rows[0])
	}
}

func TestAPassThroughRecordsNothing(t *testing.T) {
	rec := &capture{}
	exec := paths.Middleware(rec).Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ok", nil
	})
	if _, err := exec(context.Background(), core.ToolCall{Name: "read", Args: json.RawMessage(`{"path":"/tmp/a.txt"}`)}); err != nil {
		t.Fatal(err)
	}
	if rows := rec.rows(); len(rows) != 0 {
		t.Fatalf("a pass-through decides nothing, got %+v", rows)
	}
}

func TestAStoreThatCannotBeWrittenLeavesTheRewriteUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "decision.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var seenWanted, seenGot string
	inner := func(ctx context.Context, call core.ToolCall) (string, error) {
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(call.Args, &a) == nil {
			return a.Path, nil
		}
		return "", nil
	}
	want, _ := paths.Middleware().Wrap(inner)(context.Background(), core.ToolCall{Name: "read", Args: json.RawMessage(`{"path":"~/a.txt"}`)})
	seenWanted = want
	got, err := paths.Middleware(decisionstore.Recorder{DB: db}).Wrap(inner)(context.Background(), core.ToolCall{Name: "read", Args: json.RawMessage(`{"path":"~/a.txt"}`)})
	seenGot = got
	if err != nil || seenGot != seenWanted {
		t.Fatalf("the rewrite must be identical with a dead store: (%q, %v) want %q", seenGot, err, seenWanted)
	}
}
