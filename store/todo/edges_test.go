package todo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

func TestSevenLeavesBlockingARootGateClaimAndComplete(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	var items []item
	for i := 1; i <= 7; i++ {
		items = append(items, item{Text: fmt.Sprintf("leaf %d", i), Blocks: ptrTo("root")})
	}
	items = append(items, item{Text: "root"})
	reply, err := todostore.Create(ctx, db, p, items, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	root := taskIDText(t, reply, "root")
	leaves := make([]string, 7)
	for i := 1; i <= 7; i++ {
		leaves[i-1] = taskIDText(t, reply, fmt.Sprintf("leaf %d", i))
	}
	if !strings.Contains(reply, "· blocks "+root) {
		t.Errorf("the leaves must name the block:\n%s", reply)
	}
	if !strings.Contains(reply, "· waits for 7") {
		t.Errorf("the target must show the blocker count:\n%s", reply)
	}
	if _, err := todostore.Complete(ctx, db, p, root, sessA, false); err == nil {
		t.Fatal("a blocked root completed")
	} else {
		for _, leaf := range leaves {
			if !strings.Contains(err.Error(), "'"+leaf+"'") {
				t.Errorf("the refusal must name '%s': %v", leaf, err)
			}
		}
	}
	for i := 0; i < 7; i++ {
		claimed, err := todostore.Claim(ctx, db, p, sessB, "")
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if !strings.Contains(claimed, "'"+leaves[i]+"' claimed") {
			t.Fatalf("claim %d must take leaf %d:\n%s", i, i, claimed)
		}
		if _, err := todostore.Complete(ctx, db, p, leaves[i], sessB, false); err != nil {
			t.Fatalf("complete leaf %d: %v", i, err)
		}
		if i < 6 {
			if _, err := todostore.Complete(ctx, db, p, root, sessA, false); err == nil {
				t.Fatalf("root completed with leaf %d unfinished", i)
			}
		}
	}
	claimed, err := todostore.Claim(ctx, db, p, sessB, "")
	if err != nil {
		t.Fatalf("claim root: %v", err)
	}
	if !strings.Contains(claimed, "'"+root+"' claimed") {
		t.Fatalf("the last leaf must clear the block:\n%s", claimed)
	}
	if _, err := todostore.Complete(ctx, db, p, root, sessB, false); err != nil {
		t.Fatalf("complete root: %v", err)
	}
}

func TestRequiresChainGatesTheFinish(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "a", Requires: ptrTo("b")},
		{Text: "b", Requires: ptrTo("c")},
		{Text: "c"},
	}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a := taskIDText(t, reply, "a")
	b := taskIDText(t, reply, "b")
	c := taskIDText(t, reply, "c")
	if !strings.Contains(reply, "· requires "+b) || !strings.Contains(reply, "· requires "+c) {
		t.Errorf("the requires links must show:\n%s", reply)
	}
	if _, err := todostore.Complete(ctx, db, p, a, sessA, false); err == nil || !strings.Contains(err.Error(), "'"+b+"'") {
		t.Fatalf("the refusal must name what a waits for: %v", err)
	}
	for _, task := range []string{c, b, a} {
		claimed, err := todostore.Claim(ctx, db, p, sessB, "")
		if err != nil {
			t.Fatalf("claim %s: %v", task, err)
		}
		if !strings.Contains(claimed, "'"+task+"' claimed") {
			t.Fatalf("the chain must be claimed in order %s:\n%s", task, claimed)
		}
		if _, err := todostore.Complete(ctx, db, p, task, sessB, false); err != nil {
			t.Fatalf("complete %s: %v", task, err)
		}
	}
}

func TestOneTaskCarriesBothEdges(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "first"},
		{Text: "mid", Requires: ptrTo("first"), Blocks: ptrTo("last")},
		{Text: "last"},
	}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	first := taskIDText(t, reply, "first")
	mid := taskIDText(t, reply, "mid")
	last := taskIDText(t, reply, "last")
	if !strings.Contains(reply, mid+" [ ] mid · requires "+first+" · blocks "+last) {
		t.Errorf("one task must carry both links:\n%s", reply)
	}
	if _, err := todostore.Complete(ctx, db, p, mid, sessA, false); err == nil || !strings.Contains(err.Error(), "'"+first+"'") {
		t.Fatalf("mid's finish must wait for first: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, last, sessA, false); err == nil || !strings.Contains(err.Error(), "'"+mid+"'") {
		t.Fatalf("last's finish must wait for mid: %v", err)
	}
	for _, task := range []string{first, mid, last} {
		claimed, err := todostore.Claim(ctx, db, p, sessB, "")
		if err != nil {
			t.Fatalf("claim %s: %v", task, err)
		}
		if !strings.Contains(claimed, "'"+task+"' claimed") {
			t.Fatalf("both links must gate in order %s:\n%s", task, claimed)
		}
		if _, err := todostore.Complete(ctx, db, p, task, sessB, false); err != nil {
			t.Fatalf("complete %s: %v", task, err)
		}
	}
}

