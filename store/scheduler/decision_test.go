package scheduler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
)

type decisionCapture struct {
	final []decision.Final
}

func (c *decisionCapture) Record(ctx context.Context, f decision.Final) {
	c.final = append(c.final, f)
}

func TestASkipRecordsADecisionRow(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	cwd, _ := jobsRow(t, h, "j1")["cwd"].(string)
	rec := &decisionCapture{}
	spawn := &fakeSpawn{}
	opts := runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{})
	opts.Decisions = rec
	mustOK(t, sched.RunJob(key, opts))
	if len(spawn.calls) != 0 {
		t.Fatalf("a skip spawns nothing")
	}
	if len(rec.final) != 1 {
		t.Fatalf("one final row per skip, got %d", len(rec.final))
	}
	row := rec.final[0]
	if row.Site != decision.SiteScheduler || row.Answer != "no" || row.Decider != decision.SiteScheduler {
		t.Fatalf("the row must name the site, the answer, and the decider: %+v", row)
	}
	if row.Question.Kind != decision.KindYesNo {
		t.Fatalf("the fire question is yes/no: %+v", row.Question)
	}
	if row.Scope != scope.Key(cwd) {
		t.Fatalf("the row carries the job's project scope: %q, want %q", row.Scope, scope.Key(cwd))
	}
	if !strings.Contains(row.State, "j1") || !strings.Contains(row.State, "held by") {
		t.Fatalf("the state names the job and the reason: %q", row.State)
	}
}
