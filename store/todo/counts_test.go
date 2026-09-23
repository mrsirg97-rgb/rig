package todo_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/store"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

func TestTodoCountsFromTheFold(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "work"}, {Text: "broken"}, {Text: "left"}, {Text: "over"},
	}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	work := taskIDText(t, reply, "work")
	broken := taskIDText(t, reply, "broken")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, work, sessA, true); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Accept(ctx, db, p, work, sessB); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := todostore.Start(ctx, db, p, broken, sessB, false); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := todostore.Fail(ctx, db, p, broken, sessB, false); err != nil {
		t.Fatalf("fail: %v", err)
	}
	before := countEvents(t, db)
	counts, err := todostore.Counts(ctx, db, p)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Pending != 2 || counts.Review != 0 || counts.Done != 1 || counts.Failed != 1 {
		t.Fatalf("counts = %+v, want pending 2 review 0 done 1 failed 1", counts)
	}
	if got := countEvents(t, db); got != before {
		t.Fatalf("the read wrote %d events (before %d)", got-before, before)
	}
}

func TestTodoCountsEmptyQueue(t *testing.T) {
	db := newDB(t)
	counts, err := todostore.Counts(context.Background(), db, p)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts != (todostore.QueueCounts{}) {
		t.Fatalf("an empty queue counts zero: %+v", counts)
	}
}

func countEvents(t *testing.T, db store.DB) int {
	t.Helper()
	rows, err := db.DB.Query(`SELECT COUNT(*) FROM events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no count row")
	}
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