func TestBlockCycleRefusesWithTheTaskNames(t *testing.T) {
	db := newDB(t)
	_, err := todostore.Create(context.Background(), db, p, []item{
		{Text: "a", Blocks: ptrTo("b")},
		{Text: "b", Blocks: ptrTo("a")},
	}, "s1")
	if err == nil {
		t.Fatal("expected refusal")
	}
	if !strings.Contains(err.Error(), "cycle") || !strings.Contains(err.Error(), "t1 -> t2 -> t1") {
		t.Errorf("the cycle must name the tasks: %v", err)
	}
}

func TestCycleThroughEitherRelationNamesEveryTask(t *testing.T) {
	db := newDB(t)
	_, err := todostore.Create(context.Background(), db, p, []item{
		{Text: "a", Requires: ptrTo("b"), Blocks: ptrTo("c")},
		{Text: "b", Requires: ptrTo("c")},
		{Text: "c"},
	}, "s1")
	if err == nil {
		t.Fatal("expected refusal")
	}
	if !strings.Contains(err.Error(), "cycle") || !strings.Contains(err.Error(), "t1 -> t2 -> t3 -> t1") {
		t.Errorf("a cycle through either relation must name every task: %v", err)
	}
}

func TestUnknownLinkRefusesNamingTheFieldAndTask(t *testing.T) {
	db := newDB(t)
	if _, err := todostore.Create(context.Background(), db, p, []item{{Text: "a", Requires: ptrTo("nope")}}, "s1"); err == nil {
		t.Fatal("unknown requires succeeded")
	} else if !strings.Contains(err.Error(), "requires 'nope' not found") {
		t.Errorf("unknown-requires voice: %v", err)
	}
	if _, err := todostore.Create(context.Background(), db, p, []item{{Text: "a", Blocks: ptrTo("nope")}}, "s1"); err == nil {
		t.Fatal("unknown blocks succeeded")
	} else if !strings.Contains(err.Error(), "blocks 'nope' not found") {
		t.Errorf("unknown-blocks voice: %v", err)
	}
}

func TestSelfLinkRefusesNamingTheTaskAndRelation(t *testing.T) {
	db := newDB(t)
	if _, err := todostore.Create(context.Background(), db, p, []item{{Text: "a", Requires: ptrTo("a")}}, "s1"); err == nil {
		t.Fatal("self requires succeeded")
	} else if !strings.Contains(err.Error(), "'a' cannot require itself") {
		t.Errorf("self-requires voice: %v", err)
	}
	if _, err := todostore.Create(context.Background(), db, p, []item{{Text: "a", Blocks: ptrTo("a")}}, "s1"); err == nil {
		t.Fatal("self blocks succeeded")
	} else if !strings.Contains(err.Error(), "'a' cannot block itself") {
		t.Errorf("self-blocks voice: %v", err)
	}
}

func TestOldDependsOnPayloadFoldsToRequires(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	args, _ := json.Marshal(map[string]any{
		"tasks": []any{
			map[string]any{"text": "gate"},
			map[string]any{"text": "work", "dependsOn": "gate"},
		},
	})
	rawExec(t, db, "INSERT INTO events (ts, op, args, session, scope) VALUES (?, 'create', ?, NULL, 'ws')",
		"2026-01-01T00:00:00Z", string(args))
	reply, err := todostore.Read(ctx, db, p, sessA)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(reply, "· requires t1") {
		t.Errorf("old dependsOn must fold as requires:\n%s", reply)
	}
	if strings.Contains(reply, "dependsOn") {
		t.Errorf("the old field must not leak:\n%s", reply)
	}
	work := taskIDText(t, reply, "work")
	if _, err := todostore.Complete(ctx, db, p, work, sessA, false); err == nil || !strings.Contains(err.Error(), "blocked by") {
		t.Fatalf("the folded requires must gate the finish: %v", err)
	}
}

