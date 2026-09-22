package todo_test

import (
	"context"
	"strings"
	"testing"

	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

func TestTaskReturnsTextAndNotesInOrderWithTheirSessions(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "the brief"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "the brief")
	if _, err := todostore.Note(ctx, db, p, id, "first thought", sessA); err != nil {
		t.Fatalf("note 1: %v", err)
	}
	if _, err := todostore.Note(ctx, db, p, id, "second thought", sessB); err != nil {
		t.Fatalf("note 2: %v", err)
	}
	task, err := todostore.Task(ctx, db, p, id, sessC)
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if task.ID != id {
		t.Errorf("task id = %q, want %q", task.ID, id)
	}
	if task.Text != "the brief" {
		t.Errorf("task text = %q, want %q", task.Text, "the brief")
	}
	if len(task.Notes) != 2 {
		t.Fatalf("notes = %v, want the two in order", task.Notes)
	}
	if task.Notes[0].Text != "first thought" || task.Notes[0].Session != sessA {
		t.Errorf("note 1 = %+v", task.Notes[0])
	}
	if task.Notes[1].Text != "second thought" || task.Notes[1].Session != sessB {
		t.Errorf("note 2 = %+v", task.Notes[1])
	}
}

func TestTaskOnAMissingIDRefusesInTheStoreVoice(t *testing.T) {
	db := newDB(t)
	if _, err := todostore.Task(context.Background(), db, p, "t99", sessA); err == nil {
		t.Fatal("a missing task read succeeded")
	} else if !strings.Contains(err.Error(), "no task 't99'") {
		t.Errorf("unknown-task voice: %v", err)
	}
}

func TestTaskReadsNothing(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "peeked"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "peeked")
	if _, err := todostore.Task(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("task: %v", err)
	}
	if got := eventCount(t, db); got != 1 {
		t.Errorf("a task read must append nothing: %d events", got)
	}
	if got := projStatus(t, db, "peeked"); got != "pending" {
		t.Errorf("a task read must not move the task: %v", got)
	}
}
