package approve_test

import (
	"context"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/middleware/approve"
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

func TestTheAskRecordsItsVerdict(t *testing.T) {
	rec := &capture{}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "executed", nil
	}
	g := approve.Gate(func() string { return "manual" }, func(context.Context, string) bool { return true },
		func(string) bool { return true }, rec).Wrap(exec)
	out, err := g(context.Background(), call("bash", `{"cmd":"go test"}`))
	if err != nil || out != "executed" {
		t.Fatalf("an approved call must run unchanged: (%q, %v)", out, err)
	}
	rows := rec.rows()
	if len(rows) != 1 {
		t.Fatalf("one final row per ask, got %d", len(rows))
	}
	if rows[0].Site != decision.SiteApprove || rows[0].Answer != "yes" || rows[0].Decider != decision.SiteApprove {
		t.Fatalf("the row must name the site, the answer, and the decider: %+v", rows[0])
	}
	if rows[0].Question.Kind != decision.KindBinary {
		t.Fatalf("the ask is a yes/no question: %+v", rows[0].Question)
	}
}

func TestAutoRecordsNothing(t *testing.T) {
	rec := &capture{}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "executed", nil
	}
	g := approve.Gate(func() string { return "auto" }, func(context.Context, string) bool { return false },
		func(string) bool { return true }, rec).Wrap(exec)
	if _, err := g(context.Background(), call("bash", `{"cmd":"go test"}`)); err != nil {
		t.Fatal(err)
	}
	if rows := rec.rows(); len(rows) != 0 {
		t.Fatalf("auto mode decides nothing, got %+v", rows)
	}
}

func TestAStoreThatCannotBeWrittenLeavesTheResultUnchanged(t *testing.T) {
	path := t.TempDir() + "/decision.sqlite"
	db, _, _, err := store.Open(path, decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	rec := decisionstore.Recorder{DB: db}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "executed", nil
	}
	refusing := approve.Gate(func() string { return "manual" }, func(context.Context, string) bool { return false },
		func(string) bool { return true }).Wrap(exec)
	want, _ := refusing(context.Background(), call("bash", `{"cmd":"rm -rf /"}`))

	g := approve.Gate(func() string { return "manual" }, func(context.Context, string) bool { return false },
		func(string) bool { return true }, rec).Wrap(exec)
	got, err := g(context.Background(), call("bash", `{"cmd":"rm -rf /"}`))
	if err != nil || got != want {
		t.Fatalf("the refusal must be byte-identical with a dead store: (%q, %v) want %q", got, err, want)
	}
}