func TestOldSnapshotDependsOnFoldsToRequires(t *testing.T) {
	db := newDB(t)
	args, _ := json.Marshal(map[string]any{
		"maxId": 2, "maxPos": 2,
		"tasks": []any{
			map[string]any{"id": "t1", "text": "gate", "status": "pending", "pos": 0},
			map[string]any{"id": "t2", "text": "work", "status": "pending", "pos": 1, "dependsOn": "t1"},
		},
	})
	rawExec(t, db, "INSERT INTO events (ts, op, args, session, scope) VALUES (?, 'compact', ?, NULL, 'ws')",
		"2026-01-02T00:00:00Z", string(args))
	reply, err := todostore.Read(context.Background(), db, p, sessA)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(reply, "· requires t1") {
		t.Errorf("an old snapshot's dependsOn must fold as requires:\n%s", reply)
	}
}

func TestEdgesSurviveCompactionAndReplay(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "gate"},
		{Text: "work", Requires: ptrTo("gate")},
		{Text: "tail", Blocks: ptrTo("work")},
	}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gate := taskIDText(t, reply, "gate")
	work := taskIDText(t, reply, "work")
	tail := taskIDText(t, reply, "tail")
	age(t, db, 1010)
	if _, err := todostore.Move(ctx, db, p, tail, 1, sessA); err != nil {
		t.Fatalf("move (compaction trigger): %v", err)
	}
	read, err := todostore.Read(ctx, db, p, sessA)
	if err != nil {
		t.Fatalf("read after compaction: %v", err)
	}
	if !strings.Contains(read, "· requires "+gate) || !strings.Contains(read, "· blocks "+work) {
		t.Errorf("the links must survive compaction:\n%s", read)
	}
	if !strings.Contains(read, "· waits for 1") {
		t.Errorf("the block must survive compaction:\n%s", read)
	}
	if _, err := todostore.Complete(ctx, db, p, work, sessA, false); err == nil || !strings.Contains(err.Error(), "'"+gate+"'") || !strings.Contains(err.Error(), "'"+tail+"'") {
		t.Fatalf("both links must gate after compaction: %v", err)
	}
	rawExec(t, db, "DELETE FROM tasks WHERE scope = 'ws'")
	rawExec(t, db, "DELETE FROM task_deps WHERE scope = 'ws'")
	replayed, err := todostore.Read(ctx, db, p, sessA)
	if err != nil {
		t.Fatalf("read after tamper: %v", err)
	}
	if !strings.Contains(replayed, "· requires "+gate) || !strings.Contains(replayed, "· blocks "+work) {
		t.Errorf("the links must survive replay from the log:\n%s", replayed)
	}
}

func TestAcceptOnABlockedReviewTaskRefusesNamingWhatItWaitsFor(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "gate"},
		{Text: "work", Requires: ptrTo("gate")},
	}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gate := taskIDText(t, reply, "gate")
	work := taskIDText(t, reply, "work")
	claimed, err := todostore.Claim(ctx, db, p, sessA, "")
	if err != nil {
		t.Fatalf("claim gate: %v", err)
	}
	if !strings.Contains(claimed, "'"+gate+"' claimed") {
		t.Fatalf("the unblocked gate must be first: %s", claimed)
	}
	if _, err := todostore.Complete(ctx, db, p, gate, sessA, false); err != nil {
		t.Fatalf("complete gate: %v", err)
	}
	claimed, err = todostore.Claim(ctx, db, p, sessA, "")
	if err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if !strings.Contains(claimed, "'"+work+"' claimed") {
		t.Fatalf("claim after gate done: %s", claimed)
	}
	if _, err := todostore.Complete(ctx, db, p, work, sessA, true); err != nil {
		t.Fatalf("complete work to review: %v", err)
	}
	postReply, err := todostore.Create(ctx, db, p, []item{{Text: "post", Blocks: ptrTo("work")}}, sessA)
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	post := taskIDText(t, postReply, "post")
	before := eventCount(t, db)
	if _, err := todostore.Accept(ctx, db, p, work, sessB); err == nil || !strings.Contains(err.Error(), "'"+post+"'") {
		t.Fatalf("accept on a blocked review task must refuse naming what it waits for: %v", err)
	}
	if got := eventCount(t, db); got != before {
		t.Errorf("a refused accept must append nothing: %d -> %d events", before, got)
	}
	claimed, err = todostore.Claim(ctx, db, p, sessA, "")
	if err != nil {
		t.Fatalf("claim post: %v", err)
	}
	if !strings.Contains(claimed, "'"+post+"' claimed") {
		t.Fatalf("the blocked review's blocker must be the next claim: %s", claimed)
	}
	if _, err := todostore.Complete(ctx, db, p, post, sessA, false); err != nil {
		t.Fatalf("complete post: %v", err)
	}
	if _, err := todostore.Accept(ctx, db, p, work, sessB); err != nil {
		t.Fatalf("accept work after the blocker done: %v", err)
	}
}
