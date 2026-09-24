package todo_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

func TestReadShowsNoteCountAndNoNoteText(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "shared work"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "shared work")
	if _, err := todostore.Note(ctx, db, p, id, "first thought", sessA); err != nil {
		t.Fatalf("note 1: %v", err)
	}
	read, err := todostore.Read(ctx, db, p, sessC)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "· 1 note") {
		t.Errorf("one note must show the singular count:\n%s", read)
	}
	if strings.Contains(read, "first thought") {
		t.Errorf("read must not inline note text:\n%s", read)
	}
	if _, err := todostore.Note(ctx, db, p, id, "second thought", sessB); err != nil {
		t.Fatalf("note 2: %v", err)
	}
	if _, err := todostore.Note(ctx, db, p, id, "third thought", sessA); err != nil {
		t.Fatalf("note 3: %v", err)
	}
	read, err = todostore.Read(ctx, db, p, sessC)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "· 3 notes") {
		t.Errorf("three notes must show the plural count:\n%s", read)
	}
	for _, text := range []string{"first thought", "second thought", "third thought"} {
		if strings.Contains(read, text) {
			t.Errorf("read must not inline %q:\n%s", text, read)
		}
	}
}

func TestNotesActionListsNotesInOrderWithSessionAndTime(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "gate"},
		{Text: "shared work", Requires: ptrTo("gate"), Blocks: ptrTo("after")},
		{Text: "after"},
	}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "shared work")
	if _, err := todostore.Note(ctx, db, p, id, "first thought", sessA); err != nil {
		t.Fatalf("note 1: %v", err)
	}
	if _, err := todostore.Note(ctx, db, p, id, "second thought", sessB); err != nil {
		t.Fatalf("note 2: %v", err)
	}
	got, err := todostore.Notes(ctx, db, p, id, sessC)
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	if !strings.Contains(got, id+" [ ] shared work · requires t1 · blocks t3") {
		t.Errorf("the header must carry the task's link lines:\n%s", got)
	}
	first := strings.Index(got, "first thought (by "+sessA+", ")
	second := strings.Index(got, "second thought (by "+sessB+", ")
	if first == -1 || second == -1 {
		t.Fatalf("each note must carry its session and time:\n%s", got)
	}
	if first > second {
		t.Fatalf("notes must list in order:\n%s", got)
	}
	if n := len(regexp.MustCompile(`\(by [^,]+, \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\)`).FindAllString(got, -1)); n != 2 {
		t.Errorf("note times (RFC3339) = %d, want 2:\n%s", n, got)
	}
}

func TestNotesOnATaskWithoutNotesRepliesNoNotes(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "quiet"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "quiet")
	got, err := todostore.Notes(ctx, db, p, id, sessA)
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	if got != "no notes on "+id {
		t.Errorf("no-notes reply = %q, want %q", got, "no notes on "+id)
	}
}

func TestNotesOnAMissingTaskRefusesInTheStoreVoice(t *testing.T) {
	db := newDB(t)
	if _, err := todostore.Notes(context.Background(), db, p, "t99", sessA); err == nil {
		t.Fatal("notes on a missing task succeeded")
	} else if !strings.Contains(err.Error(), "no task 't99'") {
		t.Errorf("unknown-task voice: %v", err)
	}
}

func TestReadOneRendersSummaryOnlyAndPointsAtNotes(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "peeked"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "peeked")
	if _, err := todostore.Note(ctx, db, p, id, "a detail", sessA); err != nil {
		t.Fatalf("note: %v", err)
	}
	got, err := todostore.ReadOne(ctx, db, p, id, sessC)
	if err != nil {
		t.Fatalf("read one: %v", err)
	}
	if !strings.Contains(got, id+" [ ] peeked") {
		t.Errorf("the one-task read must render the task:\n%s", got)
	}
	if !strings.Contains(got, "· 1 note") {
		t.Errorf("the summary must carry the count:\n%s", got)
	}
	if !strings.Contains(got, "action 'notes' with id="+id+" lists them") {
		t.Errorf("the reply must point at the notes action:\n%s", got)
	}
	if strings.Contains(got, "a detail") {
		t.Errorf("read one must not inline note text:\n%s", got)
	}
}

func TestNotesSurviveCompactionWithTheirTime(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "noted"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "noted")
	if _, err := todostore.Note(ctx, db, p, id, "sticky", sessA); err != nil {
		t.Fatalf("note: %v", err)
	}
	age(t, db, 1010)
	if _, err := todostore.Move(ctx, db, p, id, 1, sessA); err != nil {
		t.Fatalf("move (compaction trigger): %v", err)
	}
	got, err := todostore.Notes(ctx, db, p, id, sessC)
	if err != nil {
		t.Fatalf("notes after compaction: %v", err)
	}
	if !strings.Contains(got, "sticky (by "+sessA+", ") {
		t.Errorf("a note must survive compaction with its session and time:\n%s", got)
	}
}

func TestReadCountSurvivesCompaction(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "counted"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "counted")
	if _, err := todostore.Note(ctx, db, p, id, "stick one", sessA); err != nil {
		t.Fatalf("note: %v", err)
	}
	if _, err := todostore.Note(ctx, db, p, id, "stick two", sessB); err != nil {
		t.Fatalf("note: %v", err)
	}
	age(t, db, 1010)
	if _, err := todostore.Move(ctx, db, p, id, 1, sessA); err != nil {
		t.Fatalf("move (compaction trigger): %v", err)
	}
	read, err := todostore.Read(ctx, db, p, sessC)
	if err != nil {
		t.Fatalf("read after compaction: %v", err)
	}
	if !strings.Contains(read, "· 2 notes") || strings.Contains(read, "stick one") || strings.Contains(read, "stick two") {
		t.Errorf("the count must survive compaction without inlining:\n%s", read)
	}
}
