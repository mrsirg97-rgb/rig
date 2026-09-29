package todo_test

import (
	"context"
	"strings"
	"testing"

	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

func TestStartWithoutIdStartsTheNextReadyTask(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "waits", Requires: ptrTo("ready")}, {Text: "ready"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ready := taskIDText(t, reply, "ready")
	got, err := todostore.Start(ctx, db, p, "", sessA, false)
	if err != nil {
		t.Fatalf("start without id: %v", err)
	}
	if !strings.Contains(got, "'"+ready+"' started") {
		t.Errorf("start without id must take next, skipping the blocked task:\n%s", got)
	}
}

func TestStartWithoutIdRefusesWhenNothingIsReady(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Start(ctx, db, p, "", sessA, false); err == nil {
		t.Fatal("start without id on an empty queue succeeded")
	} else if want := "nothing to start: no pending task is ready (name one as id)"; !strings.Contains(err.Error(), want) {
		t.Errorf("empty-queue voice: %v", err)
	}
}

func TestCompleteWithoutIdCompletesTheSessionsOneTaskInProgress(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "a"}, {Text: "b"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b := taskIDText(t, reply, "b")
	if _, err := todostore.Start(ctx, db, p, b, sessA, false); err != nil {
		t.Fatalf("start: %v", err)
	}
	got, err := todostore.Complete(ctx, db, p, "", sessA, false)
	if err != nil {
		t.Fatalf("complete without id: %v", err)
	}
	if !strings.Contains(got, "'"+b+"' completed") {
		t.Errorf("complete without id must finish the one in progress:\n%s", got)
	}
}

func TestCompleteWithoutIdRefusesWithNothingInProgress(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "a"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := todostore.Start(ctx, db, p, taskIDText(t, reply, "a"), sessB, false); err != nil {
		t.Fatalf("start by another session: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, "", sessA, false); err == nil {
		t.Fatal("complete without id finished another session's task")
	} else if want := "nothing in progress for this session (name the task as id)"; !strings.Contains(err.Error(), want) {
		t.Errorf("nothing-in-progress voice: %v", err)
	}
}

func TestCompleteWithoutIdRefusesNamingEveryTaskInProgress(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "a"}, {Text: "b"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a, b := taskIDText(t, reply, "a"), taskIDText(t, reply, "b")
	for _, id := range []string{a, b} {
		if _, err := todostore.Start(ctx, db, p, id, sessA, false); err != nil {
			t.Fatalf("start %s: %v", id, err)
		}
	}
	if _, err := todostore.Complete(ctx, db, p, "", sessA, false); err == nil {
		t.Fatal("complete without id chose between two tasks")
	} else if want := "2 tasks in progress (" + a + ", " + b + "); name one as id"; !strings.Contains(err.Error(), want) {
		t.Errorf("ambiguous voice, want %q: %v", want, err)
	}
}

func TestWorkerCompleteWithoutIdSubmitsTheTaskItClaimed(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "work"}}, sessB)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	work := taskIDText(t, reply, "work")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	got, err := todostore.Complete(ctx, db, p, "", sessA, true)
	if err != nil {
		t.Fatalf("worker complete without id: %v", err)
	}
	if !strings.Contains(got, "'"+work+"' completed; in review") {
		t.Errorf("a worker's bare complete submits its claim:\n%s", got)
	}
}
