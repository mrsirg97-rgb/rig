package todo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	todoapi "github.com/mrsirg97-rgb/rig/tool/todo"
)

func TestNotesActionRoundTripsThroughTheTool(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "gate"},
		map[string]any{"text": "work", "requires": "gate", "blocks": "after"},
		map[string]any{"text": "after"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	work := strings.Fields(strings.Split(reply, "\n")[3])[0]
	if !strings.Contains(reply, "· requires t1") || !strings.Contains(reply, "· blocks t3") {
		t.Errorf("the tool surface must carry both links:\n%s", reply)
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "note", "id": work, "note": "on it"}); err != nil {
		t.Fatalf("note: %v", err)
	}
	read, err := exec(t, tool, ctx, map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "· 1 note") {
		t.Errorf("read must show the count:\n%s", read)
	}
	if strings.Contains(read, "on it") {
		t.Errorf("read must not inline the note:\n%s", read)
	}
	notes, err := exec(t, tool, ctx, map[string]any{"action": "notes", "id": work})
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	if !strings.Contains(notes, work+" [ ] work · requires t1 · blocks t3") {
		t.Errorf("the notes header must carry the link lines:\n%s", notes)
	}
	if !strings.Contains(notes, "on it (by "+sess.ID+", ") {
		t.Errorf("each note must carry its session and time:\n%s", notes)
	}
	one, err := exec(t, tool, ctx, map[string]any{"action": "read", "id": work})
	if err != nil {
		t.Fatalf("read one: %v", err)
	}
	if !strings.Contains(one, "· 1 note") || !strings.Contains(one, "action 'notes' with id="+work+" lists them") {
		t.Errorf("read one must be summary-only and point at notes:\n%s", one)
	}
	if strings.Contains(one, "on it") {
		t.Errorf("read one must not inline the note:\n%s", one)
	}
	empty, err := exec(t, tool, ctx, map[string]any{"action": "notes", "id": strings.Fields(strings.Split(reply, "\n")[2])[0]})
	if err != nil {
		t.Fatalf("notes on a quiet task: %v", err)
	}
	if !strings.Contains(empty, "no notes on") {
		t.Errorf("a quiet task must say so:\n%s", empty)
	}
}

func TestNotesActionIsWorkerReadOnly(t *testing.T) {
	worker := todoapi.New(newDB(t), todoapi.Worker)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, worker, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "board entry"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := strings.Fields(strings.Split(reply, "\n")[2])[0]
	if _, err := exec(t, worker, ctx, map[string]any{"action": "note", "id": id, "note": "findings"}); err != nil {
		t.Fatalf("a worker's note must land: %v", err)
	}
	notes, err := exec(t, worker, ctx, map[string]any{"action": "notes", "id": id})
	if err != nil {
		t.Fatalf("a worker's notes read must land: %v", err)
	}
	if !strings.Contains(notes, "findings (by "+sess.ID+", ") {
		t.Errorf("the worker's notes read:\n%s", notes)
	}
}

func TestSchemaCarriesBothLinksAndNotes(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	schema := string(tool.Schema())
	for _, want := range []string{`"requires"`, `"blocks"`, `"notes"`} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema missing %s", want)
		}
	}
}

func TestToolDescriptionNamesTheLinks(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	if !strings.Contains(tool.Description(), "requires = I wait for it; blocks = it waits for me") {
		t.Errorf("the description must name the links in one line")
	}
}
