package perm_test

import (
	"context"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/middleware/perm"
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

func TestADenialRecordsAFinalRow(t *testing.T) {
	rec := &capture{}
	calls, content, err := run(t, perm.AllowlistWithDoor([]string{"bash"}, nil, rec), "file")
	if err == nil || calls != 0 {
		t.Fatalf("the denial must stand: (%q, %v)", content, err)
	}
	rows := rec.rows()
	if len(rows) != 1 {
		t.Fatalf("one final row per denial, got %d", len(rows))
	}
	if rows[0].Site != decision.SitePerm || rows[0].Answer != "no" || rows[0].Decider != decision.SitePerm {
		t.Fatalf("the row must name the site, the answer, and the decider: %+v", rows[0])
	}
	if rows[0].Question.Kind != decision.KindYesNo {
		t.Fatalf("the denial is a yes/no question: %+v", rows[0].Question)
	}
}

func TestAnAllowedCallRecordsNothing(t *testing.T) {
	rec := &capture{}
	calls, _, err := run(t, perm.AllowlistWithDoor([]string{"bash"}, nil, rec), "bash")
	if err != nil || calls != 1 {
		t.Fatalf("the allowed call must run: %v", err)
	}
	if rows := rec.rows(); len(rows) != 0 {
		t.Fatalf("the default path decides nothing, got %+v", rows)
	}
}
